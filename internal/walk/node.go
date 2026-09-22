// Package walk traverses a macOS filesystem subtree and builds an in-memory
// size tree.
//
// The walker is strictly read-only and never opens a file: sizes come from
// lstat alone, symlinks are never followed, mount points other than the scan
// root are never entered, and a directory carrying SF_DATALESS is never listed
// (listing one asks the File Provider to materialize it). walk/lint_test.go
// enforces the no-open rule mechanically.
package walk

import (
	"syscall"

	"github.com/asamgx/storix/internal/mac"
)

// Kind is the type of a filesystem object, as reported by lstat.
type Kind uint8

const (
	KindDir Kind = iota
	KindFile
	KindSymlink
	KindOther
)

func (k Kind) String() string {
	switch k {
	case KindDir:
		return "dir"
	case KindFile:
		return "file"
	case KindSymlink:
		return "symlink"
	default:
		return "other"
	}
}

// Flags records per-node facts discovered during the walk.
type Flags uint16

const (
	// FlagUnreadable marks a directory whose listing failed; Errno is set.
	FlagUnreadable Flags = 1 << iota
	// FlagPartial marks a directory with an unreadable, skipped or dataless
	// descendant, so its totals are a lower bound.
	FlagPartial
	// FlagDataless marks an entry with SF_DATALESS: its content lives in the
	// cloud and occupies no local blocks.
	FlagDataless
	// FlagCompressed marks an entry with UF_COMPRESSED (HFS+/APFS transparent
	// compression): allocated bytes are real, apparent bytes are the logical size.
	FlagCompressed
	// FlagBundle marks a directory macOS presents as a single object (".app", …).
	FlagBundle
	// FlagMountSkipped marks a mount point inside the scan root that was not entered.
	FlagMountSkipped
	// FlagSkipListed marks a directory excluded by the skip list.
	FlagSkipListed
	// FlagLinkOwner marks the hard link that carries the bytes of its link group.
	FlagLinkOwner
	// FlagLinkAlias marks a hard link whose bytes are attributed to another path.
	FlagLinkAlias
	// FlagExempt marks a leaf retained below the aggregation threshold.
	FlagExempt
)

// Small is the aggregate of a directory's own leaves that fell below the
// retention threshold. Their bytes are part of the directory's totals but they
// have no Node of their own.
type Small struct {
	Files    uint32 // aggregated leaves (files and symlinks), including link aliases
	Dataless uint32 // how many of them carry SF_DATALESS
	Bytes    int64  // allocated bytes (st_blocks*512)
	Apparent int64  // logical bytes (st_size)
}

// Node is one retained entry of the tree. Leaves below the aggregation
// threshold are folded into their parent's Small instead of getting a Node.
//
// The layout is deliberately lean (112 B on arm64) because a full scan retains
// on the order of a million of them; walk/mem_test.go guards the budget.
type Node struct {
	Name     string
	Parent   *Node
	Children []*Node // directories only, sorted by Name; nil for leaves
	Bytes    int64   // allocated; for directories the subtree total after Finalize
	Apparent int64   // logical; for directories the subtree total after Finalize
	Files    uint32  // subtree leaves including aggregated ones; 1 for a leaf
	Dirs     uint32  // subtree directories, excluding this one
	Mtime    int64   // unix seconds
	Kind     Kind
	Flags    Flags
	Errno    uint16 // syscall.Errno when FlagUnreadable is set
	Small    Small
}

// IsDir reports whether the node is a directory.
func (n *Node) IsDir() bool { return n.Kind == KindDir }

// Has reports whether every flag in f is set.
func (n *Node) Has(f Flags) bool { return n.Flags&f == f }

// Path returns the absolute scan path, rebuilt from the parent chain.
func (n *Node) Path() string {
	if n.Parent == nil {
		if n.Name == "" {
			return "/"
		}
		return n.Name
	}
	total := 0
	for p := n; p != nil; p = p.Parent {
		total += len(p.Name)
		if p.Parent != nil {
			total++
		}
	}
	buf := make([]byte, total)
	i := total
	for p := n; p != nil; p = p.Parent {
		i -= len(p.Name)
		copy(buf[i:], p.Name)
		if p.Parent != nil {
			i--
			buf[i] = '/'
		}
	}
	return string(buf)
}

// Display returns the path as a user sees it, with the data volume prefix
// stripped.
func (n *Node) Display() string { return mac.DisplayPath(n.Path()) }

// ErrClass groups per-path errors by what the user can do about them.
type ErrClass uint8

const (
	// ErrOther is any errno without a more specific class.
	ErrOther ErrClass = iota
	// ErrPermission is EACCES: ownership or mode, usually fixable with sudo.
	ErrPermission
	// ErrTCC is EPERM: privacy protection, fixable with Full Disk Access.
	ErrTCC
	// ErrProtected is EPERM on a data vault, which not even Full Disk Access opens.
	ErrProtected
	// ErrVanished is ENOENT: the entry disappeared during the walk.
	ErrVanished
)

func (c ErrClass) String() string {
	switch c {
	case ErrPermission:
		return "permission"
	case ErrTCC:
		return "tcc"
	case ErrProtected:
		return "protected"
	case ErrVanished:
		return "vanished"
	default:
		return "other"
	}
}

// PathError is a per-path failure. Errors are data: a walk reports them and
// keeps going.
type PathError struct {
	Path  string
	Op    string // "readdir" or "lstat"
	Errno syscall.Errno
	Class ErrClass
}

func (e PathError) Error() string {
	return e.Op + " " + e.Path + ": " + e.Errno.Error()
}

// SkippedMount is a mount point inside the scan root that was not entered.
type SkippedMount struct {
	Path   string
	FSType string
	From   string
	Reason string
}
