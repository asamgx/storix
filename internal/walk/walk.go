package walk

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// st_flags bits the walker cares about.
const (
	sfDataless   = uint32(unix.SF_DATALESS)
	ufCompressed = uint32(unix.UF_COMPRESSED)
)

// DefaultSmallFileThreshold is the size below which a leaf is folded into its
// parent's Small aggregate instead of getting a Node of its own.
const DefaultSmallFileThreshold = 64 * 1024

// DefaultProgressInterval is how often a running walk publishes counters.
const DefaultProgressInterval = 100 * time.Millisecond

// MountChecker reports whether a path is a mount point. internal/volume's
// MountTable satisfies it.
type MountChecker interface {
	IsMountPoint(path string) bool
}

// MountDescriber is an optional MountChecker extension: when the mount table
// can describe a mount, the skipped-mount list carries its type and source.
type MountDescriber interface {
	MountInfo(path string) (fsType, from string, ok bool)
}

// RootStater is an optional DirReader extension used to stat the scan root.
// Without it the root is stat'd with unix.Lstat, which is what production
// does; fakes implement it so tests need no real directory.
type RootStater interface {
	Stat(path string) (Entry, error)
}

// Options configures a walk.
type Options struct {
	// Root is the directory to walk. It is cleaned; the caller is responsible
	// for passing a mount point in production (see Mounts).
	Root string
	// Parallelism is the number of workers; default min(2*NumCPU, 16).
	Parallelism int
	// SmallFileThreshold is compared against max(allocated, apparent);
	// default DefaultSmallFileThreshold. Zero means the default, negative
	// means retain everything.
	SmallFileThreshold int64
	// ExemptPrefixes lists path prefixes under which every leaf is retained.
	// An absolute entry is used as is, a relative one is resolved against
	// Root, and "*" matches exactly one path segment. Nil selects
	// DefaultExemptPrefixes; an empty non-nil slice selects none.
	ExemptPrefixes []string
	// RetainLeaf is an optional per-file retention hook (phase 1b).
	RetainLeaf func(dir string, e *Entry) bool
	// SkipNames are directory names never entered. Nil selects
	// DefaultSkipNames; an empty non-nil slice selects none.
	SkipNames []string
	// SkipPaths are absolute scan paths never entered. Nil selects
	// DefaultSkipPaths; an empty non-nil slice selects none.
	SkipPaths []string
	// Mounts guards mount boundaries. Every path other than Root for which
	// IsMountPoint reports true is recorded and not entered. Nil disables the
	// guard, which is only correct when the tree holds no nested mounts.
	Mounts MountChecker
	// Reader lists directories; nil selects ReadDirLstat.
	Reader DirReader
	// Events optionally receives ProgressEvents during the walk and one
	// DoneEvent after it. Progress is dropped when the consumer is behind;
	// the DoneEvent send blocks, so a non-nil channel must be drained.
	Events chan<- Event
	// ProgressInterval is the progress tick; default DefaultProgressInterval.
	ProgressInterval time.Duration
}

// Tree is the result of a walk.
type Tree struct {
	Root     *Node
	Nodes    []*Node // preorder; the index is the node's stable ID
	Opts     Options
	Started  time.Time
	Finished time.Time
	// Incomplete is set when the context was cancelled before the walk ended.
	Incomplete     bool
	Errors         []PathError
	SkippedMounts  []SkippedMount
	SkipListed     []string
	LinkGroups     uint64 // hard-linked inodes seen more than zero times
	LinkBytesSaved uint64 // bytes that would have been double counted
	Vanished       uint64 // entries that disappeared mid-walk (ENOENT)
}

// applyDefaults fills unset options and validates the rest.
func (o *Options) applyDefaults() error {
	if o.Root == "" {
		return errors.New("walk: Root is required")
	}
	o.Root = filepath.Clean(o.Root)
	if !filepath.IsAbs(o.Root) {
		return fmt.Errorf("walk: Root %q must be absolute", o.Root)
	}
	if o.Parallelism <= 0 {
		o.Parallelism = min(2*runtime.NumCPU(), 16)
	}
	if o.SmallFileThreshold == 0 {
		o.SmallFileThreshold = DefaultSmallFileThreshold
	}
	if o.ExemptPrefixes == nil {
		o.ExemptPrefixes = DefaultExemptPrefixes
	}
	if o.SkipNames == nil {
		o.SkipNames = DefaultSkipNames
	}
	if o.SkipPaths == nil {
		o.SkipPaths = DefaultSkipPaths
	}
	if o.Reader == nil {
		o.Reader = ReadDirLstat{}
	}
	if o.ProgressInterval <= 0 {
		o.ProgressInterval = DefaultProgressInterval
	}
	return nil
}

