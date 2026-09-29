// Package classify puts every scanned byte into one of the twelve ledger
// buckets of docs/02, with a reclaimability tag and provenance.
//
// The package holds three things: a declarative rule model over display paths
// (internal/classify/catalog supplies the tables), an engine that resolves
// rules and detector claims against a walked tree in one preorder pass, and
// the classification those two produce. Nothing here reads the filesystem:
// the engine sees only a *walk.Tree, so a classification is reproducible from
// a cached scan and a test can build its input by hand.
package classify

import "encoding/json"

// Bucket is a top-level ledger bucket. The numbering is the one docs/02 uses
// and the order the report prints, so the constants are written out rather
// than derived: a reader comparing the report against the document should
// find the same numbers in the same places.
type Bucket uint8

const (
	// BucketMacOS is the non-Data volumes of the container. Nothing in a
	// walk lands here: its bytes come from statfs, so no rule targets it.
	BucketMacOS Bucket = iota + 1
	// BucketApps is application bundles wherever they are installed.
	BucketApps
	// BucketAppData is Library data attributed to an application.
	BucketAppData
	// BucketDeveloper is toolchains, package caches, IDE data and the
	// source and build artifacts under the code roots.
	BucketDeveloper
	// BucketContainers is container runtimes and virtual machines.
	BucketContainers
	// BucketPersonal is the user's own documents and media.
	BucketPersonal
	// BucketBackups is device backups and archived builds.
	BucketBackups
	// BucketSystemCaches is system-owned caches, logs and indexes.
	BucketSystemCaches
	// BucketTrash is the per-volume and per-user trash.
	BucketTrash
	// BucketPurgeable is APFS purgeable space. It is a statfs reading, not
	// a walked byte, so no rule targets it either.
	BucketPurgeable
	// BucketOther is scanned but matched no rule. A large Other is the
	// signal that the catalog needs another rule, which is why it is a
	// bucket of its own rather than folded into a neighbour.
	BucketOther
	// BucketUnaccounted is the residual between what the walk saw and what
	// the volume reports as used. Derived, never claimed.
	BucketUnaccounted

	// numBuckets is one past the last bucket, so a [numBuckets]T array can
	// be indexed by the bucket itself with index 0 left unused.
	numBuckets = int(BucketUnaccounted) + 1
)

// bucketInfo is the id and label of one bucket, indexed by the bucket.
var bucketInfo = [numBuckets]struct{ id, label string }{
	BucketMacOS:        {"macos", "macOS"},
	BucketApps:         {"apps", "Applications"},
	BucketAppData:      {"app-data", "App data"},
	BucketDeveloper:    {"developer", "Developer"},
	BucketContainers:   {"containers", "Containers & VMs"},
	BucketPersonal:     {"personal", "Personal files"},
	BucketBackups:      {"backups", "Backups"},
	BucketSystemCaches: {"system-caches", "System caches & logs"},
	BucketTrash:        {"trash", "Trash"},
	BucketPurgeable:    {"purgeable", "Purgeable"},
	BucketOther:        {"other", "Other"},
	BucketUnaccounted:  {"unaccounted", "Unreadable / unaccounted"},
}

// ID is the stable machine-readable name, used in JSON and rule tests.
func (b Bucket) ID() string {
	if int(b) >= numBuckets {
		return "invalid"
	}
	return bucketInfo[b].id
}

// Label is the name the report prints.
func (b Bucket) Label() string {
	if int(b) >= numBuckets {
		return "invalid"
	}
	return bucketInfo[b].label
}

// String makes a bucket printable in test failures.
func (b Bucket) String() string { return b.ID() }

// Valid reports whether b names one of the twelve buckets.
func (b Bucket) Valid() bool { return b >= BucketMacOS && b <= BucketUnaccounted }

// Walked reports whether the bucket's bytes are walked bytes, the ones a
// classification assigns to nodes. macOS is read from statfs, Purgeable from
// the volume and Unaccounted is the residual: none of the three has a
// directory to show.
func (b Bucket) Walked() bool {
	return b.Valid() && b != BucketMacOS && b != BucketPurgeable && b != BucketUnaccounted
}

// Buckets returns the twelve buckets in docs/02 order.
func Buckets() []Bucket {
	out := make([]Bucket, 0, numBuckets-1)
	for b := BucketMacOS; b <= BucketUnaccounted; b++ {
		out = append(out, b)
	}
	return out
}

