package cache

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/asamgx/storix/internal/walk"
)

// readChunk is the scratch size used while parsing the node arrays.
const readChunk = 64 << 10

// ReadMeta reads the header and the JSON metadata section and nothing else.
// It is the cheap call behind `cache list` and the freshness check.
//
// It does not verify the CRC, which would mean reading the whole file; use
// Read when the tree is wanted.
func ReadMeta(r io.ReaderAt) (Meta, error) {
	h, err := readHeader(r)
	if err != nil {
		return Meta{}, err
	}
	return readMetaSection(r, h)
}

// Read decodes a whole cache file: the metadata and the tree.
//
// It verifies magic, schema, section layout and the CRC32C trailer; anything
// wrong is reported as ErrIncompatible so the caller can rescan. The tree is
// rebuilt into one node arena and one child-pointer arena, and Tree.Nodes is
// re-derived by a preorder walk, so node IDs match a fresh walk of the same
// filesystem.
//
// Of walk.Options only Root, Parallelism and SmallFileThreshold are restored:
// the rest (readers, mount table, event channel) belong to a running walk, not
// to its result.
func Read(r io.ReaderAt, size int64) (Meta, *walk.Tree, error) {
	h, err := readHeader(r)
	if err != nil {
		return Meta{}, nil, err
	}
	if err := checkLayout(h, size); err != nil {
		return Meta{}, nil, err
	}

	// The sections are written back to back in a fixed order, so one
	// sequential pass reads them all and feeds the checksum at the same time.
	body := io.NewSectionReader(r, 0, size-trailerSize)
	cr := &crcReader{r: bufio.NewReaderSize(body, readChunk)}
	s := &scanner{r: cr, buf: make([]byte, readChunk)}

	if err := s.skip(int64(headerSize)); err != nil {
		return Meta{}, nil, err
	}
	metaBytes := make([]byte, h.metaLen)
	if err := s.full(metaBytes); err != nil {
		return Meta{}, nil, err
	}
	var meta Meta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return Meta{}, nil, fmt.Errorf("cache: %w: metadata: %w", ErrIncompatible, err)
	}
	if metaFlags(meta) != h.flags {
		return Meta{}, nil, fmt.Errorf("cache: %w: header flags %#x disagree with the metadata", ErrIncompatible, h.flags)
	}

	names, err := readNames(s, h)
	if err != nil {
		return Meta{}, nil, err
	}
	tree, err := readNodes(s, h, names)
	if err != nil {
		return Meta{}, nil, err
	}

	// Drain whatever is left before the trailer so the checksum covers the
	// whole body even if a future writer adds padding.
	if _, err := io.Copy(io.Discard, cr); err != nil {
		return Meta{}, nil, fmt.Errorf("cache: reading: %w", err)
	}
	var trailer [trailerSize]byte
	if _, err := r.ReadAt(trailer[:], size-trailerSize); err != nil {
		return Meta{}, nil, fmt.Errorf("cache: %w: reading checksum: %w", ErrIncompatible, err)
	}
	if want := binary.LittleEndian.Uint32(trailer[:]); want != cr.sum {
		return Meta{}, nil, fmt.Errorf("cache: %w: checksum %#08x, computed %#08x", ErrIncompatible, want, cr.sum)
	}

	applyMeta(tree, meta)
	return meta, tree, nil
}

func readHeader(r io.ReaderAt) (header, error) {
	b := make([]byte, headerSize)
	if _, err := r.ReadAt(b, 0); err != nil {
		return header{}, fmt.Errorf("cache: %w: reading header: %w", ErrIncompatible, err)
	}
	return parseHeader(b)
}

func readMetaSection(r io.ReaderAt, h header) (Meta, error) {
	b := make([]byte, h.metaLen)
	if _, err := r.ReadAt(b, int64(h.metaOff)); err != nil {
		return Meta{}, fmt.Errorf("cache: %w: reading metadata: %w", ErrIncompatible, err)
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, fmt.Errorf("cache: %w: metadata: %w", ErrIncompatible, err)
	}
	return m, nil
}

