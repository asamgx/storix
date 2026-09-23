package ledger

import (
	"fmt"
	"sort"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

// Bucket is one of the twelve top-level rows of docs/02. Buckets partition
// the volume's used space: the walked ones come from the classification, the
// others from the volume readings, and together they are the answer to "what
// is on this disk" that the rest of the report elaborates.
type Bucket struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
	// Known is false when the bucket's size could not be read, as for
	// purgeable space on a volume that reports none.
	Known bool   `json:"known"`
	Note  string `json:"note,omitempty"`
	// Reclaimable is the regenerable, tool-managed and orphaned bytes.
	Reclaimable int64 `json:"reclaimable"`
	// ByReclaim is one line per non-zero reclaimability tag.
	ByReclaim []Line `json:"by_reclaim,omitempty"`
	// Categories and Owners are the largest eight of each, so the report
	// has something to print without reaching into the classification.
	Categories []Line `json:"categories,omitempty"`
	Owners     []Line `json:"owners,omitempty"`
}

// bucketLines is how many category and owner lines a bucket carries. Eight is
// what fits under a bucket heading without the section becoming a listing.
const bucketLines = 8

// BuildClassified assembles the ledger and its buckets.
//
// It is Build plus the bucket table: the phase 1a lines and both identities
// are computed exactly as before, so adding classification cannot move a byte
// of the volume or container reconciliation.
func BuildClassified(f *volume.Facts, t *walk.Tree, u units.Format, class *classify.Classification) *Ledger {
	l := build(f, t, u)
	l.fillBuckets(class)
	return l
}

// fillBuckets builds the twelve rows.
//
// Buckets 2 to 9 and 11 come from the classification and sum to the scanned
// bytes by construction, because the engine gave every node's own bytes to
// exactly one bucket. Bucket 1 is the other volumes of the container, 10 is
// the purgeable reading, and 12 is the residual: none of the three is a
// walked byte, which is why the identity asserted in the tests covers the
// walked buckets alone.
func (l *Ledger) fillBuckets(class *classify.Classification) {
	l.Buckets = make([]Bucket, 0, len(classify.Buckets()))
	for _, b := range classify.Buckets() {
		l.Buckets = append(l.Buckets, Bucket{ID: b.ID(), Label: b.Label(), Known: true})
	}

	l.fillMacOSBucket()
	l.fillWalkedBuckets(class)
	l.fillDerivedBuckets()
}

// bucket returns the row for one bucket.
func (l *Ledger) bucket(b classify.Bucket) *Bucket { return &l.Buckets[int(b)-1] }

// fillMacOSBucket totals the volumes that are never walked.
func (l *Ledger) fillMacOSBucket() {
	m := l.bucket(classify.BucketMacOS)
	for _, line := range l.MacOS {
		m.Bytes += line.Bytes
	}
	m.Known = l.Container.Known
	m.Note = "sealed system, Preboot, VM, Update and Recovery volumes, read from statfs"
	if !m.Known {
		m.Note = "no APFS container owns the scanned volume, so its sibling volumes are unknown"
	}
}

// fillWalkedBuckets copies the classification's totals into the rows.
func (l *Ledger) fillWalkedBuckets(class *classify.Classification) {
	if class == nil {
		o := l.bucket(classify.BucketOther)
		o.Bytes = l.Scanned.Bytes
		o.Files = int64(l.Counters.Files)
		o.Note = "not classified"
		return
	}
	for _, b := range walkedBuckets() {
		t := class.Buckets[b]
		row := l.bucket(b)
		row.Bytes = t.Bytes
		row.Files = t.Files
		row.Reclaimable = t.Reclaimable()
		row.ByReclaim = reclaimLines(t)
		row.Categories = topLines(t.Categories, t.Bytes)
		row.Owners = ownerLines(class, b)
	}
	o := l.bucket(classify.BucketOther)
	if o.Bytes > 0 {
		o.Note = "scanned but matched no rule; a large Other means the catalog needs work"
	}
}

// fillDerivedBuckets fills the three rows that are readings rather than walks.
func (l *Ledger) fillDerivedBuckets() {
	p := l.bucket(classify.BucketPurgeable)
	p.Bytes = l.Purgeable.Bytes
	p.Known = l.Purgeable.Known
	p.Note = l.Purgeable.Note
	p.Reclaimable = l.Purgeable.Bytes
	if !p.Known {
		p.Reclaimable = 0
	}

	u := l.bucket(classify.BucketUnaccounted)
	switch {
	case l.Residual.Bytes > 0:
		u.Bytes = l.Residual.Bytes
		u.Note = l.Residual.Label
	case l.Residual.Bytes < 0:
		u.Note = "shared/cloned blocks: the scan counted " +
			l.Units.Bytes(-l.Residual.Bytes) + " more than the volume reports as used"
	default:
		u.Note = "nothing unaccounted"
	}
	if l.Residual.Note != "" {
		u.Note = u.Note + "; " + l.Residual.Note
	}
	if n := len(l.Unreadable); n > 0 && l.Residual.Bytes > 0 {
		u.Note = fmt.Sprintf("%s (%d unreadable %s)", u.Note, n, plural(n, "path", "paths"))
	}
	// Container overhead is APFS structure outside any volume. docs/02 puts
	// it in bucket 12, and it is what makes the twelve rows sum to the
	// container's used space when the container is known.
	if l.Container.Known && l.Overhead.Bytes > 0 {
		u.Bytes += l.Overhead.Bytes
		u.Note = fmt.Sprintf("%s; %s of container overhead", u.Note, l.Units.Bytes(l.Overhead.Bytes))
	}
}