// Reclaim is how safely the bytes under a claim can be freed. Every classified
// item carries one from day one even though phase 1 never deletes anything:
// the tag is what lets the report say "40 GB in Developer, 28 GB of it
// regenerable" without a second pass over the tree.
//
// The zero value is deliberately not a tag. A rule, target or claim literal
// that forgets the field would otherwise be read as the first tag, and when
// that tag was Regenerable a forgotten field meant "safe to delete". Now it
// means nothing: New refuses such a rule and the engine drops such a claim.
//
// The tag travels by name in every encoding (MarshalText, UnmarshalJSON), so
// its numbering is private to this binary and reordering it cannot change
// what a stored file says.
type Reclaim uint8

const (
	// reclaimUnset is the zero value, what a literal without the field
	// has. It is never valid.
	reclaimUnset Reclaim = iota
	// Regenerable is safe to delete; the owning tool rebuilds it on demand.
	Regenerable
	// ToolManaged is reclaimed through the owning tool's own command.
	ToolManaged
	// Orphaned is data whose owning application appears to be gone.
	Orphaned
	// UserData is never suggested for deletion.
	UserData
	// System is managed by macOS and left alone.
	System
	// Unknown is not enough information to say.
	Unknown

	// numReclaim is the number of reclaim values, the unset one included,
	// so that ByReclaim can be indexed by the tag directly.
	numReclaim = int(Unknown) + 1
)

// reclaimNames are the tag names, indexed by the tag.
var reclaimNames = [numReclaim]string{
	reclaimUnset: "unset",
	Regenerable:  "regenerable",
	ToolManaged:  "tool-managed",
	Orphaned:     "orphaned",
	UserData:     "user-data",
	System:       "system",
	Unknown:      "unknown",
}

// legacyReclaim is the numbering files written before the zero value was
// made invalid used, when Reclaim still encoded as a JSON number: 0 was
// Regenerable. It is only ever read, never written.
var legacyReclaim = [...]Reclaim{Regenerable, ToolManaged, Orphaned, UserData, System, Unknown}

func (r Reclaim) String() string {
	if int(r) >= numReclaim {
		return "invalid"
	}
	return reclaimNames[r]
}

// Valid reports whether r is one of the six tags: not the zero value and not
// out of range.
func (r Reclaim) Valid() bool { return r >= Regenerable && r <= Unknown }

// MarshalText encodes the tag by name. An invalid tag encodes as "unknown":
// a file is read back by a reader that trusts it, and the one safe thing to
// say about bytes whose tag was lost is that nobody knows.
func (r Reclaim) MarshalText() ([]byte, error) {
	if !r.Valid() {
		return []byte(reclaimNames[Unknown]), nil
	}
	return []byte(reclaimNames[r]), nil
}

// UnmarshalJSON reads a tag by name, or by its legacy number from a file
// written before tags travelled by name. Anything it does not recognise
// decodes as Unknown rather than failing the whole document: a cache whose
// tag cannot be read keeps its bytes, it does not lose them to an error.
func (r *Reclaim) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*r = Unknown
	switch v := v.(type) {
	case string:
		if p, ok := ParseReclaim(v); ok {
			*r = p
		}
	case float64:
		if v >= 0 && v < float64(len(legacyReclaim)) && v == float64(int(v)) {
			*r = legacyReclaim[int(v)]
		}
	}
	return nil
}

// ParseReclaim is the tag a name spells, false for anything else, the unset
// value's own name included.
func ParseReclaim(name string) (Reclaim, bool) {
	for r := Regenerable; r <= Unknown; r++ {
		if reclaimNames[r] == name {
			return r, true
		}
	}
	return 0, false
}

// Reclaimable reports whether the bytes could be freed at all. User data and
// system data are not reclaimable whatever the user thinks of them, and
// Unknown is not counted because counting a guess would inflate the headline
// number the whole report is judged on.
func (r Reclaim) Reclaimable() bool {
	return r == Regenerable || r == ToolManaged || r == Orphaned
}

// Confidence is how sure an owner attribution is. Rules never set it above
// None; the apps detector fills it in when it links data to a bundle. It
// qualifies Claim.Owner and Claim.OwnerKeys and nothing else.
type Confidence uint8

const (
	// None is no attribution claim, which is what a path rule makes.
	None Confidence = iota
	// Strong is direct evidence, such as a bundle id that matches a path.
	Strong
	// Likely is a single plausible owner, such as a cask receipt.
	Likely
	// Corroborating is agreement between two weak signals.
	Corroborating
	// UnknownOwner is a candidate with no owner evidence at all.
	UnknownOwner
)

// confidenceNames are the names, indexed by the value.
var confidenceNames = [...]string{"none", "strong", "likely", "corroborating", "unknown"}

func (c Confidence) String() string {
	if int(c) >= len(confidenceNames) {
		return "invalid"
	}
	return confidenceNames[c]
}
