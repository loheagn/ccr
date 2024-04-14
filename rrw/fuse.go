package rrw

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const (
	BLOCK_SIZE           = 4096
	SMALL_FILE_TYPE byte = 'o'
	CACHE_PATH           = "/var/rrw/blocks"
	// NFS_BLOCK_PATH          = "/mnt/nfs_client/nfs_block/"
	NFS_BLOCK_PATH = "/root/tarball/nfs_blocks/"

	KERNEL_MOUNT_TAR_PATH = "/var/rrw/metatars"

	KERNEL_IMG_PATH = "/var/rrw/imagepath"
)

func init() {
	os.MkdirAll(KERNEL_MOUNT_TAR_PATH, 0755)
	os.MkdirAll(KERNEL_IMG_PATH, 0755)
}

func getTarXattrs(h *tar.Header) map[string]string {
	re := h.Xattrs
	if re == nil {
		re = make(map[string]string)
	}

	for k, v := range h.PAXRecords {
		if strings.HasPrefix(k, "SCHILY.xattr.") {
			re[strings.TrimPrefix(k, "SCHILY.xattr.")] = v
		}
	}

	return re
}

type RRWRoot struct {
	fs.Inode

	tr *tar.Reader

	blobDigest string
}

// tarRoot implements NodeOnAdder
var _ = (fs.NodeOnAdder)((*RRWRoot)(nil))

func (r *RRWRoot) OnAdd(ctx context.Context) {
	tr := r.tr

	var longName *string

	hardLinkMap := map[string]string{}
	pNodeMap := map[string]*fs.Inode{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			log.Printf("Add: %v", err)
			// XXX handle error
			break
		}
		if hdr.Typeflag == 'L' {
			buf := bytes.NewBuffer(make([]byte, 0, hdr.Size))
			io.Copy(buf, tr)
			s := buf.String()
			longName = &s
			continue
		}

		if longName != nil {
			hdr.Name = *longName
			longName = nil
		}

		buf := bytes.NewBuffer(make([]byte, 0, hdr.Size))
		io.Copy(buf, tr)
		dir, base := filepath.Split(filepath.Clean(hdr.Name))

		p := r.EmbeddedInode()
		for _, comp := range strings.Split(dir, "/") {
			if len(comp) == 0 {
				continue
			}
			ch := p.GetChild(comp)
			if ch == nil {
				ch = p.NewPersistentInode(ctx,
					&fs.Inode{},
					fs.StableAttr{Mode: syscall.S_IFDIR})
				p.AddChild(comp, ch, false)
			}
			p = ch
		}

		var attr fuse.Attr
		headerToFileInfo(&attr, hdr)
		xattrs := getTarXattrs(hdr)
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			l := &fs.MemSymlink{
				Data: []byte(hdr.Linkname),
			}
			l.Attr = attr
			p.AddChild(base, r.NewPersistentInode(ctx, l, fs.StableAttr{Mode: syscall.S_IFLNK}), false)

		case tar.TypeLink:
			hardLinkMap[hdr.Name] = hdr.Linkname
			pNodeMap[hdr.Name] = p

		case tar.TypeChar:
			rf := &RRWInode{}
			rf.Attr = attr
			rf.Xattrs = xattrs
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFCHR}), false)
		case tar.TypeBlock:
			rf := &RRWInode{}
			rf.Attr = attr
			rf.Xattrs = xattrs
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFBLK}), false)
		case tar.TypeDir:
			rf := &RRWInode{}
			rf.Attr = attr
			rf.Xattrs = xattrs
			p.AddChild(base, r.NewInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFDIR}), false)
		case tar.TypeFifo:
			rf := &RRWInode{}
			rf.Attr = attr
			rf.Xattrs = xattrs
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFIFO}), false)
		case tar.TypeReg, tar.TypeRegA:
			rf := &RRWInode{}

			fileInfo, err := NewFileInfo(buf.Bytes())
			if err != nil {
				log.Printf("failed to unmarshal file info: %s", err.Error())
				continue
			}

			rf.reader = NewDefaultRangeReader(r.blobDigest, fileInfo.Chunks)
			rf.Attr = attr
			rf.Attr.Size = fileInfo.Size
			rf.Xattrs = xattrs
			rf.name = hdr.Name
			p.AddChild(base, r.NewInode(ctx, rf, fs.StableAttr{}), false)
			pNodeMap[hdr.Name] = p
			rf.reader.BackgroundCopy()

		case SMALL_FILE_TYPE:
			rf := &RRWInode{}
			bs := buf.Bytes()
			fileBuf := make([]byte, len(bs))
			copy(fileBuf, bs)
			rf.buf = fileBuf
			rf.Attr = attr
			rf.Xattrs = xattrs
			rf.name = hdr.Name
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{}), false)
			pNodeMap[hdr.Name] = p

		default:
			log.Printf("entry %q: unsupported type '%c'", hdr.Name, hdr.Typeflag)
		}
	}

	for targetPath, path := range hardLinkMap {
		targetDirNode := pNodeMap[targetPath]
		pathDirNode := pNodeMap[path]
		if targetDirNode == nil || pathDirNode == nil {
			continue
		}
		baseName := filepath.Base(path)
		inode := pathDirNode.GetChild(baseName)
		targetDirNode.AddChild(filepath.Base(targetPath), inode, false)
	}
}

