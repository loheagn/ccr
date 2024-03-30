package rrw

import (
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

type RangeReader interface {
	RangeRead(dest []byte, offset, length uint64) (uint64, error)
	BackgroundCopy()
}

var registry = NewRegistry()

func NewDefaultRangeReader(blobKey string, chunks []*FileChunkInfo) RangeReader {
	blockInfos := lo.Map(chunks, func(chunk *FileChunkInfo, _ int) *BlockInfo {
		return &BlockInfo{
			key:          chunk.Key,
			blobKey:      blobKey,
			offsetInBlob: chunk.Offset,
		}
	})
	return &DefaultRangeReader{
		blockInfos: blockInfos,
	}
}

var _ RangeReader = (*DefaultRangeReader)(nil)

type BlockInfo struct {
	key          string
	blobKey      string
	offsetInBlob uint64
}

func (b *BlockInfo) Read(dest []byte, offset, length uint64) (uint64, error) {
	realLen := min(length, BLOCK_SIZE-offset)
	if realLen == 0 {
		return 0, nil
	}
	blockPath := fmt.Sprintf("%s/%s", CACHE_PATH, b.key)
	if err := b.download(); err != nil {
		return 0, err
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
	blockPath := fmt.Sprintf("%s/%s", CACHE_PATH, b.key)
	if _, err := os.Stat(blockPath); err == nil {
		return nil
	}

	tmpID := uuid.NewString()
	blockPathTmp := fmt.Sprintf("%s/%s.%s", CACHE_PATH, b.key, tmpID)
	buf, err := registry.GetBlobRange(b.blobKey, b.offsetInBlob, BLOCK_SIZE)
	if err != nil {
		return err
	}
	fileBuf := make([]byte, BLOCK_SIZE)
	copy(fileBuf, buf)
	if err := os.MkdirAll(CACHE_PATH, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(blockPathTmp, fileBuf, 0644); err != nil {
		return err
	}

	return os.Rename(blockPathTmp, blockPath)
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