// checkLayout verifies that the sections tile the file exactly, which both
// catches truncation and lets the body be read in one sequential pass.
func checkLayout(h header, size int64) error {
	if size < headerSize+trailerSize {
		return fmt.Errorf("cache: %w: file is %d bytes", ErrIncompatible, size)
	}
	if h.nodeCount == 0 {
		return fmt.Errorf("cache: %w: no nodes", ErrIncompatible)
	}
	if h.dirCount > h.nodeCount {
		return fmt.Errorf("cache: %w: %d directories of %d nodes", ErrIncompatible, h.dirCount, h.nodeCount)
	}
	if h.namesOff != h.metaOff+h.metaLen || h.nodesOff != h.namesOff+h.namesLen {
		return fmt.Errorf("cache: %w: sections are not contiguous", ErrIncompatible)
	}
	if want := uint64(h.nodeCount) * nodeRecordSize; h.nodesLen != want {
		return fmt.Errorf("cache: %w: node section is %d bytes, want %d", ErrIncompatible, h.nodesLen, want)
	}
	if end := h.nodesOff + h.nodesLen; end != uint64(size-trailerSize) {
		return fmt.Errorf("cache: %w: sections end at %d, file body ends at %d", ErrIncompatible, end, size-trailerSize)
	}
	return nil
}

func readNames(s *scanner, h header) ([]string, error) {
	var count uint32
	if err := s.u32(&count); err != nil {
		return nil, err
	}
	// Each name costs at least its 2-byte length prefix.
	if uint64(count)*2+4 > h.namesLen {
		return nil, fmt.Errorf("cache: %w: %d names do not fit in %d bytes", ErrIncompatible, count, h.namesLen)
	}
	names := make([]string, count)
	for i := range names {
		var n uint16
		if err := s.u16(&n); err != nil {
			return nil, err
		}
		b := s.buf[:n]
		if err := s.full(b); err != nil {
			return nil, err
		}
		names[i] = string(b)
	}
	return names, nil
}

