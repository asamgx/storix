package walk

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// ReadDirLstat is the default DirReader: os.ReadDir for the sorted name list
// plus one lstat per entry. os.ReadDir opens the directory, never a file.
type ReadDirLstat struct {
	// Lstat defaults to unix.Lstat. Tests override it; production never does.
	Lstat func(path string, st *unix.Stat_t) error
}

// ReadDir implements DirReader.
func (r ReadDirLstat) ReadDir(path string) ([]Entry, error) {
	des, rerr := os.ReadDir(path)
	if rerr != nil && len(des) == 0 {
		return nil, rerr
	}
	lstat := r.Lstat
	if lstat == nil {
		lstat = unix.Lstat
	}
	prefix := path
	if prefix != "/" {
		prefix += "/"
	}
	entries := make([]Entry, len(des))
	for i, de := range des {
		e := &entries[i]
		e.Name = de.Name()
		var st unix.Stat_t
		if err := lstat(prefix+e.Name, &st); err != nil {
			e.Err = Errno(err)
			e.Kind = kindFromMode(uint32(de.Type()) & uint32(os.ModeType))
			continue
		}
		e.Kind = kindFromStatMode(st.Mode)
		e.Ino = st.Ino
		e.Dev = st.Dev
		e.Nlink = st.Nlink
		e.Blocks = st.Blocks
		e.Size = st.Size
		e.MtimeSec = st.Mtim.Sec
		e.Flags = st.Flags
	}
	return entries, rerr
}

// kindFromStatMode maps st_mode to a Kind.
func kindFromStatMode(mode uint16) Kind {
	switch mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return KindDir
	case unix.S_IFREG:
		return KindFile
	case unix.S_IFLNK:
		return KindSymlink
	default:
		return KindOther
	}
}

// kindFromMode maps an os.FileMode type bit set to a Kind; used only as a
// fallback for entries whose lstat failed.
func kindFromMode(m uint32) Kind {
	switch {
	case os.FileMode(m)&os.ModeDir != 0:
		return KindDir
	case os.FileMode(m)&os.ModeSymlink != 0:
		return KindSymlink
	case os.FileMode(m)&os.ModeType == 0:
		return KindFile
	default:
		return KindOther
	}
}

// Errno extracts a syscall.Errno from an error, returning EIO when the error
// carries none.
func Errno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return syscall.EIO
}
