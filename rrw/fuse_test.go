package rrw

import (
	"os"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
)

func TestAll(_ *testing.T) {
	tarFile := os.Args[1]
	path := os.Args[2]

	rrwRoot := &RRWRoot{tarFile: tarFile}

	server, err := fs.Mount(path, rrwRoot, &fs.Options{})
	if err != nil {
		panic(err)
	}

	server.Wait()
}
