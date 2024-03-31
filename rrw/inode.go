package rrw

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

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

	buf []byte

	name string

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
	timeStr := time.Now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	fmt.Println(timeStr, "loheagnttt", r.name, off, len(dest))

	if len(r.buf) > 0 {
		end := int(off) + len(dest)
		if end > len(r.buf) {
			end = len(r.buf)
		}
		return fuse.ReadResultData(r.buf[off:end]), 0
	}

	if r.Attr.Size == 0 {
		return fuse.ReadResultData(r.buf), 0
	}

	length := min(uint64(len(dest)), r.Attr.Size-uint64(off))
	_, err := r.reader.RangeRead(dest, uint64(off), length)
	if err != nil {
		return nil, syscall.Errno(fuse.EREMOTEIO)
	}
	return fuse.ReadResultData(dest), 0
}

// Open implements fs.NodeOpener.
func (r *RRWInode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	timeStr := time.Now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	fmt.Println(timeStr, "loheagnttt", r.name)
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

type FileInfo struct {
	Size   uint64
	Chunks []*FileChunkInfo
}

type FileChunkInfo struct {
	Key    string
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

		if hdr.Size < BLOCK_SIZE || !(tar.TypeReg == hdr.Typeflag || tar.TypeRegA == hdr.Typeflag) {
			if tar.TypeReg == hdr.Typeflag || tar.TypeRegA == hdr.Typeflag {
				hdr.Typeflag = SMALL_FILE_TYPE
			}
			if err := metaTW.WriteHeader(hdr); err != nil {
				return "", "", err
			}
			if hdr.Size != 0 {
				n, err := io.CopyN(metaTW, tr, int64(hdr.Size))
				if err != nil {
					return "", "", fmt.Errorf("copy %d bytes to metaTW: %w", n, err)
				}
			}
			continue
		}

		chunks, err := writeByChunks(blobWriter, tr, offset, int64(hdr.Size))
		if err != nil {
			return "", "", err
		}
		fileInfo := &FileInfo{
			Size:   uint64(hdr.Size),
			Chunks: chunks,
		}
		offset += uint64(len(chunks)) * BLOCK_SIZE
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
	}

	return metaWriter.Name(), blobWriter.Name(), metaTW.Close()
}

func writeByChunks(w io.Writer, r io.Reader, offset uint64, size int64) ([]*FileChunkInfo, error) {
	chunkList := make([]*FileChunkInfo, 0)
	buf := make([]byte, BLOCK_SIZE) // 创建一个4KB的缓冲区
	readAndWrite := func(maxSize int) error {
		n, err := io.ReadFull(r, buf[:maxSize]) // 从reader读取数据到缓冲区
		if err != nil {
			return err
		}

		if n < BLOCK_SIZE {
			// 如果读取的数据少于4KB，使用0填充剩余的部分
			for i := n; i < len(buf); i++ {
				buf[i] = 0
			}
		}

		hash := sha256.Sum256(buf)
		key := hex.EncodeToString(hash[:])

		_, err = w.Write(buf)
		if err != nil {
			return err
		}

		chunk := &FileChunkInfo{
			Key:    key,
			Offset: offset,
		}
		chunkList = append(chunkList, chunk)

		offset += BLOCK_SIZE

		return nil
	}

	chunkCnt := int(size / BLOCK_SIZE)
	for i := 0; i < chunkCnt; i++ {
		err := readAndWrite(BLOCK_SIZE)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	last := size % BLOCK_SIZE
	if last == 0 {
		return chunkList, nil
	}

	err := readAndWrite(int(last))
	if err != nil && err != io.EOF {
		return nil, err
	}

	return chunkList, nil
}
