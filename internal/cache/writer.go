package cache

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/asamgx/storix/internal/walk"
)

// emitChunk is how much the node section buffers before it writes.
const emitChunk = 64 << 10

// Write encodes a finished tree and its metadata.
//
// It makes two passes over the tree: one to lay out node indices and intern
// names, one per field array to emit. Nothing bigger than the index arrays
// (20 B/node) and the name table is held in memory; the node section itself is
// streamed in 64 KiB chunks.
//
// Write fills the derived parts of meta (schema, timestamps, tree scalars,
// walk options) so the file round-trips a tree on its own.
func Write(w io.Writer, meta Meta, t *walk.Tree) error {
	if t == nil || t.Root == nil {
		return errors.New("cache: nil tree")
	}
	lay, err := buildLayout(t.Root)
	if err != nil {
		return err
	}
	meta = completeMeta(meta, t)
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("cache: encoding metadata: %w", err)
	}

	h := header{
		schema:    SchemaVersion,
		flags:     metaFlags(meta),
		nodeCount: uint32(len(lay.order)),
		dirCount:  lay.dirCount,
		version:   meta.Storix,
		metaOff:   headerSize,
		metaLen:   uint64(len(metaJSON)),
	}
	h.namesOff = h.metaOff + h.metaLen
	h.namesLen = lay.namesLen()
	h.nodesOff = h.namesOff + h.namesLen
	h.nodesLen = uint64(len(lay.order)) * nodeRecordSize

	bw := bufio.NewWriterSize(w, emitChunk)
	cw := &crcWriter{w: bw}
	e := newEmitter(cw)
	e.bytes(h.marshal())
	e.bytes(metaJSON)
	lay.emitNames(e)
	lay.emitNodes(e)
	if err := e.flush(); err != nil {
		return fmt.Errorf("cache: writing: %w", err)
	}

	var trailer [trailerSize]byte
	leUint32(trailer[:], cw.sum)
	if _, err := bw.Write(trailer[:]); err != nil {
		return fmt.Errorf("cache: writing checksum: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("cache: flushing: %w", err)
	}
	return nil
}

// completeMeta fills the metadata fields that are derived from the tree.
func completeMeta(m Meta, t *walk.Tree) Meta {
	m.Schema = SchemaVersion
	if m.Written.IsZero() {
		m.Written = time.Now()
	}
	if m.Root == "" {
		m.Root = t.Root.Name
	}
	if len(m.Roots) == 0 {
		m.Roots = []string{m.Root}
	}
	if m.Parallelism == 0 {
		m.Parallelism = t.Opts.Parallelism
	}
	if m.SmallFileThreshold == 0 {
		m.SmallFileThreshold = t.Opts.SmallFileThreshold
	}
	// A cancelled walk always produces an incomplete cache, whatever the
	// caller passed.
	m.Incomplete = m.Incomplete || t.Incomplete
	m.Tree = TreeMeta{
		Started:        t.Started,
		Finished:       t.Finished,
		LinkGroups:     t.LinkGroups,
		LinkBytesSaved: t.LinkBytesSaved,
		Vanished:       t.Vanished,
		Errors:         t.Errors,
		SkippedMounts:  t.SkippedMounts,
		SkipListed:     t.SkipListed,
	}
	return m
}

// layout assigns every node its file index and interns the names.
//
// Index order is "directory block" order: the root is 0, then the children of
// each directory, taken in preorder, form one contiguous run. A node's parent
// therefore always has a smaller index than the node itself, and the runs tile
// [1, nodeCount) exactly, which is what lets the reader hand out child slices
// from a single allocation.
type layout struct {
	order      []*walk.Node
	parent     []uint32
	childStart []uint32
	childCount []uint32
	nameID     []uint32
	names      []string
	dirCount   uint32
}

