package rrw

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/containerd/containerd/v2/pkg/userns"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/log"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

var bufPool = &sync.Pool{
	New: func() interface{} {
		buffer := make([]byte, 32*1024)
		return &buffer
	},
}

func TARToIMG(tarFilename string) (string, error) {
	imgFilename := filepath.Join(KERNEL_IMG_PATH, uuid.NewString()+".img")
	_, err := tarToIMG(tarFilename, imgFilename)
	if err != nil {
		os.Remove(imgFilename)
		return "", err
	}

	// return imgFilename, modifyImageByTAR(tarFilename, imgFilename, inodeMap)
	return imgFilename, nil
}

func modifyImageByTAR(tarFilename string, imageFilename string, inodeMap map[string]uint64) error {
	// 打开文件
	file, err := os.OpenFile(imageFilename, os.O_RDWR, 0644)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	modifySize := func(ino uint64, realSize uint64) error {
		offset := int64(4252 + (ino-2)*72)

		// 从指定位置读取4个字节
		buff := make([]byte, 4)
		_, err = file.ReadAt(buff, offset)
		if err != nil {
			return err
		}

		// 修改这个值
		modifiedValue := uint32(realSize)

		// 将修改后的uint32值转换回字节
		binary.LittleEndian.PutUint32(buff, modifiedValue)

		// 将新的字节写回原来的位置
		_, err = file.WriteAt(buff, offset)
		if err != nil {
			return err
		}
		return nil
	}

	// 打开tar文件
	tarFile, err := os.Open(tarFilename)
	if err != nil {
		return err
	}
	defer tarFile.Close()

	tr := tar.NewReader(tarFile)

	for {
		header, err := tr.Next()

		if err == io.EOF {
			break // 文件结束
		}

		if err != nil {
			return err
		}

		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			buf := bytes.NewBuffer(make([]byte, 0, header.Size))
			io.Copy(buf, tr)

			realSize := binary.BigEndian.Uint64(buf.Bytes()[:8])

			if err := modifySize(inodeMap[header.Name], realSize); err != nil {
				return err
			}
		}
	}

	return nil
}

func copySelectedBytes(src io.Reader, dst *bytes.Buffer) error {
	buffer := make([]byte, 32)
	skipBuffer := make([]byte, 8)

	for {
		// Skip 8 bytes
		_, err := io.ReadFull(src, skipBuffer)
		if err == io.EOF { // 如果源数据不足8字节，则结束
			return nil
		}
		if err != nil {
			return err
		}

		// Read 32 bytes
		n, err := io.ReadFull(src, buffer)
		if err == io.EOF || n < 32 { // 如果读不到32字节也结束
			return nil
		}
		if err != nil {
			return err
		}

		// Write the 32 bytes to destination
		_, err = dst.Write(buffer)
		if err != nil {
			return err
		}
	}
}

func tarToIMG(tarFilename string, imageFilename string) (map[string]uint64, error) {
	if err := exec.Command("bash", "-c", fmt.Sprintf("dd if=/dev/zero of=%s bs=1M count=1024", imageFilename)).Run(); err != nil {
		return nil, err
	}
	if err := exec.Command("bash", "-c", fmt.Sprintf("mkfs.simplefs %s", imageFilename)).Run(); err != nil {
		return nil, err
	}

	root, err := os.MkdirTemp("/var/rrw/temproot", "kernel-mount-root-")
	if err != nil {
		return nil, err
	}

	mountCmd := fmt.Sprintf("mount -o loop -t simplefs %s %s", imageFilename, root)
	fmt.Println(mountCmd)
	if err := exec.Command("bash", "-c", mountCmd).Run(); err != nil {
		return nil, err
	}
	defer exec.Command("bash", "-c", fmt.Sprintf("umount %s", root)).Run()

	r, err := os.Open(tarFilename)
	if err != nil {
		return nil, fmt.Errorf("failed to open tarfile: %w", err)
	}
	defer r.Close()

	_, inodeMap, err := applyNaive(context.Background(), root, r)
	if err != nil {
		return nil, fmt.Errorf("failed to write tar to root: %w", err)
	}

	return inodeMap, nil
}

