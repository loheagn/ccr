package overlaynfs

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// copyFile copies the contents of the file and attempts to preserve the file mode, owner, and timestamps.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	sourceFileInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}

	destFile, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, sourceFileInfo.Mode())
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Preserve the timestamps.
	times := []syscall.Timespec{syscall.NsecToTimespec(sourceFileInfo.Sys().(*syscall.Stat_t).Atim.Nsec), syscall.NsecToTimespec(sourceFileInfo.Sys().(*syscall.Stat_t).Mtim.Nsec)}
	if err := syscall.UtimesNano(dst, times); err != nil {
		return err
	}

	// Preserve the owner.
	if err := os.Chown(dst, int(sourceFileInfo.Sys().(*syscall.Stat_t).Uid), int(sourceFileInfo.Sys().(*syscall.Stat_t).Gid)); err != nil {
		return err
	}

	return nil
}

// copySymlink attempts to create a new symlink that points to the resolved path of the src.
func copySymlink(src, dst string) error {
	linkTarget, err := os.Readlink(src)
	if err != nil {
		return err
	}
	return os.Symlink(linkTarget, dst)
}

// copyEntry determines if the provided path is a file or a symlink and copies it accordingly.
func copyEntry(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	switch mode := info.Mode(); {
	case mode.IsRegular():
		return copyFile(src, dst)
	case mode&os.ModeSymlink != 0:
		return copySymlink(src, dst)
	case mode.IsDir():
		return os.Mkdir(dst, info.Mode())
	default:
		return nil // Skip other file types (e.g., named pipes, sockets, devices).
	}
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

		return copyEntry(path, dstPath)
	})
}
