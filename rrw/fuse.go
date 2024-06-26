package rrw

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
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
	NFS_BLOCK_PATH       = "/mnt/nfs_client/nfs_block/"
	MetricFile           = "/root/rrw-metric"
)

func RecordTime(value interface{}) {
	var v interface{} = value
	if v == nil {
		v = time.Now().UnixMilli()
	}

	file, err := os.OpenFile(MetricFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return
	}
	defer file.Close()

	file.WriteString(fmt.Sprintf(", %v", v))
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
	startTime := time.Now()
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

	endTime := time.Now()

	fmt.Println("loheagn read tar usage: ", endTime.Sub(startTime).Milliseconds())
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

func RemoteMount(tarName, path string) (*fuse.Server, error) {
	file, err := os.Open(tarName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	rrwRoot := &RRWRoot{
		tr:         tar.NewReader(&ReaderAtWrapper{r: file}),
		blobDigest: "",
	}

	timeout := 60 * time.Minute
	server, err := fs.Mount(path, rrwRoot, &fs.Options{
		EntryTimeout:    &timeout,
		AttrTimeout:     &timeout,
		NegativeTimeout: &timeout,
	})
	if err != nil {
		return nil, err
	}

	go server.Wait()

	return server, nil
}
