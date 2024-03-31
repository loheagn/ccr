package overlaynfs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

const BaseDir = "/var/overlaynfs"

var usedMap = map[string]bool{}

func GetNFSDir(sbID string, create bool) (string, error) {
	dir := filepath.Join("/mnt/nfs_client/", sbID)
	if !create {
		return dir, nil
	}
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

func Mount(sbID, contaienrID string) ([]string, error) {
	baseDir := filepath.Join(BaseDir, contaienrID)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}
	nfsDir, err := GetNFSDir(sbID, true)
	if err != nil {
		return nil, err
	}

	lazyStoreDir := filepath.Join(baseDir, "lazy-store")
	if err := os.MkdirAll(lazyStoreDir, 0755); err != nil {
		return nil, err
	}

	activeDir := filepath.Join(baseDir, "active")
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		return nil, err
	}
	activeWorkDir := filepath.Join(baseDir, "active-work")
	if err := os.MkdirAll(activeWorkDir, 0755); err != nil {
		return nil, err
	}

	if id := sbID + "," + contaienrID; !usedMap[id] {
		usedMap[id] = true
		// go backgroundCopy(nfsDir, lazyStoreDir, activeDir)
	}

	return []string{
		fmt.Sprintf("lowerdir=%s:%s", lazyStoreDir, nfsDir),
		fmt.Sprintf("upperdir=%s", activeDir),
		fmt.Sprintf("workdir=%s", activeWorkDir),
	}, nil
}