// BucketDenominator is what the twelve bucket rows are measured against and
// what they sum to: the container's used space when the container is known
// (bucket 1 is the sibling volumes, which are not part of the data volume's
// own used space), else the data volume's used space, else the walk total.
func (l *Ledger) BucketDenominator() int64 {
	if l.Container.Known && l.Container.Used > 0 {
		return l.Container.Used
	}
	if l.Volume.UsedAfter > 0 {
		return l.Volume.UsedAfter
	}
	return l.Scanned.Bytes
}

// BucketDenominatorLabel names BucketDenominator for the table footer.
func (l *Ledger) BucketDenominatorLabel() string {
	if l.Container.Known && l.Container.Used > 0 {
		return "used (container)"
	}
	if l.Volume.UsedAfter > 0 {
		return "used (volume)"
	}
	return "scanned"
}

// BucketSum is the sum of the twelve bucket rows, the number a reader gets by
// adding the column. It equals BucketDenominator when the residual is not
// negative; a negative residual (APFS clones counted twice) is clamped to
// zero in bucket 12 and the note says so.
func (l *Ledger) BucketSum() int64 {
	var sum int64
	for i := range l.Buckets {
		sum += l.Buckets[i].Bytes
	}
	return sum
}

// reclaimLines turns a bucket's reclaim split into lines, largest first.
func reclaimLines(t classify.BucketTotal) []Line {
	var out []Line
	for r := classify.Reclaim(0); int(r) < len(t.ByReclaim); r++ {
		if t.ByReclaim[r] == 0 {
			continue
		}
		out = append(out, Line{
			ID:    r.String(),
			Label: r.String(),
			Bytes: t.ByReclaim[r],
			Known: true,
		})
	}
	sortLines(out)
	return out
}

// topLines turns a label-to-bytes map into the largest few lines, with each
// line's share of the bucket in its note.
func topLines(m map[string]int64, total int64) []Line {
	if len(m) == 0 {
		return nil
	}
	out := make([]Line, 0, len(m))
	for label, bytes := range m {
		// A directory that matched a rule but holds no bytes of its own
		// is a real claim and a useless row; the bucket lists what is in
		// it, not every label the classifier used.
		if bytes == 0 {
			continue
		}
		out = append(out, Line{ID: label, Label: label, Bytes: bytes, Known: true, Note: share(bytes, total)})
	}
	if len(out) == 0 {
		return nil
	}
	sortLines(out)
	if len(out) > bucketLines {
		out = out[:bucketLines]
	}
	return out
}

// ownerLines is the largest few owners of one bucket.
func ownerLines(class *classify.Classification, b classify.Bucket) []Line {
	total := class.Buckets[b].Bytes
	out := make([]Line, 0, len(class.Owners))
	for label, o := range class.Owners {
		if o.ByBucket[b] <= 0 {
			continue
		}
		out = append(out, Line{
			ID: label, Label: label, Bytes: o.ByBucket[b], Known: true,
			Note: share(o.ByBucket[b], total),
		})
	}
	sortLines(out)
	if len(out) > bucketLines {
		out = out[:bucketLines]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// share is a line's percentage of its bucket.
func share(part, whole int64) string {
	if whole <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%% of the bucket", 100*float64(part)/float64(whole))
}

// sortLines orders lines by size, then by label so two runs agree.
func sortLines(ls []Line) {
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].Bytes != ls[j].Bytes {
			return ls[i].Bytes > ls[j].Bytes
		}
		return ls[i].Label < ls[j].Label
	})
}

// WalkedBucketBytes is the sum of the buckets that hold walked bytes. It
// equals Scanned.Bytes; the report prints the two side by side and the tests
// assert the equality.
func (l *Ledger) WalkedBucketBytes() int64 {
	var n int64
	for _, b := range walkedBuckets() {
		n += l.bucket(b).Bytes
	}
	return n
}

// WalkedBucketFiles is the same sum over file counts. Every leaf the walk met
// belongs to exactly one bucket, whether it was retained as a node or folded
// into its parent's aggregate, so this equals Counters.Files.
func (l *Ledger) WalkedBucketFiles() int64 {
	var n int64
	for _, b := range walkedBuckets() {
		n += l.bucket(b).Files
	}
	return n
}

// walkedBuckets are the buckets whose contents came from the walk. The other
// three are readings: the sibling volumes, the purgeable space and the
// residual between what the walk saw and what the volume reports.
func walkedBuckets() []classify.Bucket {
	out := make([]classify.Bucket, 0, 9)
	for _, b := range classify.Buckets() {
		switch b {
		case classify.BucketMacOS, classify.BucketPurgeable, classify.BucketUnaccounted:
			continue
		}
		out = append(out, b)
	}
	return out
}
