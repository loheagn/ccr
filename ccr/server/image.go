package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/google/uuid"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/archive"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/images"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/rrw"
	ver "github.com/opencontainers/image-spec/specs-go"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

var client *containerd.Client
var defaultPlatform v1.Platform = platforms.DefaultSpec()

func getClient() *containerd.Client {
	if client != nil {
		return client
	}

	var err error
	client, err = containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		panic(err)
	}

	return client
}

func getCtx() context.Context {
	return namespaces.WithNamespace(context.Background(), "k8s.io")
}

func writeContent(ctx context.Context, store content.Ingester, mediaType, ref string, r io.Reader, opts ...content.Opt) (d v1.Descriptor, err error) {
	writer, err := store.Writer(ctx, content.WithRef(ref))
	if err != nil {
		return d, err
	}
	defer writer.Close()
	size, err := io.Copy(writer, r)
	if err != nil {
		return d, err
	}

	if err := writer.Commit(ctx, size, "", opts...); err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return d, err
		}
	}
	return v1.Descriptor{
		MediaType: mediaType,
		Digest:    writer.Digest(),
		Size:      size,
	}, nil
}

func writeIndex(ctx context.Context, index *imagespec.Index, client *containerd.Client, ref string) (d imagespec.Descriptor, err error) {
	labels := map[string]string{}
	for i, m := range index.Manifests {
		labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i)] = m.Digest.String()
	}
	data, err := json.Marshal(index)
	if err != nil {
		return imagespec.Descriptor{}, err
	}
	return writeContent(ctx, client.ContentStore(), imagespec.MediaTypeImageIndex, ref, bytes.NewReader(data), content.WithLabels(labels))
}

func imageRename(image string) string {
	split := strings.Split(image, "/")
	lastPart := split[len(split)-1]

	tagSplit := strings.Split(lastPart, ":")
	if len(tagSplit) > 1 {
		return tagSplit[0] + "-" + tagSplit[1]
	}

	// Default tag is latest if no tag is specified
	return tagSplit[0] + "-latest"
}

func simpleConvertImage(originalImagename string) (string, error) {
	imageR := imageRename(originalImagename)
	ref := getRef(imageR)
	err := exec.Command("bash", "-c", fmt.Sprintf("nerdctl -n k8s.io image pull %s", originalImagename)).Run()
	if err != nil {
		return "", err
	}

	err = exec.Command("bash", "-c", fmt.Sprintf("nerdctl -n k8s.io image tag %s %s", originalImagename, ref)).Run()
	if err != nil {
		return "", err
	}

	err = exec.Command("bash", "-c", fmt.Sprintf("nerdctl -n k8s.io image push %s", ref)).Run()
	if err != nil {
		return "", err
	}

	return ref, nil
}

func convertImage(originalImagename string) (string, error) {
	ctx := getCtx()
	client := getClient()

	_, err := client.Pull(ctx, originalImagename, containerd.WithPullUnpack, containerd.WithPlatform(platforms.DefaultString()))
	if err != nil {
		return "", err
	}

	mountDir, err := os.MkdirTemp("/tmp", "mount-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(mountDir)

	err = exec.Command("bash", "-c", fmt.Sprintf("ctr -n k8s.io images mount %s %s", originalImagename, mountDir)).Run()
	if err != nil {
		return "", err
	}
	defer exec.Command("bash", "-c", fmt.Sprintf("umount %s", mountDir)).Run()

	file, err := os.CreateTemp("/tmp", "origin-tar-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())

	mount.WithReadonlyTempMount(ctx, []mount.Mount{
		{
			Type:    "bind",
			Source:  mountDir,
			Options: []string{"ro", "rbind"},
		},
	}, func(upperRoot string) error {
		emptyDir, err := os.MkdirTemp("/tmp", "")
		if err != nil {
			return fmt.Errorf("failed to create tmp dir when try do full diff: %w", err)
		}
		defer os.RemoveAll(emptyDir)

		if errOpen := archive.WriteDiff(ctx, file, emptyDir, upperRoot); errOpen != nil {
			return fmt.Errorf("failed to write diff: %w", errOpen)
		}
		return nil
	})

	file.Close()

	metaFileName, err := rrw.SplitTar(ctx, file.Name())
	if err != nil {
		return "", fmt.Errorf("failed to split tar: %w", err)
	}
	defer func() {
		os.Remove(metaFileName)
	}()

	index := &imagespec.Index{
		Versioned: ver.Versioned{
			SchemaVersion: 2,
		},
		Annotations: make(map[string]string),
	}

	imageR := imageRename(originalImagename)

	writeFileToCS := func(filename string, mediaType, ref string) error {
		fileStat, err := os.Stat(filename)
		if err != nil {
			return err
		}
		if fileStat.Size() == 0 {
			return nil
		}

		fileReader, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer fileReader.Close()

		desc, err := writeContent(ctx, client.ContentStore(), mediaType, ref, fileReader)
		if err != nil {
			return err
		}
		desc.Platform = &imagespec.Platform{
			OS:           runtime.GOOS,
			Architecture: runtime.GOARCH,
		}
		index.Manifests = append(index.Manifests, desc)

		return nil
	}

	if err := writeFileToCS(metaFileName, images.MediaTypeContainerd1LoheagnRRWMetadata, imageR+"-rrw-metadata"); err != nil {
		return "", err
	}

	desc, err := writeIndex(ctx, index, client, uuid.NewString()+"index")
	if err != nil {
		return "", err
	}

	ref := getRef(imageR)
	i := images.Image{
		Name:   ref,
		Target: desc,
	}

	oldConverted, err := client.ImageService().Get(ctx, ref)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return "", err
		}
	} else {
		if err := client.ImageService().Delete(ctx, oldConverted.Name); err != nil {
			return "", err
		}
	}

	converted, err := client.ImageService().Create(ctx, i)
	if err != nil {
		return "", err
	}

	if err := client.Push(ctx, ref, converted.Target, containerd.WithPlatform(platforms.DefaultString())); err != nil {
		return "", err
	}

	return ref, nil
}

func ensureImage(originImage, convertedImage string) error {
	exist, err := checkImageExists(convertedImage)
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	if err := pullImage(originImage); err != nil {
		return err
	}

	if err := tagImage(originImage, convertedImage); err != nil {
		return err
	}

	if err := pushImage(convertedImage); err != nil {
		return err
	}

	return nil
}

func checkImageExists(imageName string) (bool, error) {
	cmd := exec.Command("nerdctl", "-n", "k8s.io", "image", "inspect", imageName)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if strings.Contains(string(output), "no such") {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func pullImage(imageName string) error {
	cmd := exec.Command("nerdctl", "-n", "k8s.io", "pull", imageName)
	return cmd.Run()
}

func tagImage(sourceImage, targetImage string) error {
	cmd := exec.Command("nerdctl", "-n", "k8s.io", "tag", sourceImage, targetImage)
	return cmd.Run()
}

func pushImage(imageName string) error {
	cmd := exec.Command("nerdctl", "-n", "k8s.io", "push", imageName)
	return cmd.Run()
}