func applyNaive(ctx context.Context, root string, r io.Reader) (size int64, inodeMap map[string]uint64, err error) {
	inodeMap = map[string]uint64{}
	var (
		dirs []*tar.Header

		tr = tar.NewReader(r)

		// Used for handling opaque directory markers which
		// may occur out of order
		unpackedPaths = make(map[string]struct{})
	)

	// handle whiteouts by removing the target files
	convertWhiteout := func(hdr *tar.Header, path string) (bool, error) {
		base := filepath.Base(path)
		dir := filepath.Dir(path)
		if base == whiteoutOpaqueDir {
			_, err := os.Lstat(dir)
			if err != nil {
				return false, err
			}
			err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					if os.IsNotExist(err) {
						err = nil // parent was deleted
					}
					return err
				}
				if path == dir {
					return nil
				}
				if _, exists := unpackedPaths[path]; !exists {
					err := os.RemoveAll(path)
					return err
				}
				return nil
			})
			return false, err
		}

		if strings.HasPrefix(base, whiteoutPrefix) {
			originalBase := base[len(whiteoutPrefix):]
			originalPath := filepath.Join(dir, originalBase)

			return false, os.RemoveAll(originalPath)
		}

		return true, nil
	}

	// Iterate through the files in the archive.
	for {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		default:
		}

		hdr, err := tr.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			return 0, nil, err
		}

		size += hdr.Size

		// Normalize name, for safety and for a simple is-root check
		hdr.Name = filepath.Clean(hdr.Name)

		if skipFile(hdr) {
			log.G(ctx).Warnf("file %q ignored: archive may not be supported on system", hdr.Name)
			continue
		}

		// Split name and resolve symlinks for root directory.
		ppath, base := filepath.Split(hdr.Name)
		ppath, err = fs.RootPath(root, ppath)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get root path: %w", err)
		}

		// Join to root before joining to parent path to ensure relative links are
		// already resolved based on the root before adding to parent.
		path := filepath.Join(ppath, filepath.Join("/", base))
		if path == root {
			log.G(ctx).Debugf("file %q ignored: resolved to root", hdr.Name)
			continue
		}

		// If file is not directly under root, ensure parent directory
		// exists or is created.
		if ppath != root {
			parentPath := ppath
			if base == "" {
				parentPath = filepath.Dir(path)
			}
			if err := mkparent(ctx, parentPath, root, nil); err != nil {
				return 0, nil, err
			}
		}

		// Naive whiteout convert function which handles whiteout files by
		// removing the target files.
		if err := validateWhiteout(path); err != nil {
			return 0, nil, err
		}
		writeFile, err := convertWhiteout(hdr, path)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to convert whiteout file %q: %w", hdr.Name, err)
		}
		if !writeFile {
			continue
		}
		// If path exits we almost always just want to remove and replace it.
		// The only exception is when it is a directory *and* the file from
		// the layer is also a directory. Then we want to merge them (i.e.
		// just apply the metadata from the layer).
		if fi, err := os.Lstat(path); err == nil {
			if !(fi.IsDir() && hdr.Typeflag == tar.TypeDir) {
				if err := os.RemoveAll(path); err != nil {
					return 0, nil, err
				}
			}
		}

		srcData := io.Reader(tr)
		srcHdr := hdr

		if err := createTarFile(ctx, path, root, srcHdr, srcData, false); err != nil {
			return 0, nil, err
		}
		if srcHdr.Typeflag == tar.TypeReg || srcHdr.Typeflag == tar.TypeRegA {
			fileInfo, err1 := os.Stat(path)
			if err1 != nil {
				return 0, nil, err1
			}

			// 类型断言以获取*syscall.Stat_t类型的信息
			stat, _ := fileInfo.Sys().(*syscall.Stat_t)
			inodeMap[srcHdr.Name] = stat.Ino
		}

		// Directory mtimes must be handled at the end to avoid further
		// file creation in them to modify the directory mtime
		if hdr.Typeflag == tar.TypeDir {
			dirs = append(dirs, hdr)
		}
		unpackedPaths[path] = struct{}{}
	}

	for _, hdr := range dirs {
		path, err := fs.RootPath(root, hdr.Name)
		if err != nil {
			return 0, nil, err
		}
		if err := chtimes(path, boundTime(latestTime(hdr.AccessTime, hdr.ModTime)), boundTime(hdr.ModTime)); err != nil {
			return 0, nil, err
		}
	}

	return size, inodeMap, nil
}