// readNodes rebuilds the tree from the struct-of-arrays section.
func readNodes(s *scanner, h header, names []string) (*walk.Tree, error) {
	n := int(h.nodeCount)
	arena := make([]walk.Node, n)
	parent := make([]uint32, n)
	childStart := make([]uint32, n)
	childCount := make([]uint32, n)
	le := binary.LittleEndian

	var bad error
	fail := func(format string, args ...any) {
		if bad == nil {
			bad = fmt.Errorf("cache: %w: "+format, append([]any{ErrIncompatible}, args...)...)
		}
	}

	if err := s.each(n, 4, func(i int, b []byte) {
		p := le.Uint32(b)
		if i == 0 {
			if p != 0 {
				fail("root has parent %d", p)
			}
			return
		}
		if uint64(p) >= uint64(i) {
			fail("node %d has parent %d, which is not earlier in the file", i, p)
			return
		}
		parent[i] = p
		arena[i].Parent = &arena[p]
	}); err != nil {
		return nil, err
	}
	if err := s.each(n, 4, func(i int, b []byte) {
		id := le.Uint32(b)
		if uint64(id) >= uint64(len(names)) {
			fail("node %d references name %d of %d", i, id, len(names))
			return
		}
		arena[i].Name = names[id]
	}); err != nil {
		return nil, err
	}
	if err := s.each(n, 1, func(i int, b []byte) {
		if b[0] > uint8(walk.KindOther) {
			fail("node %d has kind %d", i, b[0])
			return
		}
		arena[i].Kind = walk.Kind(b[0])
	}); err != nil {
		return nil, err
	}
	if err := s.each(n, 2, func(i int, b []byte) { arena[i].Flags = walk.Flags(le.Uint16(b)) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 2, func(i int, b []byte) { arena[i].Errno = le.Uint16(b) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 8, func(i int, b []byte) { arena[i].Bytes = int64(le.Uint64(b)) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 8, func(i int, b []byte) { arena[i].Apparent = int64(le.Uint64(b)) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 4, func(i int, b []byte) { arena[i].Files = le.Uint32(b) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 4, func(i int, b []byte) { arena[i].Dirs = le.Uint32(b) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 8, func(i int, b []byte) { arena[i].Mtime = int64(le.Uint64(b)) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 4, func(i int, b []byte) { childStart[i] = le.Uint32(b) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 4, func(i int, b []byte) { childCount[i] = le.Uint32(b) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 4, func(i int, b []byte) { arena[i].Small.Files = le.Uint32(b) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 4, func(i int, b []byte) { arena[i].Small.Dataless = le.Uint32(b) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 8, func(i int, b []byte) { arena[i].Small.Bytes = int64(le.Uint64(b)) }); err != nil {
		return nil, err
	}
	if err := s.each(n, 8, func(i int, b []byte) { arena[i].Small.Apparent = int64(le.Uint64(b)) }); err != nil {
		return nil, err
	}
	if bad != nil {
		return nil, bad
	}
	if err := linkChildren(arena, parent, childStart, childCount); err != nil {
		return nil, err
	}
	return &walk.Tree{Root: &arena[0], Nodes: preorder(&arena[0], n)}, nil
}

// linkChildren hands every directory its Children slice out of one shared
// allocation. The child blocks tile [1, n) exactly, which is verified here:
// every node lies inside its own parent's block, and the blocks are the right
// total length, so they cannot overlap or leave a gap.
func linkChildren(arena []walk.Node, parent, childStart, childCount []uint32) error {
	n := len(arena)
	total := uint64(0)
	for i := range arena {
		start, count := uint64(childStart[i]), uint64(childCount[i])
		if count == 0 {
			continue
		}
		if start < 1 || start+count > uint64(n) {
			return fmt.Errorf("cache: %w: node %d claims children [%d,%d) of %d", ErrIncompatible, i, start, start+count, n)
		}
		total += count
	}
	if total != uint64(n-1) {
		return fmt.Errorf("cache: %w: child blocks cover %d nodes, want %d", ErrIncompatible, total, n-1)
	}
	for i := 1; i < n; i++ {
		p := parent[i]
		start, count := uint64(childStart[p]), uint64(childCount[p])
		if uint64(i) < start || uint64(i) >= start+count {
			return fmt.Errorf("cache: %w: node %d is not in the child block of its parent %d", ErrIncompatible, i, p)
		}
	}

	kids := make([]*walk.Node, n-1)
	for i := 1; i < n; i++ {
		kids[i-1] = &arena[i]
	}
	for i := range arena {
		count := childCount[i]
		if count == 0 {
			continue
		}
		lo := childStart[i] - 1
		hi := lo + count
		arena[i].Children = kids[lo:hi:hi]
	}
	return nil
}

// preorder indexes the rebuilt tree depth-first, left to right, which is the
// order walk.Finalize produces, so the IDs match a fresh walk. It stamps
// Node.ID exactly as walk's own preorder does; cache_test asserts that a round
// trip leaves every id where it was.
func preorder(root *walk.Node, n int) []*walk.Node {
	nodes := make([]*walk.Node, 0, n)
	stack := make([]*walk.Node, 1, 64)
	stack[0] = root
	for len(stack) > 0 {
		i := len(stack) - 1
		cur := stack[i]
		stack = stack[:i]
		cur.ID = int32(len(nodes))
		nodes = append(nodes, cur)
		for j := len(cur.Children) - 1; j >= 0; j-- {
			stack = append(stack, cur.Children[j])
		}
	}
	return nodes
}

// applyMeta restores the tree fields that live in the metadata section.
func applyMeta(t *walk.Tree, m Meta) {
	t.Opts.Root = m.Root
	t.Opts.Parallelism = m.Parallelism
	t.Opts.SmallFileThreshold = m.SmallFileThreshold
	t.Started = m.Tree.Started
	t.Finished = m.Tree.Finished
	t.Incomplete = m.Incomplete
	t.Errors = m.Tree.Errors
	t.SkippedMounts = m.Tree.SkippedMounts
	t.SkipListed = m.Tree.SkipListed
	t.LinkGroups = m.Tree.LinkGroups
	t.LinkBytesSaved = m.Tree.LinkBytesSaved
	t.Vanished = m.Tree.Vanished
}

// scanner reads fixed-width records out of a sequential reader.
type scanner struct {
	r   io.Reader
	buf []byte
}

func (s *scanner) full(b []byte) error {
	if _, err := io.ReadFull(s.r, b); err != nil {
		return fmt.Errorf("cache: %w: %w", ErrIncompatible, err)
	}
	return nil
}

func (s *scanner) skip(n int64) error {
	if _, err := io.CopyN(io.Discard, s.r, n); err != nil {
		return fmt.Errorf("cache: %w: %w", ErrIncompatible, err)
	}
	return nil
}

func (s *scanner) u16(v *uint16) error {
	b := s.buf[:2]
	if err := s.full(b); err != nil {
		return err
	}
	*v = binary.LittleEndian.Uint16(b)
	return nil
}

func (s *scanner) u32(v *uint32) error {
	b := s.buf[:4]
	if err := s.full(b); err != nil {
		return err
	}
	*v = binary.LittleEndian.Uint32(b)
	return nil
}

// each reads count records of the given width, in chunks, calling fn with a
// view of each record.
func (s *scanner) each(count, width int, fn func(i int, b []byte)) error {
	perChunk := len(s.buf) / width
	if perChunk == 0 {
		return errors.New("cache: scratch buffer smaller than one record")
	}
	for i := 0; i < count; {
		k := min(perChunk, count-i)
		b := s.buf[:k*width]
		if err := s.full(b); err != nil {
			return err
		}
		for j := range k {
			fn(i+j, b[j*width:(j+1)*width])
		}
		i += k
	}
	return nil
}
