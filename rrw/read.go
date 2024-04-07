package rrw

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

type RangeReader interface {
	RangeRead(dest []byte, offset, length uint64) (uint64, error)
	BackgroundCopy()
}

func NewDefaultRangeReader(blobKey string, chunks []*FileChunkInfo) RangeReader {
	blockInfos := lo.Map(chunks, func(chunk *FileChunkInfo, _ int) *BlockInfo {
		return &BlockInfo{
			key:  hex.EncodeToString(chunk.Key[:]),
			size: chunk.Size,
		}
	})
	return &DefaultRangeReader{
		blockInfos: blockInfos,
	}
}

var _ RangeReader = (*DefaultRangeReader)(nil)

type BlockInfo struct {
	key  string
	size uint64
}

var (
// readFromRemoteSize = 0
// readFromLocalSize  = 0
)

var lru = NewLRUCache(20480, 5*time.Minute, nil)

func (b *BlockInfo) Read(dest []byte, offset, length uint64) (uint64, error) {
	realLen := min(length, b.size-offset)
	if realLen == 0 {
		return 0, nil
	}
	blockPath := filepath.Join(CACHE_PATH, b.key)

	tryBuf, ok := lru.Get(b.key)
	if ok {
		if buf, ok := tryBuf.([]byte); ok && buf != nil {
			cnt := copy(dest[:realLen], buf[offset:offset+realLen])
			return uint64(cnt), nil
		}
	}

	if _, err := os.Stat(blockPath); err != nil {
		remotePath := filepath.Join(NFS_BLOCK_PATH, b.key)
		buf, err := os.ReadFile(remotePath)
		if err != nil {
			return 0, err
		}
		lru.Put(b.key, buf, 5*time.Second)
		go safeWriteFile(buf, blockPath)

		cnt := copy(dest[:realLen], buf[offset:offset+realLen])
		// fmt.Println(os.Getpid(), "read from remote")
		// readFromRemoteSize += len(buf)
		return uint64(cnt), nil
	} else {
		// readFromLocalSize += int(stat.Size())
		// fmt.Println(os.Getpid(), "read from local")
	}

	// ra := float64(0)
	// if readFromLocalSize != 0 {
	// 	ra = float64(readFromRemoteSize) / float64(readFromLocalSize + readFromRemoteSize)
	// }
	// fmt.Println(os.Getpid(), "loheagn read local remote", readFromRemoteSize, readFromLocalSize, float64(readFromRemoteSize)/float64(readFromLocalSize+readFromRemoteSize))

	buf, err := os.ReadFile(blockPath)
	if err != nil {
		return 0, err
	}
	lru.Put(b.key, buf, 5*time.Second)

	cnt := copy(dest[:realLen], buf[offset:offset+realLen])
	return uint64(cnt), nil
}

func (b *BlockInfo) download() error {
	blockPath := filepath.Join(CACHE_PATH, b.key)
	if _, err := os.Stat(blockPath); err == nil {
		return nil
	}

	srcPath := filepath.Join(NFS_BLOCK_PATH, b.key)
	return copyBetweenNFS(srcPath, blockPath)
}

type DefaultRangeReader struct {
	blockInfos []*BlockInfo
}

func (r *DefaultRangeReader) BackgroundCopy() {
	// go func() {
	// 	for _, b := range r.blockInfos {
	// 		b.download()
	// 	}
	// }()
}

func (r *DefaultRangeReader) RangeRead(dest []byte, offset, length uint64) (uint64, error) {

	if len(r.blockInfos) == 0 {
		return 0, nil
	}

	readCnt := uint64(0)
	blockIDX := int(offset / BLOCK_SIZE)
	offsetInBlock := offset % BLOCK_SIZE

	for readCnt < length && blockIDX < len(r.blockInfos) {
		block := r.blockInfos[blockIDX]

		thisReadCnt, err := block.Read(dest[readCnt:], offsetInBlock, length-readCnt)
		if err != nil {
			return 0, err
		}

		readCnt += thisReadCnt
		offsetInBlock += thisReadCnt
		if offsetInBlock >= BLOCK_SIZE {
			blockIDX++
			offsetInBlock = 0
		}
	}

	return readCnt, nil
}

func copyBetweenNFS(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	dstTmp := dst + "." + uuid.NewString()
	buf, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	dir, _ := filepath.Split(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(dstTmp, buf, 0644); err != nil {
		return err
	}

	return os.Rename(dstTmp, dst)
}

func safeWriteFile(buf []byte, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	dstTmp := dst + "." + uuid.NewString()
	dir, _ := filepath.Split(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(dstTmp, buf, 0644); err != nil {
		return err
	}

	return os.Rename(dstTmp, dst)
}
