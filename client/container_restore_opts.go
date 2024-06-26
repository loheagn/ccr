/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package client

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/samber/lo"

	"github.com/containerd/containerd/v2/ccr"
	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/images"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/protobuf/proto"
	ptypes "github.com/containerd/containerd/v2/protobuf/types"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
)

var (
	// ErrImageNameNotFoundInIndex is returned when the image name is not found in the index
	ErrImageNameNotFoundInIndex = errors.New("image name not found in index")
	// ErrRuntimeNameNotFoundInIndex is returned when the runtime is not found in the index
	ErrRuntimeNameNotFoundInIndex = errors.New("runtime not found in index")
	// ErrSnapshotterNameNotFoundInIndex is returned when the snapshotter is not found in the index
	ErrSnapshotterNameNotFoundInIndex = errors.New("snapshotter not found in index")
)

// RestoreOpts are options to manage the restore operation
type RestoreOpts func(context.Context, string, *Client, Image, *imagespec.Index) NewContainerOpts

// WithRestoreImage restores the image for the container
func WithRestoreImage(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index, rwPath string) NewContainerOpts {

	return func(ctx context.Context, client *Client, c *containers.Container) error {
		name, ok := index.Annotations[checkpointImageNameLabel]
		if !ok || name == "" {
			return ErrImageNameNotFoundInIndex
		}
		snapshotter, ok := index.Annotations[checkpointSnapshotterNameLabel]
		if !ok || name == "" {
			return ErrSnapshotterNameNotFoundInIndex
		}
		i, err := client.GetImage(ctx, name)
		if err != nil {
			return err
		}

		diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore(), client.platform)
		if err != nil {
			return err
		}
		parent := identity.ChainID(diffIDs).String()

		snapshotInfoLabels := make(map[string]string)

		rrwMeta, ok := lo.Find(index.Manifests, func(m imagespec.Descriptor) bool {
			return m.MediaType == images.MediaTypeContainerd1LoheagnRRWMetadata
		})

		if ok {
			tarname :=
				"/var/lib/containerd/io.containerd.content.v1.content/blobs/" +
					strings.ReplaceAll(rrwMeta.Digest.String(), ":", "/")

			if err := ccr.RemoteMount(tarname, rwPath); err != nil {
				return err
			}
			snapshotInfoLabels[snapshots.LabelSnapshotExtraRWPath] = rwPath
		}

		if _, err := client.SnapshotService(snapshotter).Prepare(ctx, id, parent, snapshots.WithLabels(snapshotInfoLabels)); err != nil {
			return err
		}
		c.Image = i.Name()
		c.SnapshotKey = id
		c.Snapshotter = snapshotter
		return nil
	}
}

// WithRestoreRuntime restores the runtime for the container
func WithRestoreRuntime(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		name, ok := index.Annotations[checkpointRuntimeNameLabel]
		if !ok {
			return ErrRuntimeNameNotFoundInIndex
		}

		// restore options if present
		m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointRuntimeOptions)
		if err != nil {
			if err != ErrMediaTypeNotFound {
				return err
			}
		}
		var options ptypes.Any
		if m != nil {
			store := client.ContentStore()
			data, err := content.ReadBlob(ctx, store, *m)
			if err != nil {
				return fmt.Errorf("unable to read checkpoint runtime: %w", err)
			}
			if err := proto.Unmarshal(data, &options); err != nil {
				return err
			}
		}

		c.Runtime = containers.RuntimeInfo{
			Name:    name,
			Options: &options,
		}
		return nil
	}
}

// WithRestoreSpec restores the spec from the checkpoint for the container
func WithRestoreSpec(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointConfig)
		if err != nil {
			return err
		}
		store := client.ContentStore()
		data, err := content.ReadBlob(ctx, store, *m)
		if err != nil {
			return fmt.Errorf("unable to read checkpoint config: %w", err)
		}

		var any ptypes.Any
		if err := proto.Unmarshal(data, &any); err != nil {
			return err
		}

		ns, _ := namespaces.NamespaceRequired(ctx)
		spec := specs.Spec{}
		if err := typeurl.UnmarshalTo(&any, &spec); err != nil {
			fmt.Println(err.Error())
			return err
		}
		spec.Linux.CgroupsPath = filepath.Join("/", ns, id)

		c.Spec, _ = typeurl.MarshalAny(&spec)
		return nil
	}
}

// WithRestoreRW restores the rw layer from the checkpoint for the container
func WithRestoreRW(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		// apply rw layer
		rw, err := GetIndexByMediaType(index, imagespec.MediaTypeImageLayerGzip)
		if err != nil {
			return err
		}
		mounts, err := client.SnapshotService(c.Snapshotter).Mounts(ctx, c.SnapshotKey)
		if err != nil {
			return err
		}

		if _, err := client.DiffService().Apply(ctx, *rw, mounts); err != nil {
			return err
		}
		return nil
	}
}
