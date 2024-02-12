package rrw

import (
	"fmt"
	"testing"
)

func TestRegistry_GetBlobRange(t *testing.T) {
	registry := NewRegistry()
	blob, err := registry.GetBlobRange("sha256:5aa5901ac9c75b399796aacfd0113e2f04c0d58b87ba5057da539d5db91eb52b", 11, 100)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(blob))
}