func buildLayout(root *walk.Node) (*layout, error) {
	l := &layout{}
	ids := make(map[string]uint32)
	intern := func(s string) uint32 {
		if id, ok := ids[s]; ok {
			return id
		}
		id := uint32(len(l.names))
		ids[s] = id
		l.names = append(l.names, s)
		return id
	}
	add := func(n *walk.Node, parent uint32) {
		l.order = append(l.order, n)
		l.parent = append(l.parent, parent)
		l.childStart = append(l.childStart, 0)
		l.childCount = append(l.childCount, 0)
		l.nameID = append(l.nameID, intern(n.Name))
		if n.IsDir() {
			l.dirCount++
		}
	}

	add(root, 0)
	// LIFO of directory indices: popping gives the directories in preorder,
	// so the blocks land in the order the format specifies.
	stack := []uint32{0}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		kids := l.order[i].Children
		if len(kids) == 0 {
			continue
		}
		if uint64(len(l.order))+uint64(len(kids)) > math.MaxUint32 {
			return nil, errors.New("cache: tree has more than 2^32-1 nodes")
		}
		start := uint32(len(l.order))
		l.childStart[i] = start
		l.childCount[i] = uint32(len(kids))
		for _, c := range kids {
			add(c, i)
		}
		for j := len(kids) - 1; j >= 0; j-- {
			if kids[j].IsDir() {
				stack = append(stack, start+uint32(j))
			}
		}
	}
	for _, n := range l.names {
		if len(n) > maxNameLen {
			return nil, fmt.Errorf("cache: name of %d bytes exceeds the %d byte limit", len(n), maxNameLen)
		}
	}
	return l, nil
}

func (l *layout) namesLen() uint64 {
	total := uint64(4)
	for _, n := range l.names {
		total += 2 + uint64(len(n))
	}
	return total
}

func (l *layout) emitNames(e *emitter) {
	e.u32(uint32(len(l.names)))
	for _, n := range l.names {
		e.u16(uint16(len(n)))
		e.bytes([]byte(n))
	}
}

// emitNodes writes the struct-of-arrays section, one field at a time.
func (l *layout) emitNodes(e *emitter) {
	for _, v := range l.parent {
		e.u32(v)
	}
	for _, v := range l.nameID {
		e.u32(v)
	}
	for _, n := range l.order {
		e.u8(uint8(n.Kind))
	}
	for _, n := range l.order {
		e.u16(uint16(n.Flags))
	}
	for _, n := range l.order {
		e.u16(n.Errno)
	}
	for _, n := range l.order {
		e.i64(n.Bytes)
	}
	for _, n := range l.order {
		e.i64(n.Apparent)
	}
	for _, n := range l.order {
		e.u32(n.Files)
	}
	for _, n := range l.order {
		e.u32(n.Dirs)
	}
	for _, n := range l.order {
		e.i64(n.Mtime)
	}
	for _, v := range l.childStart {
		e.u32(v)
	}
	for _, v := range l.childCount {
		e.u32(v)
	}
	for _, n := range l.order {
		e.u32(n.Small.Files)
	}
	for _, n := range l.order {
		e.u32(n.Small.Dataless)
	}
	for _, n := range l.order {
		e.i64(n.Small.Bytes)
	}
	for _, n := range l.order {
		e.i64(n.Small.Apparent)
	}
}

// emitter buffers little-endian values and writes them in chunks. It latches
// the first write error; callers check it once, at flush.
type emitter struct {
	w   io.Writer
	buf []byte
	err error
}

func newEmitter(w io.Writer) *emitter {
	return &emitter{w: w, buf: make([]byte, 0, emitChunk)}
}

func (e *emitter) reserve(n int) {
	if len(e.buf)+n > cap(e.buf) {
		_ = e.flush()
	}
}

func (e *emitter) u8(v uint8) {
	e.reserve(1)
	e.buf = append(e.buf, v)
}

func (e *emitter) u16(v uint16) {
	e.reserve(2)
	e.buf = append(e.buf, byte(v), byte(v>>8))
}

func (e *emitter) u32(v uint32) {
	e.reserve(4)
	e.buf = append(e.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (e *emitter) i64(v int64) {
	u := uint64(v)
	e.reserve(8)
	e.buf = append(e.buf,
		byte(u), byte(u>>8), byte(u>>16), byte(u>>24),
		byte(u>>32), byte(u>>40), byte(u>>48), byte(u>>56))
}

func (e *emitter) bytes(b []byte) {
	if e.err != nil {
		return
	}
	if len(b) > cap(e.buf) {
		if err := e.flush(); err != nil {
			return
		}
		_, e.err = e.w.Write(b)
		return
	}
	e.reserve(len(b))
	e.buf = append(e.buf, b...)
}

func (e *emitter) flush() error {
	if e.err != nil {
		return e.err
	}
	if len(e.buf) > 0 {
		_, e.err = e.w.Write(e.buf)
		e.buf = e.buf[:0]
	}
	return e.err
}

func leUint32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