// walker holds the mutable state shared by the workers.
type walker struct {
	opts      Options
	root      string
	threshold int64
	skipNames map[string]bool
	skipPaths map[string]bool
	exempt    prefixMatcher
	sched     *scheduler
	links     *linkTable
	counts    counters
	done      <-chan struct{}

	// mu guards the three result slices. They are appended to rarely (errors
	// and skips only), so one mutex is cheaper than per-worker buffers.
	mu            sync.Mutex
	errs          []PathError
	skippedMounts []SkippedMount
	skipListed    []string

	vanished   atomic.Uint64
	incomplete atomic.Bool
}

// Walk traverses opts.Root and returns the finalized tree. It returns an error
// only for configuration problems: every per-path failure is recorded in the
// tree instead.
func Walk(ctx context.Context, opts Options) (*Tree, error) {
	if err := opts.applyDefaults(); err != nil {
		return nil, err
	}
	rootEntry, err := statRoot(opts.Reader, opts.Root)
	if err != nil {
		return nil, fmt.Errorf("walk: %s: %w", opts.Root, err)
	}
	if rootEntry.Kind != KindDir {
		return nil, fmt.Errorf("walk: %s is not a directory", opts.Root)
	}

	w := &walker{
		opts:      opts,
		root:      opts.Root,
		threshold: opts.SmallFileThreshold,
		skipNames: toSet(opts.SkipNames),
		skipPaths: toSet(opts.SkipPaths),
		exempt:    newPrefixMatcher(opts.Root, opts.ExemptPrefixes),
		sched:     newScheduler(),
		links:     newLinkTable(),
		done:      ctx.Done(),
	}

	root := &Node{
		Name:     opts.Root,
		Kind:     KindDir,
		Bytes:    rootEntry.Allocated(),
		Apparent: rootEntry.Size,
		Mtime:    rootEntry.MtimeSec,
		Flags:    entryFlags(&rootEntry),
	}
	tree := &Tree{Root: root, Opts: opts, Started: time.Now()}

	stopReporter := make(chan struct{})
	if opts.Events != nil {
		go reporter(opts.Events, &w.counts, tree.Started, opts.ProgressInterval, stopReporter)
	}

	// Wake parked workers when the context is cancelled.
	watcherDone := make(chan struct{})
	if w.done != nil {
		go func() {
			select {
			case <-w.done:
				w.incomplete.Store(true)
				w.sched.cancel()
			case <-watcherDone:
			}
		}()
	}

	w.sched.push(task{node: root, path: opts.Root, exempt: w.exempt.match(opts.Root)})
	var wg sync.WaitGroup
	wg.Add(opts.Parallelism)
	for range opts.Parallelism {
		go func() {
			defer wg.Done()
			for {
				t, ok := w.sched.pop()
				if !ok {
					return
				}
				w.processDir(t)
				w.sched.done()
			}
		}()
	}
	wg.Wait()
	close(watcherDone)
	close(stopReporter)

	w.finalize(tree)
	tree.Finished = time.Now()

	if opts.Events != nil {
		opts.Events <- DoneEvent{Tree: tree}
	}
	return tree, nil
}

// statRoot lstats the scan root, using the reader's own stat when it has one.
func statRoot(r DirReader, path string) (Entry, error) {
	if s, ok := r.(RootStater); ok {
		return s.Stat(path)
	}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return Entry{}, err
	}
	return Entry{
		Name:     filepath.Base(path),
		Kind:     kindFromStatMode(st.Mode),
		Ino:      st.Ino,
		Dev:      st.Dev,
		Nlink:    st.Nlink,
		Blocks:   st.Blocks,
		Size:     st.Size,
		MtimeSec: st.Mtim.Sec,
		Flags:    st.Flags,
	}, nil
}

