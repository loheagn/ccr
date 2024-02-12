package rrw

type RangeReader interface {
	RangeRead(dest []byte, offset uint64) (uint64, error)
}

var registry = NewRegistry()

func NewDefaultRangeReader(blobKey string, baseOffset uint64) RangeReader {
	return &DefaultRangeReader{
		blobKey:    blobKey,
		baseOffset: baseOffset,
	}
}

var _ RangeReader = (*DefaultRangeReader)(nil)

type DefaultRangeReader struct {
	blobKey    string
	baseOffset uint64
}

func (r *DefaultRangeReader) RangeRead(dest []byte, offset uint64) (uint64, error) {
	buf, err := registry.GetBlobRange(r.blobKey, r.baseOffset+offset, uint64(len(dest)))
	if err != nil {
		return 0, err
	}

	return uint64(copy(dest, buf)), nil
}
