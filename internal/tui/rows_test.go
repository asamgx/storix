package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// node builds one child of dir for the tests, which construct trees directly
// rather than walking a filesystem: the row layer is about ordering and
// rendering, and a hand-built tree makes both exact.
func node(dir *walk.Node, name string, kind walk.Kind, bytes int64, files uint32, flags walk.Flags) *walk.Node {
	n := &walk.Node{
		Name: name, Parent: dir, Kind: kind, Bytes: bytes, Apparent: bytes,
		Files: files, Flags: flags,
	}
	dir.Children = append(dir.Children, n)
	dir.Bytes += bytes
	dir.Apparent += bytes
	dir.Files += files
	return n
}

// sampleDir is a directory with one of everything the marker column shows.
func sampleDir() *walk.Node {
	root := &walk.Node{Name: "/System/Volumes/Data/fixture", Kind: walk.KindDir}
	node(root, "Applications", walk.KindDir, 4_000_000, 40, 0)
	node(root, "Photos.photoslibrary", walk.KindDir, 3_000_000, 30, walk.FlagBundle)
	node(root, "locked", walk.KindDir, 0, 0, walk.FlagUnreadable)
	node(root, "partial", walk.KindDir, 1_000_000, 10, walk.FlagPartial)
	node(root, "evicted.psd", walk.KindFile, 0, 1, walk.FlagDataless)
	node(root, "alias.bin", walk.KindFile, 0, 1, walk.FlagLinkAlias)
	node(root, "movie.mov", walk.KindFile, 2_000_000, 1, 0)
	root.Small = walk.Small{Files: 1234, Bytes: 500_000, Apparent: 400_000}
	root.Files += root.Small.Files
	root.Bytes += root.Small.Bytes
	return root
}

func rowNames(rows []row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.name)
	}
	return out
}

func TestBuildRowsSortsBySizeAndAggregatesSmallFiles(t *testing.T) {
	rows := buildRows(sampleDir(), rowOpts{sort: sortSize})
	want := []string{
		"Applications", "Photos.photoslibrary", "movie.mov", "partial",
		"… 1,234 small files", "alias.bin", "evicted.psd", "locked",
	}
	if got := rowNames(rows); !equal(got, want) {
		t.Errorf("rows by size =\n%v\nwant\n%v", got, want)
	}
	last := rows[len(rows)-1]
	if last.name != "locked" || !last.partial {
		t.Errorf("the unreadable directory should carry the partial marker, got %+v", last)
	}
}

func TestBuildRowsFlipsAndFilters(t *testing.T) {
	dir := sampleDir()
	asc := buildRows(dir, rowOpts{sort: sortSize, desc: true})
	if asc[0].name != "locked" {
		t.Errorf("ascending by size starts with %q, want the empty directory", asc[0].name)
	}
	byName := buildRows(dir, rowOpts{sort: sortName})
	if byName[0].name != "Applications" {
		t.Errorf("by name starts with %q", byName[0].name)
	}
	byCount := buildRows(dir, rowOpts{sort: sortCount})
	if byCount[0].name != "… 1,234 small files" {
		t.Errorf("by file count starts with %q", byCount[0].name)
	}
	// The aggregate row is not a name the user can filter on, so filtering
	// drops it.
	filtered := buildRows(dir, rowOpts{sort: sortSize, filter: "PHOTO"})
	if got := rowNames(filtered); len(got) != 1 || got[0] != "Photos.photoslibrary" {
		t.Errorf("filter %q matched %v", "PHOTO", got)
	}
}

func TestBuildRowsOpensBundlesOnlyWhenAsked(t *testing.T) {
	dir := sampleDir()
	closed := buildRows(dir, rowOpts{sort: sortName})
	open := buildRows(dir, rowOpts{sort: sortName, bundles: true})
	find := func(rows []row, name string) row {
		for _, r := range rows {
			if r.name == name {
				return r
			}
		}
		t.Fatalf("no row named %q", name)
		return row{}
	}
	if find(closed, "Photos.photoslibrary").openable {
		t.Error("a bundle is a leaf until bundles are turned on")
	}
	if !find(open, "Photos.photoslibrary").openable {
		t.Error("with bundles on the cursor must descend into one")
	}
	if !find(closed, "Applications").openable {
		t.Error("a plain directory is always openable")
	}
}

