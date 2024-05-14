package rrw

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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

	buf []byte

	name string

	Attr   fuse.Attr
	Xattrs map[string]string

	lock sync.Mutex
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
	// timeStr := time.Now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	// fmt.Println(timeStr, "loheagnttt", r.name, off, len(dest))

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

	// go r.preFetch()

	length := min(uint64(len(dest)), r.Attr.Size-uint64(off))
	_, err := r.reader.RangeRead(dest, uint64(off), length)
	if err != nil {
		return nil, syscall.Errno(fuse.EREMOTEIO)
	}
	return fuse.ReadResultData(dest), 0
}

func (r *RRWInode) preFetch() {
	if !r.lock.TryLock() {
		return
	}
	defer r.lock.Unlock()

	r.reader.BackgroundCopy()
}

// Open implements fs.NodeOpener.
func (r *RRWInode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	// timeStr := time.Now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	// fmt.Println(timeStr, "loheagnttt", r.name)
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

type FileInfo struct {
	Size   uint64
	Chunks []*FileChunkInfo
}

func NewFileInfo(data []byte) (*FileInfo, error) {
	buf := bytes.NewBuffer(data[:8])

	size := uint64(0)
	err := binary.Read(buf, binary.BigEndian, &size)
	if err != nil {
		return nil, err
	}

	chunks := make([]*FileChunkInfo, 0)
	for i := 8; i < len(data); i += 40 {
		hash := [32]byte{}
		copy(hash[:], data[i:i+32])

		buf := bytes.NewBuffer(data[i+32 : i+40])

		size := uint64(0)
		err := binary.Read(buf, binary.BigEndian, &size)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, &FileChunkInfo{hash, size})
	}

	return &FileInfo{
		Size:   size,
		Chunks: chunks,
	}, nil

}

func (f *FileInfo) ToBytes() ([]byte, error) {
	buf := new(bytes.Buffer)

	err := binary.Write(buf, binary.BigEndian, f.Size)
	if err != nil {
		return nil, err
	}
	for _, chunk := range f.Chunks {
		_, err := buf.Write(chunk.Key[:])
		if err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, chunk.Size); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

type FileChunkInfo struct {
	Key  [32]byte
	Size uint64
}

var (
	checkpointBasePath = os.Getenv("CCR_CHECKPOINT_RW_PATH")
)

func SplitTar(ctx context.Context, tarFileName string) (metaFileName string, err error) {
	tarFile, err := os.Open(tarFileName)
	if err != nil {
		return "", err
	}
	defer tarFile.Close()

	metaWriter, err := os.CreateTemp(checkpointBasePath, "meta-*")
	if err != nil {
		return "", err
	}
	defer metaWriter.Close()

	tr := tar.NewReader(tarFile)

	metaTW := tar.NewWriter(metaWriter)

	wg := &sync.WaitGroup{}

	concurrencyLimit := make(chan struct{}, 20)

	offset := uint64(0)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			return "", fmt.Errorf("add: %w", err)
		}

		if hdr.Size < BLOCK_SIZE || !(tar.TypeReg == hdr.Typeflag || tar.TypeRegA == hdr.Typeflag) {
			if tar.TypeReg == hdr.Typeflag || tar.TypeRegA == hdr.Typeflag {
				hdr.Typeflag = SMALL_FILE_TYPE
			}
			if err := metaTW.WriteHeader(hdr); err != nil {
				return "", err
			}
			if hdr.Size != 0 {
				n, err := io.CopyN(metaTW, tr, int64(hdr.Size))
				if err != nil {
					return "", fmt.Errorf("copy %d bytes to metaTW: %w", n, err)
				}
			}
			continue
		}

		chunks, err := writeByChunks(tr, offset, int64(hdr.Size), wg, concurrencyLimit)
		if err != nil {
			return "", err
		}
		fileInfo := &FileInfo{
			Size:   uint64(hdr.Size),
			Chunks: chunks,
		}
		offset += uint64(len(chunks)) * BLOCK_SIZE
		fileInfoData, err := fileInfo.ToBytes()
		if err != nil {
			return "", fmt.Errorf("write fileInfo to bytes: %w", err)
		}

		// generate new tar header
		newHDr := *hdr
		newHDr.Size = int64(len(fileInfoData))

		if err := metaTW.WriteHeader(&newHDr); err != nil {
			return "", err
		}
		if _, err := metaTW.Write(fileInfoData); err != nil {
			return "", err
		}
	}

	wg.Wait()

	return metaWriter.Name(), metaTW.Close()
}

func writeByChunks(r io.Reader, offset uint64, size int64, wg *sync.WaitGroup, concurrencyLimit chan struct{}) ([]*FileChunkInfo, error) {
	chunkList := make([]*FileChunkInfo, 0)
	// buf := make([]byte, BLOCK_SIZE) // 创建一个4KB的缓冲区
	readAndWrite := func(maxSize int) error {
		buf := make([]byte, BLOCK_SIZE)         // 创建一个4KB的缓冲区
		n, err := io.ReadFull(r, buf[:maxSize]) // 从reader读取数据到缓冲区
		if err != nil {
			return err
		}

		newBuf := buf[:n]

		hash := sha256.Sum256(newBuf)
		key := hex.EncodeToString(hash[:])

		// if err := safeWriteFile(newBuf, filepath.Join(NFS_BLOCK_PATH, key)); err != nil {
		// 	return err
		// }

		chunk := &FileChunkInfo{
			Key:  hash,
			Size: uint64(n),
		}
		chunkList = append(chunkList, chunk)

		wg.Add(1)
		concurrencyLimit <- struct{}{}
		go func(buf []byte, key string) {
			defer wg.Done()
			defer func() {
				<-concurrencyLimit
			}()
			safeWriteFile(buf, filepath.Join(NFS_BLOCK_PATH, key))
		}(buf, key)

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
