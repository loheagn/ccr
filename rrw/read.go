package rrw

import (
	"io"
	"os"
	"path/filepath"

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
			key:  chunk.Key,
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

func (b *BlockInfo) Read(dest []byte, offset, length uint64) (uint64, error) {
	realLen := min(length, b.size-offset)
	if realLen == 0 {
		return 0, nil
	}
	blockPath := filepath.Join(CACHE_PATH, b.key)

	if _, err := os.Stat(blockPath); err != nil {
		remotePath := filepath.Join(NFS_BLOCK_PATH, b.key)
		buf, err := os.ReadFile(remotePath)
		if err != nil {
			return 0, err
		}
		go safeWriteFile(buf, blockPath)

		cnt := copy(dest[:realLen], buf[offset:offset+realLen])
		return uint64(cnt), nil
	}

	blockFile, err := os.Open(blockPath)
	if err != nil {
		return 0, err
	}

	ret, err := blockFile.Seek(int64(offset), 0)
	if ret != int64(offset) || err != nil {
		return 0, err
	}

	readCnt, err := io.ReadFull(blockFile, dest[:realLen])
	if err != nil && err != io.EOF {
		return 0, err
	}

	return uint64(readCnt), nil
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
	go func() {
		for _, b := range r.blockInfos {
			b.download()
		}
	}()
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
