package main

import (
	"fmt"
	"os"

	"github.com/containerd/containerd/v2/rrw"
)

func main() {
	file := os.Args[1]
	path := os.Args[2]

	fmt.Println("rrw mount", file, path)

	r, err := os.Open(file)
	if err != nil {
		panic(err)
	}

	rrw.MountRRWV2(r, "", path)
}