func TestBuildRowsShowsApparentBytesOnRequest(t *testing.T) {
	dir := &walk.Node{Name: "/d", Kind: walk.KindDir}
	n := node(dir, "sparse.img", walk.KindFile, 0, 1, 0)
	n.Apparent = 10 << 30
	rows := buildRows(dir, rowOpts{sort: sortSize, apparent: true})
	if rows[0].bytes != 10<<30 {
		t.Errorf("apparent bytes = %d, want %d", rows[0].bytes, int64(10)<<30)
	}
}

func TestRenderRowMarksWhatARowIs(t *testing.T) {
	st := NewStyles(false, true)
	dir := sampleDir()
	rows := buildRows(dir, rowOpts{sort: sortName})
	c := layout(120)
	var lines []string
	for _, r := range rows {
		lines = append(lines, renderRow(st, units.Decimal, r, c, dir.Bytes, false))
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{markerBundle, markerAlias, markerDataless, markerPartial, "Applications/"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the table is missing %q:\n%s", want, joined)
		}
	}
	for _, l := range lines {
		if w := len([]rune(stripStyles(l))); w > 120 {
			t.Errorf("row is %d columns wide, over the terminal's 120:\n%s", w, l)
		}
	}
}

// wideDir builds a directory of n children, the shape the browse view has to
// stay fast on.
func wideDir(n int) *walk.Node {
	dir := &walk.Node{Name: "/System/Volumes/Data/wide", Kind: walk.KindDir}
	dir.Children = make([]*walk.Node, 0, n)
	for i := range n {
		node(dir, "entry-"+pad0(i), walk.KindFile, int64(i)*4096, 1, 0)
	}
	return dir
}

func pad0(i int) string {
	s := []byte("00000")
	for p := 4; p >= 0 && i > 0; p-- {
		s[p] = byte('0' + i%10)
		i /= 10
	}
	return string(s)
}

// TestBrowseRendersALargeDirectoryWithinAFrame is the M6 budget: a keypress
// in a twenty-thousand-entry directory costs less than one 60 Hz frame,
// because only the visible window is rendered and the sorted row list is
// cached against the directory.
func TestBrowseRendersALargeDirectoryWithinAFrame(t *testing.T) {
	const keypresses = 200
	b := newBrowse(wideDir(20_000))
	b.setSize(120, 40)
	st := NewStyles(false, true)
	b.visible() // the first build sorts; the keypresses that follow must not

	start := time.Now()
	for i := range keypresses {
		b.move(1 + i%3)
		_ = b.View(st, units.Decimal, "live")
	}
	per := time.Since(start) / keypresses
	t.Logf("%s per keypress over %d keypresses in a %d-entry directory", per, keypresses, 20_000)
	if per > 16*time.Millisecond {
		t.Errorf("a keypress costs %s, over the 16 ms frame budget", per)
	}
}

func BenchmarkBrowseRender(b *testing.B) {
	m := newBrowse(wideDir(20_000))
	m.setSize(120, 40)
	st := NewStyles(false, true)
	m.visible()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		m.move(1 + i%3)
		_ = m.View(st, units.Decimal, "live")
	}
}

func BenchmarkBuildRows(b *testing.B) {
	dir := wideDir(20_000)
	b.ResetTimer()
	for b.Loop() {
		_ = buildRows(dir, rowOpts{sort: sortSize})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNavigatingClearsTheFilter is the rule a filtered directory depends on:
// the filter belongs to the directory it was typed in, so opening a child or
// going back up shows that directory whole.
func TestNavigatingClearsTheFilter(t *testing.T) {
	root := &walk.Node{Name: "/System/Volumes/Data/fixture", Kind: walk.KindDir}
	apps := node(root, "Applications", walk.KindDir, 4_000_000, 2, 0)
	node(apps, "Chat", walk.KindFile, 4_000_000, 1, 0)
	node(root, "Movies", walk.KindDir, 1_000_000, 1, 0)

	b := newBrowse(root)
	b.setSize(120, 20)
	b.setFilter("Applications")
	if len(b.visible()) != 1 {
		t.Fatalf("the filter matched %d rows, want 1", len(b.visible()))
	}
	if !b.open() {
		t.Fatal("could not open the filtered directory")
	}
	if b.opts.filter != "" {
		t.Errorf("the filter %q followed the cursor into %s", b.opts.filter, b.dir.Name)
	}
	if got := rowNames(b.visible()); len(got) != 1 || got[0] != "Chat" {
		t.Errorf("the child directory shows %v, want its own entry", got)
	}
	b.setFilter("nothing")
	if !b.parent() || b.opts.filter != "" {
		t.Errorf("going up kept the filter %q", b.opts.filter)
	}
}
