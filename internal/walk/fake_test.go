package walk

import (
	"path"
	"sort"
	"sync"
	"syscall"
)

// fakeReader is an in-memory DirReader. It lets tests produce attributes the
// filesystem will not make on demand: SF_DATALESS, UF_COMPRESSED, and specific
// errnos on specific paths.
type fakeReader struct {
	mu      sync.Mutex
	dirs    map[string][]Entry
	dirErrs map[string]syscall.Errno
	listed  map[string]int // how often each path was listed
	root    string
}

// fakeRoot is the scan root of every fake tree.
const fakeRoot = "/fake"

func newFake() *fakeReader {
	f := &fakeReader{
		dirs:    map[string][]Entry{},
		dirErrs: map[string]syscall.Errno{},
		listed:  map[string]int{},
		root:    fakeRoot,
	}
	f.dirs[fakeRoot] = nil
	return f
}

// addDir registers a directory, creating any missing ancestor, and links it
// into its parent.
func (f *fakeReader) addDir(p string, flags uint32) {
	if _, ok := f.dirs[p]; ok {
		return
	}
	f.dirs[p] = nil
	if p == f.root {
		return
	}
	parent := path.Dir(p)
	f.addDir(parent, 0)
	f.dirs[parent] = append(f.dirs[parent], Entry{
		Name: path.Base(p), Kind: KindDir, Ino: uint64(len(f.dirs) + 1000), Dev: 1, Nlink: 2, Flags: flags,
	})
}

// addFile registers a leaf under its parent directory.
func (f *fakeReader) addFile(p string, e Entry) {
	e.Name = path.Base(p)
	if e.Dev == 0 {
		e.Dev = 1
	}
	if e.Nlink == 0 {
		e.Nlink = 1
	}
	if e.Kind == KindDir {
		e.Kind = KindFile
	}
	parent := path.Dir(p)
	f.addDir(parent, 0)
	f.dirs[parent] = append(f.dirs[parent], e)
}

// failDir makes listing p return errno.
func (f *fakeReader) failDir(p string, errno syscall.Errno) { f.dirErrs[p] = errno }

// sortAll puts every directory's entries in name order, as os.ReadDir does.
func (f *fakeReader) sortAll() {
	for _, es := range f.dirs {
		sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	}
}

func (f *fakeReader) ReadDir(p string) ([]Entry, error) {
	f.mu.Lock()
	f.listed[p]++
	f.mu.Unlock()
	if errno, ok := f.dirErrs[p]; ok {
		return nil, errno
	}
	es, ok := f.dirs[p]
	if !ok {
		return nil, syscall.ENOENT
	}
	return es, nil
}

func (f *fakeReader) Stat(p string) (Entry, error) {
	if _, ok := f.dirs[p]; !ok {
		return Entry{}, syscall.ENOENT
	}
	return Entry{Name: path.Base(p), Kind: KindDir, Dev: 1, Nlink: 2}, nil
}

func (f *fakeReader) timesListed(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listed[p]
}

// fakeMounts is a mount table with a fixed set of mount points.
type fakeMounts struct {
	points map[string]bool
	fsType string
	from   string
}

func (m fakeMounts) IsMountPoint(p string) bool { return m.points[p] }

func (m fakeMounts) MountInfo(p string) (string, string, bool) {
	if !m.points[p] {
		return "", "", false
	}
	return m.fsType, m.from, true
}
