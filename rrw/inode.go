package rrw

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

	TarType byte

	Size   uint64
	Offset uint64

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
func (*RRWInode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	return fuse.ReadResultData([]byte("hellohelloh")), 0
}

// Open implements fs.NodeOpener.
func (*RRWInode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

type InodeType string

const (
	InodeTypeRRW        = "rrw"
	InodeTypeMemSymlink = "symlink"
)

type RRWInodeType interface {
	*fs.MemSymlink | *RRWInode
}

type RRWMeta struct {
	InodeList []InodeWrapper
}

type InodeWrapper struct {
	InodeType  InodeType
	BaseName   string
	HeaderName string
	Data       []byte
}

func wrapInode[T RRWInodeType](inode T, inodeType InodeType) *InodeWrapper {
	data, _ := json.Marshal(inode)
	return &InodeWrapper{
		InodeType: inodeType,
		Data:      data,
	}
}

func TarToRRWLayers(ctx context.Context, tarFileName string) (*RRWMeta, io.Reader, error) {
	tarFile, err := os.Open(tarFileName)
	if err != nil {
		log.Fatalf("Failed to open tar file: %v", err)
	}
	defer tarFile.Close()

	tr := tar.NewReader(tarFile)

	wrapperList := make([]InodeWrapper, 0)
	blob := make([]byte, 0)
	blobReadWriter := bytes.NewBuffer(blob)

	offset := uint64(0)

	var longName *string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("add: %w", err)
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
		_, base := filepath.Split(filepath.Clean(hdr.Name))

		// we don't need to record the dir

		var attr fuse.Attr
		headerToFileInfo(&attr, hdr)
		xattrs := getTarXattrs(hdr)

		var inode *InodeWrapper

		switch hdr.Typeflag {
		case tar.TypeSymlink:
			l := &fs.MemSymlink{
				Data: []byte(hdr.Linkname),
			}
			l.Attr = attr
			inode = wrapInode(l, InodeTypeMemSymlink)

		case tar.TypeLink:
			log.Println("don't know how to handle Typelink")

		case tar.TypeChar, tar.TypeBlock, tar.TypeDir, tar.TypeFifo:
			rf := &RRWInode{}
			rf.Attr = attr
			rf.Xattrs = xattrs
			inode = wrapInode(rf, InodeTypeRRW)

		case tar.TypeReg, tar.TypeRegA:
			rf := &RRWInode{}
			rf.Attr = attr
			rf.Xattrs = xattrs

			rf.Size = rf.Attr.Size
			rf.Offset = offset
			offset += rf.Attr.Size
			blobReadWriter.Write(buf.Bytes())

			inode = wrapInode(rf, InodeTypeRRW)
		default:
			log.Printf("entry %q: unsupported type '%c'", hdr.Name, hdr.Typeflag)
		}

		if inode != nil {
			inode.BaseName = base
			inode.HeaderName = hdr.Name
			wrapperList = append(wrapperList, *inode)
		}

	}

	meta := &RRWMeta{
		InodeList: wrapperList,
	}

	return meta, blobReadWriter, nil
}
