package rrw

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// HeaderToFileInfo fills a fuse.Attr struct from a tar.Header.
func HeaderToFileInfo(out *fuse.Attr, h *tar.Header) {
	out.Mode = uint32(h.Mode)
	out.Size = uint64(h.Size)
	out.Uid = uint32(h.Uid)
	out.Gid = uint32(h.Gid)
	out.SetTimes(&h.AccessTime, &h.ModTime, &h.ChangeTime)
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

	tarFile string
}

// tarRoot implements NodeOnAdder
var _ = (fs.NodeOnAdder)((*RRWRoot)(nil))

func (r *RRWRoot) OnAdd(ctx context.Context) {
	tarFile, err := os.Open(r.tarFile)
	if err != nil {
		log.Fatalf("Failed to open tar file: %v", err)
	}
	defer tarFile.Close()

	tr := tar.NewReader(tarFile)

	var longName *string
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
		HeaderToFileInfo(&attr, hdr)
		xattrs := getTarXattrs(hdr)
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			l := &fs.MemSymlink{
				Data: []byte(hdr.Linkname),
			}
			l.Attr = attr
			p.AddChild(base, r.NewPersistentInode(ctx, l, fs.StableAttr{Mode: syscall.S_IFLNK}), false)

		case tar.TypeLink:
			log.Println("don't know how to handle Typelink")

		case tar.TypeChar:
			rf := &RRWInode{}
			rf.attr = attr
			rf.xatters = xattrs
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFCHR}), false)
		case tar.TypeBlock:
			rf := &RRWInode{}
			rf.attr = attr
			rf.xatters = xattrs
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFBLK}), false)
		case tar.TypeDir:
			rf := &RRWInode{}
			rf.attr = attr
			rf.xatters = xattrs
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFDIR}), false)
		case tar.TypeFifo:
			rf := &RRWInode{}
			rf.attr = attr
			rf.xatters = xattrs
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{Mode: syscall.S_IFIFO}), false)
		case tar.TypeReg, tar.TypeRegA:
			rf := &RRWInode{}
			rf.attr = attr
			rf.attr.Size = 9
			rf.xatters = xattrs
			rf.size = 9
			p.AddChild(base, r.NewPersistentInode(ctx, rf, fs.StableAttr{}), false)
		default:
			log.Printf("entry %q: unsupported type '%c'", hdr.Name, hdr.Typeflag)
		}
	}
}

type RRWInode struct {
	fs.Inode

	size    int64
	attr    fuse.Attr
	xatters map[string]string
}

// Getxattr implements fs.NodeGetxattrer.
func (r *RRWInode) Getxattr(ctx context.Context, attr string, dest []byte) (uint32, syscall.Errno) {
	v, ok := r.xatters[attr]
	if !ok {
		return 0, syscall.Errno(fuse.ENOATTR)
	}

	return uint32(copy(dest, []byte(v))), 0
}

// Getattr implements fs.NodeGetattrer.
func (r *RRWInode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Attr = r.attr
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

var _ = (fs.NodeOpener)((*RRWInode)(nil))
var _ = (fs.NodeReader)((*RRWInode)(nil))
var _ = (fs.NodeGetattrer)((*RRWInode)(nil))
var _ = (fs.NodeGetxattrer)((*RRWInode)(nil))

func MountRRW(tarFile string, path string) error {
	rrwRoot := &RRWRoot{tarFile: tarFile}

	server, err := fs.Mount(path, rrwRoot, &fs.Options{})
	if err != nil {
		return err
	}

	go server.Wait()

	return nil
}
