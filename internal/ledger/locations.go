package ledger

import (
	"fmt"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/walk"
)

// bucketLocations are the places that would hold a bucket's bytes, as display
// path patterns in which "*" is one path segment. They are the locations
// macOS guards most often, so a walk without Full Disk Access or root is
// likely to meet them as errors rather than as directories.
//
// The table is small on purpose. It does not have to describe the bucket,
// only the places whose unreadability means the walk cannot have seen the
// bucket's bytes: a bucket that walked nothing while one of these was
// unreadable is unknown, not empty.
var bucketLocations = map[classify.Bucket][]string{
	classify.BucketTrash: {
		"/Users/*/.Trash",
		"/.Trashes",
	},
	classify.BucketBackups: {
		"/Users/*/Library/Application Support/MobileSync",
	},
	classify.BucketPersonal: {
		"/Users/*/Desktop",
		"/Users/*/Documents",
		"/Users/*/Downloads",
		"/Users/*/Movies",
		"/Users/*/Music",
		"/Users/*/Pictures",
		"/Users/*/Library/Mail",
		"/Users/*/Library/Messages",
		"/Users/*/Library/Mobile Documents",
		"/Users/*/Library/Safari",
	},
}

// markUnreadableBuckets turns what the walk could not read into what the
// bucket rows claim.
//
// An unreadable path hides a bucket location when it is the location, lies
// inside it, or lies above it (the walk then never reached the location at
// all). A bucket with no walked bytes and a hidden location has no evidence
// for any figure, so it becomes unknown; one with walked bytes keeps them as
// a lower bound and says so. Either way the hidden bytes are part of the
// residual, which is where the note sends the reader.
func (l *Ledger) markUnreadableBuckets() {
	if len(l.Unreadable) == 0 {
		return
	}
	for _, b := range classify.Buckets() {
		patterns := bucketLocations[b]
		if len(patterns) == 0 {
			continue
		}
		var hidden []walk.PathError
		for _, e := range l.Unreadable {
			if e.Class == walk.ErrVanished {
				continue
			}
			if hidesAny(mac.DisplayPath(e.Path), patterns) {
				hidden = append(hidden, e)
			}
		}
		if len(hidden) == 0 {
			continue
		}
		row := l.bucket(b)
		why := unreadableReason(hidden)
		if row.Bytes == 0 {
			row.Known = false
			row.Note = joinNote(fmt.Sprintf("could not be read %s; its content is counted in %s",
				why, classify.BucketUnaccounted.Label()), row.Note)
			continue
		}
		n := len(hidden)
		row.Note = joinNote(fmt.Sprintf("partial: %d %s unreadable %s; the rest is counted in %s",
			n, plural(n, "location", "locations"), why, classify.BucketUnaccounted.Label()), row.Note)
	}
}

// hidesAny reports whether an unreadable display path hides any of the
// patterns: it matches one, is inside one, or is above one.
func hidesAny(path string, patterns []string) bool {
	segs := splitPath(path)
	for _, p := range patterns {
		pat := splitPath(p)
		n := min(len(segs), len(pat))
		ok := true
		for i := range n {
			if pat[i] != "*" && pat[i] != segs[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// splitPath is a display path's segments, the root being none.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// unreadableReason says what would open the hidden locations, in the words
// the hints use: Full Disk Access for privacy protection, sudo for ownership.
func unreadableReason(errs []walk.PathError) string {
	var tcc, perm, protected bool
	for _, e := range errs {
		switch e.Class {
		case walk.ErrTCC:
			tcc = true
		case walk.ErrPermission:
			perm = true
		case walk.ErrProtected:
			protected = true
		}
	}
	var fixes []string
	if tcc {
		fixes = append(fixes, "Full Disk Access")
	}
	if perm {
		fixes = append(fixes, "sudo")
	}
	switch {
	case len(fixes) > 0 && protected:
		return "without " + strings.Join(fixes, " or ") + ", and part is protected by the system"
	case len(fixes) > 0:
		return "without " + strings.Join(fixes, " or ")
	case protected:
		return "even with Full Disk Access: it is protected by the system"
	default:
		return "by this scan"
	}
}

// joinNote puts a new sentence before whatever the row already said.
func joinNote(note, existing string) string {
	if existing == "" {
		return note
	}
	return note + "; " + existing
}
