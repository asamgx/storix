package detect

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
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
		return nil, &fs.PathError{Op: "read", Path: path, Err: errors.New("not a regular file")}
	}
	if lst.Flags&uint32(unix.SF_DATALESS) != 0 {
		return nil, &fs.PathError{Op: "read", Path: path, Err: errors.New("dataless: reading it would download it")}
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
		return nil, &fs.PathError{Op: "read", Path: path, Err: errors.New("the path changed between the stat and the open")}
	}

	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	return data, nil
}

// ReadDir is the one sanctioned directory lister for detectors.
//
// It is ReadFile's twin and carries the same guarantees, for the same reason.
// Listing a directory carrying SF_DATALESS asks the File Provider to
// materialize it, which is the download the whole design exists to avoid, and
// internal/walk refuses to list one for exactly that reason. A detector that
// reached for os.ReadDir would walk around both defences, so the lint test
// forbids it everywhere but here.
//
//   - the path is lstat'd first, so a symlink to somewhere else is refused
//     rather than followed, and anything that is not a directory is refused;
//   - a directory carrying SF_DATALESS is refused;
//   - the open uses O_DIRECTORY and O_NOFOLLOW and the result is re-stat'd
//     through the descriptor, so a path swapped between the two is caught;
//   - more than MaxReadDir entries is an error, never a silent truncation.
//
// The entries are sorted by name, as os.ReadDir's are, so two runs over the
// same directory produce the same order and a golden file can hold it.
func ReadDir(path string) ([]os.DirEntry, error) {
	var lst unix.Stat_t
	if err := unix.Lstat(path, &lst); err != nil {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	if lst.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return nil, &fs.PathError{Op: "readdir", Path: path, Err: errors.New("not a directory")}
	}
	if lst.Flags&uint32(unix.SF_DATALESS) != 0 {
		return nil, &fs.PathError{Op: "readdir", Path: path, Err: errors.New("dataless: listing it would download it")}
	}

	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
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
		return nil, &fs.PathError{Op: "readdir", Path: path, Err: errors.New("the path changed between the stat and the open")}
	}

	// One more than the limit, so a directory exactly at it still reads and
	// one past it is detected rather than quietly cut short.
	entries, err := f.ReadDir(MaxReadDir + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, &fs.PathError{Op: "readdir", Path: path, Err: err}
	}
	if len(entries) > MaxReadDir {
		return nil, &fs.PathError{Op: "readdir", Path: path,
			Err: fmt.Errorf("more than %d entries", MaxReadDir)}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
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

// Lookup answers the question Exists cannot: whether a path is there, and,
// when that is not knowable, why.
//
// The distinction is the whole point. A stat that fails with ENOENT says the
// path is gone; a stat that fails with EPERM says only that this process was
// not allowed to look, which on macOS is the ordinary answer for half the
// user's Library until Full Disk Access is granted. Collapsing the second into
// "it does not exist" is how an installed application's receipt, launch item
// or LaunchServices registration turns into evidence that the application was
// uninstalled — the most expensive wrong answer the apps inventory can give.
//
// A path that is there returns (true, nil); one that is definitely gone
// returns (false, nil); anything else returns (false, err) and means the
// caller does not know.
func (e Env) Lookup(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	if e.Stat == nil {
		return false, &fs.PathError{Op: "stat", Path: path, Err: errors.New("no stat function is configured")}
	}
	if _, err := e.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Exists reports whether env.Stat finds anything at path. It is the gate
// nearly every detector opens with, so it is written once here.
//
// It answers a boolean question with a boolean, which means it cannot tell
// "gone" from "could not look": both are false. That is fine for a caller
// deciding whether to bother reading a file, and wrong for a caller turning
// the answer into evidence about what the user has installed. Those callers
// use [Env.Lookup] instead.
func (e Env) Exists(path string) bool {
	ok, _ := e.Lookup(path)
	return ok
}
