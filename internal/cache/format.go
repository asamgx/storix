// Package cache stores a finished scan on disk and reads it back.
//
// The file format is flat and versioned: a fixed header, a small JSON metadata
// section, a deduplicated string table and a struct-of-arrays node section,
// closed by a CRC32C trailer. Nothing is reflected over at load time, so a
// 1.3 M node tree comes back in well under a second, which reflection-driven
// encodings (gob) do not manage (see bench_test.go).
//
// Any header, schema or checksum mismatch is reported as ErrIncompatible: the
// caller is expected to rescan rather than to repair.
package cache

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"time"

	"github.com/asamgx/storix/internal/walk"
)

// SchemaVersion is the on-disk format version. A file written by a different
// version is not read.
const SchemaVersion = 1

// ErrIncompatible marks a cache file this build cannot use: wrong magic, wrong
// schema, truncation, or a failed checksum. Errors wrap it with the detail.
var ErrIncompatible = errors.New("incompatible scan cache")

// magic identifies a storix scan cache.
const magic = "STRX"

// Layout constants.
//
// The header carries the field list fixed in the plan (magic, schema, flags,
// nodeCount, dirCount, three offset/length pairs and a 24-byte version), which
// needs 92 bytes; it is padded to 96 so every u64 is 8-byte aligned. (The plan
// said "64-byte header" alongside a field list that cannot fit in 64 bytes.)
const (
	headerSize  = 96
	trailerSize = 4
	versionLen  = 24
	// nodeRecordSize is the per-node cost of the struct-of-arrays section.
	nodeRecordSize = 4 + 4 + 1 + 2 + 2 + 8 + 8 + 4 + 4 + 8 + 4 + 4 + 4 + 4 + 8 + 8
	// maxNameLen is the longest name the string table can hold; macOS caps a
	// path component at 255 bytes, and the root's full path is far shorter.
	maxNameLen = 1<<16 - 1
)

// Header flag bits.
const (
	flagIncomplete uint32 = 1 << 0
	flagSudo       uint32 = 1 << 1
)

// crcTable is CRC32C (Castagnoli), which has a hardware implementation on
// arm64 and x86-64.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Meta is the JSON section of a cache file: everything about the scan that is
// not the tree itself.
//
// Sections carries pre-encoded JSON the scan orchestrator owns ("facts",
// "ledger", "errors", "skipped"), so this package depends on neither
// internal/volume nor internal/ledger.
type Meta struct {
	Schema             int                        `json:"schema"`
	Storix             string                     `json:"storix"`
	Written            time.Time                  `json:"written"`
	Root               string                     `json:"root"`
	Roots              []string                   `json:"roots,omitempty"`
	Incomplete         bool                       `json:"incomplete"`
	Sudo               bool                       `json:"sudo"`
	Parallelism        int                        `json:"parallelism"`
	SmallFileThreshold int64                      `json:"small_file_threshold"`
	Tree               TreeMeta                   `json:"tree"`
	Sections           map[string]json.RawMessage `json:"sections,omitempty"`
}

// TreeMeta holds the walk.Tree fields that live outside the node arrays. Write
// fills it from the tree, so a cache file round-trips a tree on its own
// without help from whatever the caller put in Sections.
type TreeMeta struct {
	Started        time.Time           `json:"started"`
	Finished       time.Time           `json:"finished"`
	LinkGroups     uint64              `json:"link_groups"`
	LinkBytesSaved uint64              `json:"link_bytes_saved"`
	Vanished       uint64              `json:"vanished"`
	Errors         []walk.PathError    `json:"errors,omitempty"`
	SkippedMounts  []walk.SkippedMount `json:"skipped_mounts,omitempty"`
	SkipListed     []string            `json:"skip_listed,omitempty"`
}

// header is the fixed-size preamble.
type header struct {
	schema    uint32
	flags     uint32
	nodeCount uint32
	dirCount  uint32
	metaOff   uint64
	metaLen   uint64
	namesOff  uint64
	namesLen  uint64
	nodesOff  uint64
	nodesLen  uint64
	version   string
}

func (h header) marshal() []byte {
	b := make([]byte, headerSize)
	copy(b, magic)
	le := binary.LittleEndian
	le.PutUint32(b[4:], h.schema)
	le.PutUint32(b[8:], h.flags)
	le.PutUint32(b[12:], h.nodeCount)
	le.PutUint32(b[16:], h.dirCount)
	// b[20:24] reserved, zero.
	le.PutUint64(b[24:], h.metaOff)
	le.PutUint64(b[32:], h.metaLen)
	le.PutUint64(b[40:], h.namesOff)
	le.PutUint64(b[48:], h.namesLen)
	le.PutUint64(b[56:], h.nodesOff)
	le.PutUint64(b[64:], h.nodesLen)
	v := h.version
	if len(v) > versionLen {
		v = v[:versionLen]
	}
	copy(b[72:], v)
	return b
}

// parseHeader validates the magic and schema and returns the header fields.
func parseHeader(b []byte) (header, error) {
	if len(b) < headerSize {
		return header{}, fmt.Errorf("cache: %w: header is %d bytes, want %d", ErrIncompatible, len(b), headerSize)
	}
	if string(b[:4]) != magic {
		return header{}, fmt.Errorf("cache: %w: bad magic %q", ErrIncompatible, b[:4])
	}
	le := binary.LittleEndian
	h := header{
		schema:    le.Uint32(b[4:]),
		flags:     le.Uint32(b[8:]),
		nodeCount: le.Uint32(b[12:]),
		dirCount:  le.Uint32(b[16:]),
		metaOff:   le.Uint64(b[24:]),
		metaLen:   le.Uint64(b[32:]),
		namesOff:  le.Uint64(b[40:]),
		namesLen:  le.Uint64(b[48:]),
		nodesOff:  le.Uint64(b[56:]),
		nodesLen:  le.Uint64(b[64:]),
		version:   trimNUL(b[72 : 72+versionLen]),
	}
	if h.schema != SchemaVersion {
		return header{}, fmt.Errorf("cache: %w: schema %d, this build reads %d", ErrIncompatible, h.schema, SchemaVersion)
	}
	if h.metaOff != headerSize {
		return header{}, fmt.Errorf("cache: %w: metadata at offset %d, want %d", ErrIncompatible, h.metaOff, headerSize)
	}
	return h, nil
}

func trimNUL(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// metaFlags derives the header flag word from the metadata.
func metaFlags(m Meta) uint32 {
	var f uint32
	if m.Incomplete {
		f |= flagIncomplete
	}
	if m.Sudo {
		f |= flagSudo
	}
	return f
}

// crcWriter accumulates a CRC32C over everything written through it.
type crcWriter struct {
	w   io.Writer
	sum uint32
}

func (c *crcWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.sum = crc32.Update(c.sum, crcTable, p[:n])
	return n, err
}

// crcReader accumulates a CRC32C over everything read through it.
type crcReader struct {
	r   io.Reader
	sum uint32
}

func (c *crcReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.sum = crc32.Update(c.sum, crcTable, p[:n])
	return n, err
}
