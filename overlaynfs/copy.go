package overlaynfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// copyFile copies a file from src to dst and preserves its file mode and timestamps.
func copyFile(src, originDST string) error {
	dst := originDST + ".loheagn.tmp"

	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}

	// Set the permissions
	if err := os.Chmod(dst, sourceFileStat.Mode()); err != nil {
		return err
	}

	// Get the source file's access and modification times
	atime := sourceFileStat.Sys().(*syscall.Stat_t).Atim
	mtime := sourceFileStat.Sys().(*syscall.Stat_t).Mtim

	// Set the destination file's access and modification times
	if err := os.Chtimes(dst, time.Unix(atime.Sec, atime.Nsec), time.Unix(mtime.Sec, mtime.Nsec)); err != nil {
		return err
	}

	return os.Rename(dst, originDST)
}

// copyDirRecursively copies the contents of the src directory to the dst directory.
func copyDirRecursively(src, dst string, fileChan chan string) error {
	fileMap := map[string]bool{}
	go func() {
		for filename := range fileChan {
			fileMap[filename] = true
		}
	}()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the source directory itself
		if path == src {
			return nil
		}

		// Calculate the relative path to maintain the directory structure
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if fileMap[relPath] {
			return nil
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			// Create the directory
			return os.MkdirAll(dstPath, info.Mode())
		} else {
			// Copy the file
			return copyFile(path, dstPath)
		}
	})
}
