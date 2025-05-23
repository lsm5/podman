//go:build cgo

package copy

/*
#include <linux/fs.h>

#ifndef FICLONE
#define FICLONE		_IOW(0x94, 9, int)
#endif
*/
import "C"

import (
	"container/list"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/containers/storage/pkg/idtools"
	"github.com/containers/storage/pkg/pools"
	"github.com/containers/storage/pkg/system"
	"github.com/containers/storage/pkg/unshare"
	"golang.org/x/sys/unix"
)

// Mode indicates whether to use hardlink or copy content
type Mode int

const (
	// Content creates a new file, and copies the content of the file
	Content Mode = iota
	// Hardlink creates a new hardlink to the existing file
	Hardlink
)

// CopyRegularToFile copies the content of a file to another
func CopyRegularToFile(srcPath string, dstFile *os.File, fileinfo os.FileInfo, copyWithFileRange, copyWithFileClone *bool) error { //nolint: revive
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if *copyWithFileClone {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, dstFile.Fd(), C.FICLONE, srcFile.Fd())
		if errno == 0 {
			return nil
		}

		*copyWithFileClone = false
		if errno == unix.EXDEV {
			*copyWithFileRange = false
		}
	}
	if *copyWithFileRange {
		err = doCopyWithFileRange(srcFile, dstFile, fileinfo)
		// Trying the file_clone may not have caught the exdev case
		// as the ioctl may not have been available (therefore EINVAL)
		if err == unix.EXDEV || err == unix.ENOSYS {
			*copyWithFileRange = false
		} else {
			return err
		}
	}
	return legacyCopy(srcFile, dstFile)
}

// CopyRegular copies the content of a file to another
func CopyRegular(srcPath, dstPath string, fileinfo os.FileInfo, copyWithFileRange, copyWithFileClone *bool) error { //nolint: revive
	// If the destination file already exists, we shouldn't blow it away
	dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileinfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	return CopyRegularToFile(srcPath, dstFile, fileinfo, copyWithFileRange, copyWithFileClone)
}

func doCopyWithFileRange(srcFile, dstFile *os.File, fileinfo os.FileInfo) error {
	amountLeftToCopy := fileinfo.Size()

	for amountLeftToCopy > 0 {
		n, err := unix.CopyFileRange(int(srcFile.Fd()), nil, int(dstFile.Fd()), nil, int(amountLeftToCopy), 0)
		if err != nil {
			return err
		}

		amountLeftToCopy = amountLeftToCopy - int64(n)
	}

	return nil
}

func legacyCopy(srcFile io.Reader, dstFile io.Writer) error {
	_, err := pools.Copy(dstFile, srcFile)

	return err
}

func copyXattr(srcPath, dstPath, attr string) error {
	data, err := system.Lgetxattr(srcPath, attr)
	if err != nil && !errors.Is(err, system.ENOTSUP) {
		return err
	}
	if data != nil {
		if err := system.Lsetxattr(dstPath, attr, data, 0); err != nil {
			return err
		}
	}
	return nil
}

type fileID struct {
	dev uint64
	ino uint64
}

type dirMtimeInfo struct {
	dstPath *string
	stat    *syscall.Stat_t
}

