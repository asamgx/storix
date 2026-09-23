package apps

import (
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/testutil"
	"github.com/asamgx/storix/internal/walk"
)

// publisherFixture is one publisher folder holding two products, which is the
// shape the whole of finding 1 is about.
//
// ~/Library/Application Support/Google holds Chrome's data and Android
// Studio's side by side. Both applications are installed unless the test says
// otherwise, and each subtree carries enough bytes that the arithmetic of who
// owns what is visible in the numbers.
type publisherFixture struct {
	tree *walk.Tree
	home string
	root string
}

const (
	chromeDataBytes  = 400 << 10
	studioDataBytes  = 800 << 10
	sharedDataBytes  = 64 << 10
	chromeAppBytes   = 128 << 10
	studioAppBytes   = 256 << 10
	publisherSupport = "Users/andrewsam/Library/Application Support/Google"
)

func newPublisherFixture(t *testing.T, withChromeBundle bool) *publisherFixture {
	t.Helper()
	f := testutil.New(t)
	f.File(publisherSupport+"/Chrome/History", chromeDataBytes)
	f.File(publisherSupport+"/AndroidStudio2025.1.3/caches.bin", studioDataBytes)
	f.File(publisherSupport+"/updater.log", sharedDataBytes)

	bundle := func(rel, id, name string, size int) {
		f.File(rel+"/Contents/Info.plist", 64)
		f.File(rel+"/Contents/MacOS/stub", size)
		_ = id
		_ = name
	}
	bundle("Applications/Android Studio.app", "com.google.android.studio", "Android Studio", studioAppBytes)
	if withChromeBundle {
		bundle("Applications/Google Chrome.app", "com.google.Chrome", "Google Chrome", chromeAppBytes)
	}

	tree, err := walk.Walk(t.Context(), walk.Options{Root: f.Root})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return &publisherFixture{tree: tree, home: f.Root + "/Users/andrewsam", root: f.Root}
}

// analyze runs the analysis with the bundles named as facts, because the
// fixture's Info.plists are stubs rather than real property lists.
func (pf *publisherFixture) analyze(t *testing.T, withChromeBundle bool) *Analysis {
	t.Helper()
	facts := &Facts{AppDirBundles: []BundleInfo{
		{Path: pf.root + "/Applications/Android Studio.app", ID: "com.google.android.studio", DisplayName: "Android Studio"},
	}}
	if withChromeBundle {
		facts.AppDirBundles = append(facts.AppDirBundles, BundleInfo{
			Path: pf.root + "/Applications/Google Chrome.app", ID: "com.google.Chrome", DisplayName: "Google Chrome",
		})
	}
	return Analyze(pf.tree, facts, classify.Context{Home: pf.home}, Options{
		Root: pf.root, Now: time.Now().Add(365 * 24 * time.Hour),
	})
}

// ownerOf finds the owner key a display path resolved to.
func ownerOf(t *testing.T, a *Analysis, suffix string) Owner {
	t.Helper()
	for i, c := range a.Candidates {
		if strings.HasSuffix(c.Path, suffix) {
			return a.Matches[i].Owner
		}
	}
	t.Fatalf("no candidate ending in %q", suffix)
	return Owner{}
}

// TestAPublisherFolderIsNotItsBiggestProduct is finding 1's first half.
//
// "Google" was one of Chrome's names in the alias table, so the publisher
// folder resolved to Chrome and Chrome was handed the whole subtree — Android
// Studio's data with it. The folder belongs to the publisher, and each product
// inside it belongs to the product.
func TestAPublisherFolderIsNotItsBiggestProduct(t *testing.T) {
	t.Parallel()
	pf := newPublisherFixture(t, true)
	a := pf.analyze(t, true)

	folder := ownerOf(t, a, "/Application Support/Google")
	if folder.Kind != KindVendor || folder.Key != "vendor:com.google" {
		t.Errorf("the Google folder resolved to %+v, want the publisher", folder)
	}
	if got := ownerOf(t, a, "/Google/Chrome").Key; got != "app:com.google.Chrome" {
		t.Errorf("Google/Chrome resolved to %q, want Chrome", got)
	}
	if got := ownerOf(t, a, "/Google/AndroidStudio2025.1.3").Key; got != "app:com.google.android.studio" {
		t.Errorf("Google/AndroidStudio2025.1.3 resolved to %q, want Android Studio", got)
	}
	if v := a.Verdicts["vendor:com.google"]; v == nil || v.State != StateVendor {
		t.Errorf("the Google folder's verdict is %v, want vendor while Google's products are installed", v)
	}
}

// TestNoByteIsInTwoOwnersFootprints is finding 1's second half, and the reason
// the first half is not enough on its own.
//
// A claim carries its whole subtree, so the publisher folder's claim counts
// Android Studio's bytes and Android Studio's claim counts them again. Two
// owners then offer the same bytes for deletion, and the sum of the footprints
// is larger than the disk. Each component keeps only what no deeper claim
// took.
func TestNoByteIsInTwoOwnersFootprints(t *testing.T) {
	t.Parallel()
	pf := newPublisherFixture(t, true)
	a := pf.analyze(t, true)
	fps := Footprints(a, a.Claims())

	byKey := make(map[string]Footprint, len(fps))
	for _, fp := range fps {
		byKey[fp.Owner.Key] = fp
	}

	studio, ok := byKey["app:com.google.android.studio"]
	if !ok {
		t.Fatalf("Android Studio has no footprint; owners are %v", a.OwnerKeys())
	}
	if got := componentBytes(studio, "/Google/AndroidStudio2025.1.3"); got < studioDataBytes {
		t.Errorf("Android Studio's own data = %d bytes, want at least %d", got, studioDataBytes)
	}

	folder, ok := byKey["vendor:com.google"]
	if !ok {
		t.Fatalf("the publisher folder has no footprint; owners are %v", a.OwnerKeys())
	}
	own := componentBytes(folder, "/Application Support/Google")
	if own >= studioDataBytes {
		t.Errorf("the Google folder still counts %d bytes, which is Android Studio's data over again", own)
	}
	if own < sharedDataBytes {
		t.Errorf("the Google folder kept %d bytes, want at least its own %d", own, sharedDataBytes)
	}

	// The invariant in one line: the components under the publisher folder
	// partition it. They add up to what the folder actually holds, once.
	node, ok := lookupDisplay(a.Tree, pf.home+"/Library/Application Support/Google")
	if !ok {
		t.Fatal("the publisher folder is not in the tree")
	}
	if total := componentsUnder(fps, node.Display()); total != node.Bytes {
		t.Errorf("the components under the publisher folder add up to %d, want %d: "+
			"the same bytes are in two footprints", total, node.Bytes)
	}
}

