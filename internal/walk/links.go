package walk

import "sync"

// inoKey identifies an inode. st_dev is not usable as a mount boundary on APFS
// volume groups, but it is still the right key half for inode identity.
type inoKey struct {
	Dev int32
	Ino uint64
}

// linkRef is one path that points at a hard-linked inode. node is nil when the
// leaf was aggregated into parent.Small instead of being retained.
type linkRef struct {
	parent *Node
	name   string
	node   *Node
}

// path returns the full scan path of the reference.
func (r linkRef) path() string { return r.parent.Path() + "/" + r.name }

// linkGroup collects every reference to one hard-linked inode seen in the scan.
type linkGroup struct {
	bytes    int64
	apparent int64
	nlink    uint16
	dataless bool
	refs     []linkRef
}

const linkShards = 16

// linkTable is a sharded map of hard-linked inodes. It exists only for the
// duration of the walk: Finalize resolves it and drops it, so its memory does
// not survive into the tree.
type linkTable struct {
	shards [linkShards]struct {
		mu sync.Mutex
		m  map[inoKey]*linkGroup
		_  [48]byte // pad each shard to a cache line
	}
}

func newLinkTable() *linkTable {
	t := &linkTable{}
	for i := range t.shards {
		t.shards[i].m = make(map[inoKey]*linkGroup)
	}
	return t
}

// add records a reference to a hard-linked inode and reports whether this was
// the first sighting of it, which is when the progress counter charges its bytes.
func (t *linkTable) add(key inoKey, e *Entry, ref linkRef) (first bool) {
	sh := &t.shards[key.Ino%linkShards]
	sh.mu.Lock()
	g, ok := sh.m[key]
	if !ok {
		g = &linkGroup{
			bytes:    e.Allocated(),
			apparent: e.Size,
			nlink:    e.Nlink,
			dataless: e.Flags&sfDataless != 0,
		}
		sh.m[key] = g
		first = true
	}
	g.refs = append(g.refs, ref)
	sh.mu.Unlock()
	return first
}

// resolve attributes each link group's bytes to the lexicographically smallest
// of its paths and marks the rest as aliases with zero bytes. The rule makes
// the result independent of scheduling, which keeps diffs and golden tests
// stable. Groups whose links are not all inside the scan root still attribute
// to the smallest path seen.
//
// resolve runs single-threaded inside Finalize.
func (t *linkTable) resolve() (groups, bytesSaved uint64) {
	for i := range t.shards {
		sh := &t.shards[i]
		for _, g := range sh.m {
			if len(g.refs) == 0 {
				continue
			}
			groups++
			owner := 0
			ownerPath := g.refs[0].path()
			for j := 1; j < len(g.refs); j++ {
				if p := g.refs[j].path(); p < ownerPath {
					owner, ownerPath = j, p
				}
			}
			for j, ref := range g.refs {
				if j == owner {
					if ref.node != nil {
						ref.node.Bytes = g.bytes
						ref.node.Apparent = g.apparent
						ref.node.Flags |= FlagLinkOwner
					} else {
						ref.parent.Small.Bytes += g.bytes
						ref.parent.Small.Apparent += g.apparent
					}
					continue
				}
				bytesSaved += uint64(g.bytes)
				if ref.node != nil {
					ref.node.Bytes = 0
					ref.node.Apparent = 0
					ref.node.Flags |= FlagLinkAlias
				}
			}
		}
		sh.m = nil
	}
	return groups, bytesSaved
}