// DirCopy copies or hardlinks the contents of one directory to another,
// properly handling xattrs, and soft links
//
// Copying xattrs can be opted out of by passing false for copyXattrs.
func DirCopy(srcDir, dstDir string, copyMode Mode, copyXattrs bool) error {
	copyWithFileRange := true
	copyWithFileClone := true

	// This is a map of source file inodes to dst file paths
	copiedFiles := make(map[fileID]string)

	dirsToSetMtimes := list.New()
	err := filepath.Walk(srcDir, func(srcPath string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Rebase path
		relPath, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dstDir, relPath)
		stat, ok := f.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("unable to get raw syscall.Stat_t data for %s", srcPath)
		}

		isHardlink := false

		switch mode := f.Mode(); {
		case mode.IsRegular():
			id := fileID{
				dev: uint64(stat.Dev), //nolint:unconvert
				ino: stat.Ino,
			}
			if copyMode == Hardlink {
				isHardlink = true
				if err2 := os.Link(srcPath, dstPath); err2 != nil {
					return err2
				}
			} else if hardLinkDstPath, ok := copiedFiles[id]; ok {
				if err2 := os.Link(hardLinkDstPath, dstPath); err2 != nil {
					return err2
				}
			} else {
				if err2 := CopyRegular(srcPath, dstPath, f, &copyWithFileRange, &copyWithFileClone); err2 != nil {
					return err2
				}
				copiedFiles[id] = dstPath
			}

		case mode.IsDir():
			if err := os.Mkdir(dstPath, f.Mode()); err != nil && !os.IsExist(err) {
				return err
			}

		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}

			if err := os.Symlink(link, dstPath); err != nil {
				return err
			}

		case mode&os.ModeNamedPipe != 0:
			if err := unix.Mkfifo(dstPath, stat.Mode); err != nil {
				return err
			}

		case mode&os.ModeSocket != 0:
			if err := unix.Mknod(dstPath, stat.Mode, int(stat.Rdev)); err != nil {
				return err
			}

		case mode&os.ModeDevice != 0:
			if unshare.IsRootless() {
				// cannot create a device if running in user namespace
				return nil
			}
			if err := unix.Mknod(dstPath, stat.Mode, int(stat.Rdev)); err != nil {
				return err
			}

		default:
			return fmt.Errorf("unknown file type with mode %v for %s", mode, srcPath)
		}

		// Everything below is copying metadata from src to dst. All this metadata
		// already shares an inode for hardlinks.
		if isHardlink {
			return nil
		}

		if err := idtools.SafeLchown(dstPath, int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}

		if copyXattrs {
			if err := doCopyXattrs(srcPath, dstPath); err != nil {
				return err
			}
		}

		isSymlink := f.Mode()&os.ModeSymlink != 0

		// There is no LChmod, so ignore mode for symlink. Also, this
		// must happen after chown, as that can modify the file mode
		if !isSymlink {
			if err := os.Chmod(dstPath, f.Mode()); err != nil {
				return err
			}
		}

		// system.Chtimes doesn't support a NOFOLLOW flag atm
		if f.IsDir() {
			dirsToSetMtimes.PushFront(&dirMtimeInfo{dstPath: &dstPath, stat: stat})
		} else if !isSymlink {
			aTime := time.Unix(stat.Atim.Unix())
			mTime := time.Unix(stat.Mtim.Unix())
			if err := system.Chtimes(dstPath, aTime, mTime); err != nil {
				return err
			}
		} else {
			ts := []syscall.Timespec{stat.Atim, stat.Mtim}
			if err := system.LUtimesNano(dstPath, ts); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for e := dirsToSetMtimes.Front(); e != nil; e = e.Next() {
		mtimeInfo := e.Value.(*dirMtimeInfo)
		ts := []syscall.Timespec{mtimeInfo.stat.Atim, mtimeInfo.stat.Mtim}
		if err := system.LUtimesNano(*mtimeInfo.dstPath, ts); err != nil {
			return err
		}
	}

	return nil
}

func doCopyXattrs(srcPath, dstPath string) error {
	if err := copyXattr(srcPath, dstPath, "security.capability"); err != nil {
		return err
	}

	xattrs, err := system.Llistxattr(srcPath)
	if err != nil && !errors.Is(err, system.ENOTSUP) {
		return err
	}

	for _, key := range xattrs {
		if strings.HasPrefix(key, "user.") {
			if err := copyXattr(srcPath, dstPath, key); err != nil {
				return err
			}
		}
	}

	if unshare.IsRootless() {
		return nil
	}

	// We need to copy this attribute if it appears in an overlay upper layer, as
	// this function is used to copy those. It is set by overlay if a directory
	// is removed and then re-created and should not inherit anything from the
	// same dir in the lower dir.
	return copyXattr(srcPath, dstPath, "trusted.overlay.opaque")
}
