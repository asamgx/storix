package walk

import "syscall"

// Entry is one directory entry with the lstat attributes the walker needs.
type Entry struct {
	Name     string
	Kind     Kind
	Ino      uint64
	Dev      int32
	Nlink    uint16
	Blocks   int64 // st_blocks, 512-byte units
	Size     int64 // st_size
	MtimeSec int64
	Flags    uint32        // st_flags
	Err      syscall.Errno // non-zero when the entry could not be stat'd
}

// Allocated returns the bytes the entry occupies on disk.
func (e *Entry) Allocated() int64 { return e.Blocks * 512 }

// DirReader lists a directory. Implementations must not open any file: the
// default reader opens only the directory itself, via os.ReadDir.
type DirReader interface {
	// ReadDir returns the entries of path sorted by name. A non-nil error may
	// come with a partial (or nil) slice; the walker records the error and
	// processes whatever entries it got.
	ReadDir(path string) ([]Entry, error)
}
