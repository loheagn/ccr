package rrw

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// headerToFileInfo fills a fuse.Attr struct from a tar.Header.
func headerToFileInfo(out *fuse.Attr, h *tar.Header) {
	out.Mode = uint32(h.Mode)
	out.Size = uint64(h.Size)
	out.Uid = uint32(h.Uid)
	out.Gid = uint32(h.Gid)
	out.SetTimes(&h.AccessTime, &h.ModTime, &h.ChangeTime)
}

type RRWInode struct {
	fs.Inode

	reader RangeReader

	Attr   fuse.Attr
	Xattrs map[string]string
}

var _ = (fs.NodeOpener)((*RRWInode)(nil))
var _ = (fs.NodeReader)((*RRWInode)(nil))
var _ = (fs.NodeGetattrer)((*RRWInode)(nil))
var _ = (fs.NodeGetxattrer)((*RRWInode)(nil))

// Getxattr implements fs.NodeGetxattrer.
func (r *RRWInode) Getxattr(ctx context.Context, attr string, dest []byte) (uint32, syscall.Errno) {
	v, ok := r.Xattrs[attr]
	if !ok {
		return 0, syscall.Errno(fuse.ENOATTR)
	}

	return uint32(copy(dest, []byte(v))), 0
}

// Getattr implements fs.NodeGetattrer.
func (r *RRWInode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Attr = r.Attr
	return 0
}

// Read implements fs.NodeReader.
func (r *RRWInode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	_, err := r.reader.RangeRead(dest, uint64(off))
	if err != nil {
		return nil, syscall.Errno(fuse.EREMOTEIO)
	}
	return fuse.ReadResultData(dest), 0
}

// Open implements fs.NodeOpener.
func (*RRWInode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

type FileInfo struct {
	Size   uint64
	Offset uint64
}

var (
	checkpointBasePath = os.Getenv("CCR_CHECKPOINT_RW_PATH")
)

func SplitTar(ctx context.Context, tarFileName string) (metaFileName, blobFileName string, err error) {
	tarFile, err := os.Open(tarFileName)
	if err != nil {
		return "", "", err
	}
	defer tarFile.Close()

	metaWriter, err := os.CreateTemp(checkpointBasePath, "meta-*")
	if err != nil {
		return "", "", err
	}
	defer metaWriter.Close()

	blobWriter, err := os.CreateTemp(checkpointBasePath, "blob-*")
	if err != nil {
		return "", "", err
	}
	defer blobWriter.Close()

	tr := tar.NewReader(tarFile)

	metaTW := tar.NewWriter(metaWriter)

	offset := uint64(0)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("add: %w", err)
		}

		if tar.TypeReg == hdr.Typeflag || tar.TypeRegA == hdr.Typeflag {
			fileInfo := &FileInfo{
				Size:   uint64(hdr.Size),
				Offset: offset,
			}
			offset += fileInfo.Size
			fileInfoData, err := json.Marshal(fileInfo)
			if err != nil {
				return "", "", fmt.Errorf("json marshal: %w", err)
			}

			// generate new tar header
			newHDr := *hdr
			newHDr.Size = int64(len(fileInfoData))

			if err := metaTW.WriteHeader(&newHDr); err != nil {
				return "", "", err
			}
			if _, err := metaTW.Write(fileInfoData); err != nil {
				return "", "", err
			}

			if _, err := io.CopyN(blobWriter, tr, int64(hdr.Size)); err != nil {
				return "", "", err
			}
		} else {
			if err := metaTW.WriteHeader(hdr); err != nil {
				return "", "", err
			}
			if hdr.Size != 0 {
				io.CopyN(metaWriter, tr, int64(hdr.Size))
			}
		}

	}

	return metaWriter.Name(), blobWriter.Name(), metaTW.Close()
}