// cancelled reports whether the context is done.
func (w *walker) cancelled() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// processDir lists one directory, creates its children and queues the
// subdirectories. It is the only writer of that node's Children and Small.
func (w *walker) processDir(t task) {
	if w.cancelled() {
		w.incomplete.Store(true)
		return
	}
	n := t.node
	w.counts.dirs.Add(1)
	w.counts.setCurrent(t.path)

	if t.path != w.root {
		// A mount point inside the root belongs to another volume: entering it
		// would double count (autofs /home, the OrbStack NFS export).
		if w.opts.Mounts != nil && w.opts.Mounts.IsMountPoint(t.path) {
			n.Flags |= FlagMountSkipped
			w.recordMount(t.path)
			w.counts.skipped.Add(1)
			return
		}
		if w.skipNames[n.Name] || w.skipPaths[t.path] {
			n.Flags |= FlagSkipListed
			w.mu.Lock()
			w.skipListed = append(w.skipListed, t.path)
			w.mu.Unlock()
			w.counts.skipped.Add(1)
			return
		}
	}
	// Listing a dataless directory asks the File Provider to materialize it,
	// which is exactly what this tool must never do.
	if n.Flags&FlagDataless != 0 {
		return
	}

	entries, err := w.opts.Reader.ReadDir(t.path)
	if err != nil {
		errno := Errno(err)
		n.Flags |= FlagUnreadable
		n.Errno = uint16(errno)
		w.recordErr(PathError{Path: t.path, Op: "readdir", Errno: errno, Class: classify(t.path, errno)})
		if errno == syscall.ENOENT {
			w.vanished.Add(1)
		}
	}
	if len(entries) == 0 {
		return
	}

	prefix := t.path
	if prefix != "/" {
		prefix += "/"
	}
	children := make([]*Node, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if e.Err != 0 {
			p := prefix + e.Name
			w.recordErr(PathError{Path: p, Op: "lstat", Errno: e.Err, Class: classify(p, e.Err)})
			if e.Err == syscall.ENOENT {
				w.vanished.Add(1)
			}
			continue
		}
		if e.Kind == KindDir {
			child := &Node{
				Name:     e.Name,
				Parent:   n,
				Kind:     KindDir,
				Bytes:    e.Allocated(), // zero on APFS; kept so other filesystems add up
				Apparent: e.Size,
				Mtime:    e.MtimeSec,
				Flags:    entryFlags(e),
			}
			if isBundleName(e.Name) {
				child.Flags |= FlagBundle
			}
			children = append(children, child)
			w.counts.bytes.Add(uint64(child.Bytes))
			childPath := prefix + e.Name
			w.sched.push(task{
				node:   child,
				path:   childPath,
				exempt: t.exempt || w.exempt.match(childPath),
			})
			continue
		}
		w.addLeaf(n, t, e, &children)
	}
	n.Children = children
}

// addLeaf records a non-directory entry, either as a Node or folded into the
// parent's Small aggregate.
func (w *walker) addLeaf(n *Node, t task, e *Entry, children *[]*Node) {
	alloc, app := e.Allocated(), e.Size
	big := alloc >= w.threshold || app >= w.threshold
	retain := big || t.exempt ||
		(w.opts.RetainLeaf != nil && w.opts.RetainLeaf(t.path, e))

	flags := entryFlags(e)
	var leaf *Node
	if retain {
		leaf = &Node{
			Name:   e.Name,
			Parent: n,
			Kind:   e.Kind,
			Mtime:  e.MtimeSec,
			Flags:  flags,
			Files:  1,
		}
		if !big {
			leaf.Flags |= FlagExempt
		}
		*children = append(*children, leaf)
	}
	w.counts.files.Add(1)

	// Hard links: nothing is attributed now. Finalize gives the bytes to the
	// lexicographically smallest path so the result does not depend on which
	// worker saw which link first.
	if e.Nlink > 1 && e.Kind == KindFile {
		first := w.links.add(inoKey{Dev: e.Dev, Ino: e.Ino}, e, linkRef{parent: n, name: e.Name, node: leaf})
		if first {
			w.counts.bytes.Add(uint64(alloc))
		} else {
			w.counts.links.Add(1)
		}
		if leaf == nil {
			n.Small.Files++
			if flags&FlagDataless != 0 {
				n.Small.Dataless++
			}
		}
		return
	}

	w.counts.bytes.Add(uint64(alloc))
	if leaf != nil {
		leaf.Bytes = alloc
		leaf.Apparent = app
		return
	}
	n.Small.Files++
	n.Small.Bytes += alloc
	n.Small.Apparent += app
	if flags&FlagDataless != 0 {
		n.Small.Dataless++
	}
}

// recordErr appends a per-path error and bumps the error counter.
func (w *walker) recordErr(pe PathError) {
	w.counts.errors.Add(1)
	w.mu.Lock()
	w.errs = append(w.errs, pe)
	w.mu.Unlock()
}

// recordMount appends a skipped mount, with its type and source when the
// mount table can describe it.
func (w *walker) recordMount(path string) {
	sm := SkippedMount{Path: path, Reason: "mount point inside the scan root"}
	if d, ok := w.opts.Mounts.(MountDescriber); ok {
		if fsType, from, found := d.MountInfo(path); found {
			sm.FSType, sm.From = fsType, from
		}
	}
	w.mu.Lock()
	w.skippedMounts = append(w.skippedMounts, sm)
	w.mu.Unlock()
}

// entryFlags maps st_flags to node flags.
func entryFlags(e *Entry) Flags {
	var f Flags
	if e.Flags&sfDataless != 0 {
		f |= FlagDataless
	}
	if e.Flags&ufCompressed != 0 {
		f |= FlagCompressed
	}
	return f
}

func toSet(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
