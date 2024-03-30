package overlaynfs

import "testing"

func Test_mount(t *testing.T) {
	sbID := "container"
	containerID := "container"
	_, err := Mount(sbID, containerID)
	if err != nil {
		t.Fatal(err)
	}
}