func createTarFile(ctx context.Context, path, extractDir string, hdr *tar.Header, reader io.Reader, noSameOwner bool) error {
	// hdr.Mode is in linux format, which we can use for syscalls,
	// but for os.Foo() calls we need the mode converted to os.FileMode,
	// so use hdrInfo.Mode() (they differ for e.g. setuid bits)
	hdrInfo := hdr.FileInfo()

	switch hdr.Typeflag {
	case tar.TypeDir:
		// Create directory unless it exists as a directory already.
		// In that case we just want to merge the two
		if fi, err := os.Lstat(path); !(err == nil && fi.IsDir()) {
			if err := mkdir(path, hdrInfo.Mode()); err != nil {
				return err
			}
		}

	//nolint:staticcheck // TypeRegA is deprecated but we may still receive an external tar with TypeRegA
	case tar.TypeReg, tar.TypeRegA:
		file, err := openFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, hdrInfo.Mode())
		if err != nil {
			return err
		}

		// buf := bytes.NewBuffer(make([]byte, 0, hdr.Size))
		// if err := copySelectedBytes(reader, buf); err != nil {
		// 	return err
		// }

		_, err = copyBuffered(ctx, file, reader)
		if err1 := file.Close(); err == nil {
			err = err1
		}
		if err != nil {
			return err
		}

	case tar.TypeBlock, tar.TypeChar:
		// Handle this is an OS-specific way
		if err := handleTarTypeBlockCharFifo(hdr, path); err != nil {
			return err
		}

	case tar.TypeFifo:
		// Handle this is an OS-specific way
		if err := handleTarTypeBlockCharFifo(hdr, path); err != nil {
			return err
		}

	case tar.TypeLink:
		targetPath, err := hardlinkRootPath(extractDir, hdr.Linkname)
		if err != nil {
			return err
		}

		if err := link(targetPath, path); err != nil {
			return err
		}

	case tar.TypeSymlink:
		if err := os.Symlink(hdr.Linkname, path); err != nil {
			return err
		}

	case tar.TypeXGlobalHeader:
		log.G(ctx).Debug("PAX Global Extended Headers found and ignored")
		return nil

	default:
		return fmt.Errorf("unhandled tar header type %d", hdr.Typeflag)
	}

	if !noSameOwner {
		if err := os.Lchown(path, hdr.Uid, hdr.Gid); err != nil {
			err = fmt.Errorf("failed to Lchown %q for UID %d, GID %d: %w", path, hdr.Uid, hdr.Gid, err)
			if errors.Is(err, syscall.EINVAL) && userns.RunningInUserNS() {
				err = fmt.Errorf("%w (Hint: try increasing the number of subordinate IDs in /etc/subuid and /etc/subgid)", err)
			}
			return err
		}
	}

	// call lchmod after lchown since lchown can modify the file mode
	if err := lchmod(path, hdrInfo.Mode()); err != nil {
		return err
	}

	return chtimes(path, boundTime(latestTime(hdr.AccessTime, hdr.ModTime)), boundTime(hdr.ModTime))
}

