package rrw

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"log"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

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
}

// tarRoot implements NodeOnAdder
var _ = (fs.NodeOnAdder)((*RRWRoot)(nil))

func (r *RRWRoot) OnAdd(ctx context.Context) {
	tr := r.tr

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
			log.Println("don't know how to handle Typelink")

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
			rf.Attr = attr
			rf.Attr.Size = 9
			rf.Xattrs = xattrs
			rf.Size = 9
			p.AddChild(base, r.NewInode(ctx, rf, fs.StableAttr{}), false)
		default:
			log.Printf("entry %q: unsupported type '%c'", hdr.Name, hdr.Typeflag)
		}
	}
}

func MountRRW(reader io.ReaderAt, path string) error {
	rrwRoot := &RRWRoot{tr: tar.NewReader(&ReaderAtWrapper{r: reader})}

	server, err := fs.Mount(path, rrwRoot, &fs.Options{})
	if err != nil {
		return err
	}

	go server.Wait()

	return nil
}
