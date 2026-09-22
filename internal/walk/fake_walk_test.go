package walk

import (
	"syscall"
	"testing"
)

// fakeWalk runs a walk over a fake reader rooted at "/fake".
func fakeWalk(t *testing.T, f *fakeReader, tweak func(*Options)) *Tree {
	t.Helper()
	f.sortAll()
	opts := Options{Root: f.root, Reader: f, Parallelism: 4, SkipNames: []string{}, SkipPaths: []string{}, ExemptPrefixes: []string{}}
	if tweak != nil {
		tweak(&opts)
	}
	tree, err := Walk(t.Context(), opts)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return tree
}

// TestDatalessDirectoryIsNeverListed is the rule that keeps a scan from
// pulling a cloud folder down: listing a SF_DATALESS directory is itself the
// materialization trigger, so the walker must stop at it.
func TestDatalessDirectoryIsNeverListed(t *testing.T) {
	f := newFake()
	f.addDir("/fake/cloud", sfDataless)
	f.addDir("/fake/cloud/inside", 0)
	f.addFile("/fake/cloud/inside/huge.bin", Entry{Blocks: 1 << 20, Size: 1 << 29})
	f.addDir("/fake/local", 0)
	f.addFile("/fake/local/file.bin", Entry{Blocks: 256, Size: 128 * 1024})

	tree := fakeWalk(t, f, nil)

	cloud, ok := tree.Lookup("/fake/cloud")
	if !ok {
		t.Fatal("cloud directory missing from the tree")
	}
	if !cloud.Has(FlagDataless) {
		t.Errorf("cloud flags = %04x, want FlagDataless", cloud.Flags)
	}
	if len(cloud.Children) != 0 || cloud.Bytes != 0 {
		t.Errorf("cloud: children=%d bytes=%d, want nothing", len(cloud.Children), cloud.Bytes)
	}
	if n := f.timesListed("/fake/cloud"); n != 0 {
		t.Errorf("cloud was listed %d times, want 0", n)
	}
	if !tree.Root.Has(FlagPartial) {
		t.Error("root should be partial when a dataless directory was not listed")
	}
	if tree.Root.Bytes != 256*512 {
		t.Errorf("root bytes = %d, want only the local file's %d", tree.Root.Bytes, 256*512)
	}
}

func TestDatalessAndCompressedFiles(t *testing.T) {
	f := newFake()
	// Evicted iCloud file: no blocks, large logical size, retained because
	// max(allocated, apparent) is what the threshold compares.
	f.addFile("/fake/evicted.mov", Entry{Blocks: 0, Size: 4 << 20, Flags: sfDataless})
	f.addFile("/fake/tiny.txt", Entry{Blocks: 0, Size: 10, Flags: sfDataless})
	f.addFile("/fake/squeezed.bin", Entry{Blocks: 16, Size: 1 << 20, Flags: ufCompressed})

	tree := fakeWalk(t, f, nil)

	ev, ok := tree.Lookup("/fake/evicted.mov")
	if !ok {
		t.Fatal("evicted.mov not retained")
	}
	if !ev.Has(FlagDataless) || ev.Bytes != 0 || ev.Apparent != 4<<20 {
		t.Errorf("evicted.mov = %+v, want dataless with 0 allocated", ev)
	}
	sq, ok := tree.Lookup("/fake/squeezed.bin")
	if !ok {
		t.Fatal("squeezed.bin not retained")
	}
	if !sq.Has(FlagCompressed) || sq.Bytes != 16*512 {
		t.Errorf("squeezed.bin = %+v, want compressed with real blocks", sq)
	}
	if tree.Root.Small.Files != 1 || tree.Root.Small.Dataless != 1 {
		t.Errorf("root.Small = %+v, want one aggregated dataless file", tree.Root.Small)
	}
}