func mkparent(ctx context.Context, path, root string, parents []string) error {
	if dir, err := os.Lstat(path); err == nil {
		if dir.IsDir() {
			return nil
		}
		return &os.PathError{
			Op:   "mkparent",
			Path: path,
			Err:  syscall.ENOTDIR,
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	i := len(path)
	for i > len(root) && !os.IsPathSeparator(path[i-1]) {
		i--
	}

	if i > len(root)+1 {
		if err := mkparent(ctx, path[:i-1], root, parents); err != nil {
			return err
		}
	}

	if err := mkdir(path, 0755); err != nil {
		// Check that still doesn't exist
		dir, err1 := os.Lstat(path)
		if err1 == nil && dir.IsDir() {
			return nil
		}
		return err
	}

	for _, p := range parents {
		ppath, err := fs.RootPath(p, path[len(root):])
		if err != nil {
			return err
		}

		dir, err := os.Lstat(ppath)
		if err == nil {
			if !dir.IsDir() {
				// Replaced, do not copy attributes
				break
			}
			if err := copyDirInfo(dir, path); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	log.G(ctx).Debugf("parent directory %q not found: default permissions(0755) used", path)

	return nil
}

func skipFile(hdr *tar.Header) bool {
	switch hdr.Typeflag {
	case tar.TypeBlock, tar.TypeChar:
		// cannot create a device if running in user namespace
		return userns.RunningInUserNS()
	default:
		return false
	}
}

func copyBuffered(ctx context.Context, dst io.Writer, src io.Reader) (written int64, err error) {
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)

	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		default:
		}

		nr, er := src.Read(*buf)
		if nr > 0 {
			nw, ew := dst.Write((*buf)[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err

}

func mkdir(path string, perm os.FileMode) error {
	if err := os.Mkdir(path, perm); err != nil {
		return err
	}
	// Only final created directory gets explicit permission
	// call to avoid permission mask
	return os.Chmod(path, perm)
}

func chtimes(path string, atime, mtime time.Time) error {
	var utimes [2]unix.Timespec
	utimes[0] = unix.NsecToTimespec(atime.UnixNano())
	utimes[1] = unix.NsecToTimespec(mtime.UnixNano())

	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, utimes[0:], unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("failed call to UtimesNanoAt for %s: %w", path, err)
	}

	return nil
}

var (
	minTime = time.Unix(0, 0)
	maxTime time.Time
)

func init() {
	if unsafe.Sizeof(syscall.Timespec{}.Nsec) == 8 {
		// This is a 64 bit timespec
		// os.Chtimes limits time to the following
		maxTime = time.Unix(0, 1<<63-1)
	} else {
		// This is a 32 bit timespec
		maxTime = time.Unix(1<<31-1, 0)
	}
}

func boundTime(t time.Time) time.Time {
	if t.Before(minTime) || t.After(maxTime) {
		return minTime
	}

	return t
}

func latestTime(t1, t2 time.Time) time.Time {
	if t1.Before(t2) {
		return t2
	}
	return t1
}

const (
	// whiteoutPrefix prefix means file is a whiteout. If this is followed by a
	// filename this means that file has been removed from the base layer.
	// See https://github.com/opencontainers/image-spec/blob/main/layer.md#whiteouts
	whiteoutPrefix = ".wh."

	// whiteoutMetaPrefix prefix means whiteout has a special meaning and is not
	// for removing an actual file. Normally these files are excluded from exported
	// archives.
	whiteoutMetaPrefix = whiteoutPrefix + whiteoutPrefix

	// whiteoutOpaqueDir file means directory has been made opaque - meaning
	// readdir calls to this directory do not follow to lower layers.
	whiteoutOpaqueDir = whiteoutMetaPrefix + ".opq"

	paxSchilyXattr = "SCHILY.xattr."

	userXattrPrefix = "user."
)

func copyDirInfo(fi os.FileInfo, path string) error {
	st := fi.Sys().(*syscall.Stat_t)
	if err := os.Lchown(path, int(st.Uid), int(st.Gid)); err != nil {
		if os.IsPermission(err) {
			// Normally if uid/gid are the same this would be a no-op, but some
			// filesystems may still return EPERM... for instance NFS does this.
			// In such a case, this is not an error.
			if dstStat, err2 := os.Lstat(path); err2 == nil {
				st2 := dstStat.Sys().(*syscall.Stat_t)
				if st.Uid == st2.Uid && st.Gid == st2.Gid {
					err = nil
				}
			}
		}
		if err != nil {
			return fmt.Errorf("failed to chown %s: %w", path, err)
		}
	}

	if err := os.Chmod(path, fi.Mode()); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", path, err)
	}

	timespec := []unix.Timespec{
		unix.NsecToTimespec(syscall.TimespecToNsec(fs.StatAtime(st))),
		unix.NsecToTimespec(syscall.TimespecToNsec(fs.StatMtime(st))),
	}
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, timespec, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("failed to utime %s: %w", path, err)
	}

	return nil
}

// lchmod checks for symlink and changes the mode if not a symlink
func lchmod(path string, mode os.FileMode) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func handleTarTypeBlockCharFifo(hdr *tar.Header, path string) error {
	mode := uint32(hdr.Mode & 07777)
	switch hdr.Typeflag {
	case tar.TypeBlock:
		mode |= unix.S_IFBLK
	case tar.TypeChar:
		mode |= unix.S_IFCHR
	case tar.TypeFifo:
		mode |= unix.S_IFIFO
	}

	return mknod(path, mode, unix.Mkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor)))
}

// mknod wraps Unix.Mknod and casts dev to int
func mknod(path string, mode uint32, dev uint64) error {
	return unix.Mknod(path, mode, int(dev))
}

func hardlinkRootPath(root, linkname string) (string, error) {
	ppath, base := filepath.Split(linkname)
	ppath, err := fs.RootPath(root, ppath)
	if err != nil {
		return "", err
	}

	targetPath := filepath.Join(ppath, base)
	if !strings.HasPrefix(targetPath, root) {
		targetPath = root
	}
	return targetPath, nil
}

func validateWhiteout(path string) error {
	base := filepath.Base(path)
	dir := filepath.Dir(path)

	if base == whiteoutOpaqueDir {
		return nil
	}

	if strings.HasPrefix(base, whiteoutPrefix) {
		originalBase := base[len(whiteoutPrefix):]
		originalPath := filepath.Join(dir, originalBase)

		// Ensure originalPath is under dir
		if dir[len(dir)-1] != filepath.Separator {
			dir += string(filepath.Separator)
		}
		if !strings.HasPrefix(originalPath, dir) {
			return fmt.Errorf("invalid whiteout name: %v: %w", base, errInvalidArchive)
		}
	}
	return nil
}

var errInvalidArchive = errors.New("invalid archive")

func link(oldname, newname string) error {
	return os.Link(oldname, newname)
}

func openFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	// Call chmod to avoid permission mask
	if err := os.Chmod(name, perm); err != nil {
		f.Close()
		return nil, err
	}
	return f, err
}
