package rrw

import "io"

type ReaderAtWrapper struct {
	r   io.ReaderAt
	off int64
}

func (r *ReaderAtWrapper) Read(p []byte) (n int, err error) {
	n, err = r.r.ReadAt(p, r.off)
	r.off += int64(n)
	return n, err
}