// componentsUnder totals every owner's component at or beneath a path.
func componentsUnder(fps []Footprint, root string) int64 {
	var total int64
	for _, fp := range fps {
		for _, c := range fp.Components {
			if c.Path == root || strings.HasPrefix(c.Path, root+"/") {
				total += c.Bytes
			}
		}
	}
	return total
}

// componentBytes is the size a footprint gives one of its components.
func componentBytes(fp Footprint, suffix string) int64 {
	for _, c := range fp.Components {
		if strings.HasSuffix(c.Path, suffix) {
			return c.Bytes
		}
	}
	return -1
}

// TestUninstallingOneProductLeavesTheOthersAlone is the failure the whole
// finding exists to prevent, stated as the user would meet it.
//
// With Chrome gone, the publisher folder must not become an orphan that offers
// Android Studio's live data for deletion. The folder stays the publisher's
// because Android Studio is still installed, and Android Studio's own
// directory stays Android Studio's.
func TestUninstallingOneProductLeavesTheOthersAlone(t *testing.T) {
	t.Parallel()
	pf := newPublisherFixture(t, false)
	a := pf.analyze(t, false)

	folder := ownerOf(t, a, "/Application Support/Google")
	if folder.Key != "vendor:com.google" {
		t.Fatalf("the Google folder resolved to %q, want the publisher", folder.Key)
	}
	v := a.Verdicts[folder.Key]
	if v == nil || v.State != StateVendor {
		t.Fatalf("the Google folder's verdict is %v, want vendor: Android Studio is still installed", v)
	}
	if v.State.Reclaimable() {
		t.Error("the publisher folder was offered for deletion while one of its products is installed")
	}

	studio := ownerOf(t, a, "/Google/AndroidStudio2025.1.3")
	if studio.Key != "app:com.google.android.studio" {
		t.Errorf("Android Studio's data resolved to %q", studio.Key)
	}
	if sv := a.Verdicts[studio.Key]; sv == nil || sv.State != StateInstalled {
		t.Errorf("Android Studio's verdict is %v, want installed", sv)
	}

	// Chrome's own directory is orphaned, because Chrome really is gone.
	// Nothing else under the folder may be: not Android Studio's data, and
	// not the folder itself, whose claim covers the whole subtree.
	for _, cl := range a.Claims() {
		display := cl.Node.Display()
		if !strings.HasSuffix(display, "/Application Support/Google") &&
			!strings.Contains(display, "/Application Support/Google/AndroidStudio") {
			continue
		}
		if cl.Reclaim == classify.Orphaned {
			t.Errorf("%s was tagged orphaned: %v", display, cl.Evidence)
		}
	}

	// And the bytes offered for deletion are Chrome's alone. Before the
	// split, the folder's claim covered Android Studio's data too, so
	// reclaiming "Chrome" would have taken it.
	var reclaimable int64
	for _, fp := range Footprints(a, a.Claims()) {
		if strings.HasPrefix(fp.Owner.Key, "product:chrome") || fp.Owner.Key == "app:com.google.Chrome" {
			reclaimable += fp.Reclaimable()
		}
	}
	if reclaimable >= studioDataBytes {
		t.Errorf("Chrome offers %d bytes for deletion, which reaches into Android Studio's %d",
			reclaimable, studioDataBytes)
	}
}

// TestAPublisherFolderIsAnOrphanOnceNothingIsInstalled is the other side of
// the rule. A publisher folder is kept by the publisher's products, not by
// being a publisher folder, so one whose products have all gone is residue
// like any other.
func TestAPublisherFolderIsAnOrphanOnceNothingIsInstalled(t *testing.T) {
	t.Parallel()
	f := testutil.New(t)
	f.File("Users/andrewsam/Library/Application Support/Adobe/Acrobat/cache.bin", 64<<10)
	tree, err := walk.Walk(t.Context(), walk.Options{Root: f.Root})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	a := Analyze(tree, &Facts{}, classify.Context{Home: f.Root + "/Users/andrewsam"}, Options{
		Root: f.Root, Now: time.Now().Add(365 * 24 * time.Hour),
	})

	folder := ownerOf(t, a, "/Application Support/Adobe")
	if folder.Key != "vendor:com.adobe" {
		t.Fatalf("the Adobe folder resolved to %q", folder.Key)
	}
	v := a.Verdicts[folder.Key]
	if v == nil || v.State != StateOrphanLikely {
		t.Fatalf("the Adobe folder's verdict is %v, want orphan-likely: nothing Adobe is installed", v)
	}
	if !strings.Contains(strings.Join(v.Evidence, " "), "nothing inside Adobe") {
		t.Errorf("evidence = %v, want it to say why the folder is not protected", v.Evidence)
	}
}
