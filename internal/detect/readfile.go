package detect

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// MaxReadFile is the largest file a detector may read. Every file a detector
// wants is a plist, a receipt or a version marker; a megabyte is far past all
// of them and small enough that a file pretending to be one cannot cost
// anything.
const MaxReadFile = 1 << 20

// MaxReadDir is the largest directory a detector may list. The directories
// detectors list are /Applications, the Caskroom and the launch agent folders,
// which hold tens of entries; ten thousand is far past any of them.
//
// Passing the limit is an error rather than a truncation. A silently short
// listing would make the apps inventory report an installed application as
// missing, which is worse than reporting that the directory could not be read.
const MaxReadDir = 10_000

// FileInfo is what a detector learns by asking whether a path exists. It is a
// narrow subset of fs.FileInfo so that a test can answer with a struct
// literal instead of a filesystem.
type FileInfo struct {
	Name  string
	Size  int64
	Mode  fs.FileMode
	IsDir bool
}

// IsSymlink reports whether the path is a symbolic link.
//
// It matters for exactly one question the apps inventory asks. A cask leaves
// behind entries like Caskroom/cursor/1.2/Cursor.app pointing at
// /Applications/Cursor.app, and when the application has been deleted the link
// is still there and still occupies a directory entry. Stat says it exists,
// because it does; only this says it is a link and not the bundle.
func (f FileInfo) IsSymlink() bool { return f.Mode&fs.ModeSymlink != 0 }

// ReadFile is the one sanctioned file reader for detectors.
//
// The walker never opens a file at all, for the reason walk's package comment
// gives: opening an evicted iCloud file asks the File Provider to download it,
// and a disk survey that downloads fifty gigabytes to measure them has done
// the user real harm. Detectors do need to read a handful of small files, so
// this is the single place where that happens and the single place where the
// rules are enforced:
//
//   - the path is lstat'd first, so a symlink is refused rather than followed;
//   - a file carrying SF_DATALESS is refused, because reading it is exactly
//     the download the whole design avoids;
//   - anything that is not a regular file is refused;
//   - the size is checked before the open and the read is limited after it, so
//     a file that grows between the two cannot exceed the cap;
//   - the open uses O_NOFOLLOW and the result is re-stat'd through the file
//     descriptor, so a path swapped for a symlink between the lstat and the
//     open is caught rather than followed.
func ReadFile(path string) ([]byte, error) {
	return ReadFileLimit(path, MaxReadFile)
}

// ReadFileLimit is ReadFile with an explicit cap.
func ReadFileLimit(path string, limit int64) ([]byte, error) {
	var lst unix.Stat_t
	if err := unix.Lstat(path, &lst); err != nil {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	if lst.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fmt.Errorf("not a regular file")}
	}
	if lst.Flags&uint32(unix.SF_DATALESS) != 0 {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fmt.Errorf("dataless: reading it would download it")}
	}
	if lst.Size > limit {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fmt.Errorf("%d bytes is over the %d-byte limit", lst.Size, limit)}
	}

	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()

	var fst unix.Stat_t
	if err := unix.Fstat(fd, &fst); err != nil {
		return nil, &fs.PathError{Op: "fstat", Path: path, Err: err}
	}
	if fst.Ino != lst.Ino || fst.Dev != lst.Dev {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fmt.Errorf("the path changed between the stat and the open")}
	}

	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	return data, nil
}

// Stat is how a detector asks whether a path exists. It never follows a final
// symlink, so a dangling link is reported as what it is rather than as a
// missing file.
func Stat(path string) (FileInfo, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Name: fi.Name(), Size: fi.Size(), Mode: fi.Mode(), IsDir: fi.IsDir()}, nil
}

// Exists reports whether env.Stat finds anything at path. It is the gate
// nearly every detector opens with, so it is written once here.
func (e Env) Exists(path string) bool {
	if e.Stat == nil || path == "" {
		return false
	}
	_, err := e.Stat(path)
	return err == nil
}
