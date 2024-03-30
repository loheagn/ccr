package overlaynfs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

const BaseDir = "/var/overlaynfs"

func GetNFSDir(sbID string) (string, error) {
	dir := filepath.Join("/mnt/nfs_client/", sbID)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return "", err
	}
	return dir, nil
}

func backgroundCopy(nfsDir, lazyStoreDir, activeDir string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	done := make(chan bool)
	defer close(done)

	fileChan := make(chan string, 1024)
	defer close(fileChan)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					relFileName, err := filepath.Rel(activeDir, event.Name)
					if err != nil {
						log.Println("error:", err)
					}
					fileChan <- relFileName
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			case <-done:
				return
			}
		}
	}()

	err = watcher.Add(activeDir)
	if err != nil {
		fmt.Println(err.Error())
	}

	err = copyDirRecursively(nfsDir, lazyStoreDir, fileChan)
	if err != nil {
		fmt.Println(err.Error())
	}
	done <- true
}

func Mount(sbID, contaienrID string) (string, error) {
	baseDir := filepath.Join(BaseDir, contaienrID)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", err
	}
	nfsDir, err := GetNFSDir(sbID)
	if err != nil {
		return "", err
	}

	lazyStoreDir := filepath.Join(baseDir, "lazy-store")
	if err := os.MkdirAll(lazyStoreDir, 0755); err != nil {
		return "", err
	}

	activeDir := filepath.Join(baseDir, "active")
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		return "", err
	}
	activeWorkDir := filepath.Join(baseDir, "active-work")
	if err := os.MkdirAll(activeWorkDir, 0755); err != nil {
		return "", err
	}

	opts := fmt.Sprintf("lowerdir=%s:%s,upperdir=%s,workdir=%s", lazyStoreDir, nfsDir, activeDir, activeWorkDir)

	mountPoint := filepath.Join(baseDir, "mount")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return "", err
	}

	err = syscall.Mount("overlay", mountPoint, "overlay", 0, opts)
	if err != nil {
		return "", fmt.Errorf("failed to mount overlay: %w", err)
	}

	go backgroundCopy(nfsDir, lazyStoreDir, activeDir)

	return mountPoint, nil
}
