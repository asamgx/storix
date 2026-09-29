package ledger

import (
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// unreadable is a walk error at a display path, the way the walk records one
// on the data volume.
func unreadable(display string, c walk.ErrClass) walk.PathError {
	return walk.PathError{Path: mac.DataRoot + display, Op: "readdir", Class: c}
}

// unreadableLedger is a classified ledger over a tree whose walk met errs.
// Personal files hold bytes; Trash and Backups hold none.
func unreadableLedger(errs ...walk.PathError) *Ledger {
	const scanned = 10_000_000_000
	tr := tree(mac.DataRoot, scanned)
	tr.Errors = errs
	class := classified(scanned, map[classify.Bucket]int64{
		classify.BucketPersonal: 4_000_000_000,
	})
	return BuildClassified(fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0)), tr, units.Decimal, class)
}

// TestUnreadableTrashAndBackupsAreUnknown is the verification finding: with
// the Trash and MobileSync unreadable without Full Disk Access, the walk saw
// nothing there, and "0 bytes" would be a claim the scan has no evidence
// for. The rows say unknown and point at where the bytes went instead.
func TestUnreadableTrashAndBackupsAreUnknown(t *testing.T) {
	l := unreadableLedger(
		unreadable("/Users/me/.Trash", walk.ErrTCC),
		unreadable("/Users/me/Library/Application Support/MobileSync", walk.ErrTCC),
	)
	for _, b := range []classify.Bucket{classify.BucketTrash, classify.BucketBackups} {
		row := l.bucket(b)
		if row.Known {
			t.Errorf("%s: known with %d bytes although its location could not be read", b, row.Bytes)
		}
		if !strings.Contains(row.Note, "Full Disk Access") || !strings.Contains(row.Note, "Unreadable / unaccounted") {
			t.Errorf("%s: note = %q, want the reason and where the bytes are counted", b, row.Note)
		}
	}
}

// TestAnUnreadableAncestorHidesTheLocation: when a parent of the location
// could not be read, the walk never reached the location at all, which is
// the same absence of evidence.
func TestAnUnreadableAncestorHidesTheLocation(t *testing.T) {
	l := unreadableLedger(unreadable("/Users/other/Library", walk.ErrPermission))
	if b := l.bucket(classify.BucketBackups); b.Known {
		t.Errorf("backups known although /Users/other/Library was never read: %+v", b)
	} else if !strings.Contains(b.Note, "sudo") {
		t.Errorf("backups note = %q, want the sudo fix for a permission error", b.Note)
	}
	if b := l.bucket(classify.BucketTrash); !b.Known {
		t.Errorf("trash unknown although no trash location is under /Users/other/Library: %q", b.Note)
	}
}

// TestPartiallyUnreadableBucketKeepsItsBytes: a bucket with walked bytes has
// evidence for at least those, so it stays known and the note says the
// figure is a lower bound.
func TestPartiallyUnreadableBucketKeepsItsBytes(t *testing.T) {
	l := unreadableLedger(
		unreadable("/Users/me/Library/Mail", walk.ErrTCC),
		unreadable("/Users/me/Library/Messages", walk.ErrTCC),
	)
	p := l.bucket(classify.BucketPersonal)
	if !p.Known || p.Bytes != 4_000_000_000 {
		t.Fatalf("personal = %+v, want known with its walked bytes", p)
	}
	if !strings.HasPrefix(p.Note, "partial: 2 locations unreadable") {
		t.Errorf("personal note = %q, want it to say the figure is partial", p.Note)
	}
}

// TestReadableEmptyBucketsStayKnown: the rule only fires on evidence of an
// unreadable location. An error elsewhere, a vanished entry, or no error at
// all leaves an empty Trash an honest zero.
func TestReadableEmptyBucketsStayKnown(t *testing.T) {
	for name, errs := range map[string][]walk.PathError{
		"no errors":      nil,
		"elsewhere":      {unreadable("/Library/Caches/com.apple.aned", walk.ErrTCC)},
		"sibling prefix": {unreadable("/Users/me/.Trashy", walk.ErrTCC)},
		"vanished":       {unreadable("/Users/me/.Trash", walk.ErrVanished)},
	} {
		l := unreadableLedger(errs...)
		for _, b := range []classify.Bucket{classify.BucketTrash, classify.BucketBackups} {
			if row := l.bucket(b); !row.Known || row.Note != "" {
				t.Errorf("%s: %s = %+v, want a known zero with no note", name, b, row)
			}
		}
		if p := l.bucket(classify.BucketPersonal); p.Note != "" {
			t.Errorf("%s: personal note = %q, want none", name, p.Note)
		}
	}
}

// TestUnknownBucketFromARealWalk runs the rule over a walked fixture whose
// Trash is present but unreadable, the shape a machine without Full Disk
// Access has.
func TestUnknownBucketFromARealWalk(t *testing.T) {
	root := &walk.Node{Name: mac.DataRoot, Kind: walk.KindDir}
	users := &walk.Node{Name: "Users", Kind: walk.KindDir, Parent: root}
	home := &walk.Node{Name: "u", Kind: walk.KindDir, Parent: users}
	trash := &walk.Node{Name: ".Trash", Kind: walk.KindDir, Parent: home, Flags: walk.FlagUnreadable}
	docs := &walk.Node{Name: "Documents", Kind: walk.KindDir, Parent: home, Bytes: 5_000, Files: 1}
	root.Children = []*walk.Node{users}
	users.Children = []*walk.Node{home}
	home.Children = []*walk.Node{trash, docs}
	for _, n := range []*walk.Node{home, users, root} {
		for _, c := range n.Children {
			n.Bytes += c.Bytes
			n.Files += c.Files
		}
	}
	tr := &walk.Tree{
		Root:     root,
		Nodes:    []*walk.Node{root, users, home, trash, docs},
		Errors:   []walk.PathError{{Path: trash.Path(), Op: "readdir", Class: walk.ErrTCC}},
		Started:  before,
		Finished: after,
	}

	e, err := classify.New([]classify.Rule{
		{ID: "trash.user.home", Match: "~/.Trash", Bucket: classify.BucketTrash,
			Category: "Trash", Owner: "Trash", Reclaim: classify.ToolManaged},
		{ID: "personal.documents", Match: "~/Documents", Bucket: classify.BucketPersonal,
			Category: "Documents", Owner: "Documents", Reclaim: classify.UserData},
	}, classify.Context{Home: "/Users/u", CodeRoots: []string{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	class := e.Run(tr, nil)
	l := BuildClassified(fakeFacts(mac.DataRoot, dataUsed, dataUsed, known(0)), tr, units.Decimal, class)
	if b := l.bucket(classify.BucketTrash); b.Known {
		t.Errorf("trash = %+v, want unknown", b)
	}
	if b := l.bucket(classify.BucketPersonal); !b.Known || b.Bytes == 0 {
		t.Errorf("personal = %+v, want known with the documents", b)
	}
}
