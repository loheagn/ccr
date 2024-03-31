package overlaynfs

import "testing"

func Test_mount(t *testing.T) {
	sbID := "cp-mysql-nfs-id-3"
	containerID := "container-test"
	_, err := Mount(sbID, containerID)
	done := make(chan bool)
	<- done
	if err != nil {
		t.Fatal(err)
	}
}