func TestErrorClassification(t *testing.T) {
	f := newFake()
	f.addDir("/fake/denied", 0)
	f.failDir("/fake/denied", syscall.EACCES)
	f.addDir("/fake/mail", 0)
	f.failDir("/fake/mail", syscall.EPERM)
	f.addDir("/fake/Library/Caches/com.apple.ap.adprivacyd", 0)
	f.failDir("/fake/Library/Caches/com.apple.ap.adprivacyd", syscall.EPERM)
	f.addDir("/fake/gone", 0)
	f.failDir("/fake/gone", syscall.ENOENT)
	f.addDir("/fake/weird", 0)
	f.failDir("/fake/weird", syscall.EIO)
	// An entry whose lstat failed produces an error but no node.
	f.addFile("/fake/racing.tmp", Entry{Err: syscall.ENOENT})

	tree := fakeWalk(t, f, nil)

	want := map[string]ErrClass{
		"/fake/denied": ErrPermission,
		"/fake/mail":   ErrTCC,
		"/fake/Library/Caches/com.apple.ap.adprivacyd": ErrProtected,
		"/fake/gone":       ErrVanished,
		"/fake/weird":      ErrOther,
		"/fake/racing.tmp": ErrVanished,
	}
	got := map[string]ErrClass{}
	for _, e := range tree.Errors {
		got[e.Path] = e.Class
	}
	for path, class := range want {
		if got[path] != class {
			t.Errorf("%s classified %s, want %s", path, got[path], class)
		}
	}
	if len(tree.Errors) != len(want) {
		t.Errorf("errors = %+v, want %d", tree.Errors, len(want))
	}
	if tree.Vanished != 2 {
		t.Errorf("Vanished = %d, want 2", tree.Vanished)
	}
	if _, ok := tree.Lookup("/fake/racing.tmp"); ok {
		t.Error("an entry that failed lstat must not become a node")
	}
	// Errors are sorted, so the list is stable across runs.
	for i := 1; i < len(tree.Errors); i++ {
		if tree.Errors[i-1].Path > tree.Errors[i].Path {
			t.Fatal("errors are not sorted by path")
		}
	}
}

func TestPartialErrorStillYieldsEntries(t *testing.T) {
	// A reader may return entries together with an error; both are kept.
	f := newFake()
	f.addDir("/fake/half", 0)
	f.addFile("/fake/half/kept.bin", Entry{Blocks: 256, Size: 128 * 1024})
	pr := &partialReader{fakeReader: f, failPath: "/fake/half", errno: syscall.EPERM}

	tree := fakeWalk(t, f, func(o *Options) { o.Reader = pr })

	half, ok := tree.Lookup("/fake/half")
	if !ok || !half.Has(FlagUnreadable) {
		t.Fatalf("half = %+v, want unreadable", half)
	}
	if _, ok := tree.Lookup("/fake/half/kept.bin"); !ok {
		t.Error("entries returned alongside the error were dropped")
	}
}

// partialReader returns a directory's entries together with an error, the way
// os.ReadDir does when it fails part way through.
type partialReader struct {
	*fakeReader
	failPath string
	errno    syscall.Errno
}

func (p *partialReader) ReadDir(path string) ([]Entry, error) {
	es, err := p.fakeReader.ReadDir(path)
	if path == p.failPath {
		return es, p.errno
	}
	return es, err
}

func TestExemptPrefixWildcard(t *testing.T) {
	f := newFake()
	f.addDir("/fake/Users", 0)
	f.addDir("/fake/Users/alice", 0)
	f.addDir("/fake/Users/alice/Library", 0)
	f.addDir("/fake/Users/alice/Library/Preferences", 0)
	f.addDir("/fake/Users/alice/Library/Preferences/ByHost", 0)
	f.addFile("/fake/Users/alice/Library/Preferences/app.plist", Entry{Blocks: 8, Size: 400})
	f.addFile("/fake/Users/alice/Library/Preferences/ByHost/deep.plist", Entry{Blocks: 8, Size: 400})
	f.addDir("/fake/Users/alice/Library/Caches", 0)
	f.addFile("/fake/Users/alice/Library/Caches/junk.bin", Entry{Blocks: 8, Size: 400})

	tree := fakeWalk(t, f, func(o *Options) {
		o.ExemptPrefixes = []string{"Users/*/Library/Preferences"}
	})

	for _, p := range []string{
		"/fake/Users/alice/Library/Preferences/app.plist",
		"/fake/Users/alice/Library/Preferences/ByHost/deep.plist",
	} {
		n, ok := tree.Lookup(p)
		if !ok {
			t.Errorf("%s: not retained under the exemption", p)
			continue
		}
		if !n.Has(FlagExempt) {
			t.Errorf("%s flags = %04x, want FlagExempt", p, n.Flags)
		}
	}
	if _, ok := tree.Lookup("/fake/Users/alice/Library/Caches/junk.bin"); ok {
		t.Error("the exemption leaked outside its prefix")
	}
}

func TestBundleFlag(t *testing.T) {
	f := newFake()
	f.addDir("/fake/Photo Booth.app", 0)
	f.addDir("/fake/plain", 0)

	tree := fakeWalk(t, f, nil)

	app, _ := tree.Lookup("/fake/Photo Booth.app")
	if app == nil || !app.Has(FlagBundle) {
		t.Errorf("app bundle not flagged: %+v", app)
	}
	plain, _ := tree.Lookup("/fake/plain")
	if plain == nil || plain.Has(FlagBundle) {
		t.Errorf("plain directory flagged as a bundle: %+v", plain)
	}
}