func MountRRW(metaReader io.ReaderAt, blobDigest, path string) error {
	rrwRoot := &RRWRoot{
		tr:         tar.NewReader(&ReaderAtWrapper{r: metaReader}),
		blobDigest: blobDigest,
	}

	timeout := 60 * time.Minute
	server, err := fs.Mount(path, rrwRoot, &fs.Options{
		EntryTimeout:    &timeout,
		AttrTimeout:     &timeout,
		NegativeTimeout: &timeout,
	})
	if err != nil {
		return err
	}

	go server.Wait()

	return nil
}

func MountRRWV2(metaReader io.Reader, blobDigest, path string) error {
	rrwRoot := &RRWRoot{
		tr:         tar.NewReader(metaReader),
		blobDigest: blobDigest,
	}
	timeout := 60 * time.Minute
	server, err := fs.Mount(path, rrwRoot, &fs.Options{
		EntryTimeout:    &timeout,
		AttrTimeout:     &timeout,
		NegativeTimeout: &timeout,
	})
	if err != nil {
		return err
	}

	server.Wait()

	return nil
}

func fixTar(filePath string) error {
	// 打开文件
	file, err := os.OpenFile(filePath, os.O_RDWR, 0666)
	if err != nil {
		return fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()

	// 获取文件状态信息
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("error getting file info: %w", err)
	}

	// 计算需要补充的0的数量
	fileSize := fileInfo.Size()
	remainder := fileSize % 4096
	var paddingSize int64 = 0
	if remainder != 0 {
		paddingSize = 4096 - remainder
	}

	// 创建一个大小等于需要补0的切片
	padding := make([]byte, paddingSize)

	// 将切片写入文件末尾
	_, err = file.WriteAt(padding, fileSize)
	if err != nil {
		return fmt.Errorf("error writing to file: %w", err)
	}

	return nil
}

func createTmpTAR(reader io.Reader) (string, error) {
	// 创建临时文件
	tempFile, err := os.CreateTemp(KERNEL_MOUNT_TAR_PATH, "")
	if err != nil {
		return "", fmt.Errorf("failed to create tmp tar for kernel mount: %w", err)
	}
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, reader); err != nil {
		return "", fmt.Errorf("failed to write tmp tar for kernel mount: %w", err)
	}

	return tempFile.Name(), nil
}

func KernelMountV2(imageFilename, path string) error {
	mountCmd := fmt.Sprintf("mount -o loop -t simplefs %s %s", imageFilename, path)
	return exec.Command("bash", "-c", mountCmd).Run()
}

func KernelMount(metaReader io.ReaderAt, blobDigest, path string) error {
	reader := &ReaderAtWrapper{r: metaReader}
	tarpath, err := createTmpTAR(reader)
	if err != nil {
		return err
	}
	if err := fixTar(tarpath); err != nil {
		return err
	}

	mountCmd := fmt.Sprintf("mount -o loop -t tarfs %s %s", tarpath, path)
	if err := exec.Command("bash", "-c", mountCmd).Run(); err != nil {
		return err
	}

	return nil
}
