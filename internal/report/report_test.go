package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/ledger"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/volume"
	"github.com/asamgx/storix/internal/walk"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Fixed instants and space numbers, so the golden files describe a machine
// rather than the machine the test happens to run on.
var (
	started  = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	finished = time.Date(2026, 9, 22, 10, 0, 19, 700_000_000, time.UTC)
)

const (
	total    = 245_107_195_904
	avail    = 34_493_267_968
	dataUsed = 181_046_386_688
)

// node makes a leaf.
func node(name string, bytes, apparent int64) *walk.Node {
	return &walk.Node{
		Name: name, Kind: walk.KindFile, Bytes: bytes, Apparent: apparent,
		Mtime: started.Unix(),
	}
}

// dir makes a directory with the given children.
func dir(name string, children ...*walk.Node) *walk.Node {
	n := &walk.Node{Name: name, Kind: walk.KindDir, Mtime: started.Unix()}
	for _, c := range children {
		c.Parent = n
		n.Children = append(n.Children, c)
	}
	return n
}

// fillTree sums a hand-built tree the way the walker's finalize pass does and
// assigns the preorder node index.
func fillTree(root *walk.Node) *walk.Tree {
	var index []*walk.Node
	var visit func(*walk.Node)
	visit = func(n *walk.Node) {
		index = append(index, n)
		if !n.IsDir() {
			n.Files = 1
			return
		}
		n.Files = n.Small.Files
		n.Bytes += n.Small.Bytes
		n.Apparent += n.Small.Apparent
		for _, c := range n.Children {
			visit(c)
			n.Bytes += c.Bytes
			n.Apparent += c.Apparent
			n.Files += c.Files
			if c.IsDir() {
				n.Dirs += c.Dirs + 1
			}
		}
	}
	visit(root)
	return &walk.Tree{Root: root, Nodes: index, Started: started, Finished: finished}
}

// fakeScan is the scan both golden files describe: a data-volume walk with a
// nested mount skipped, three kinds of unreadable directory, evicted cloud
// files in two locations and a hard-link group.
func fakeScan() *scan.Result {
	users := dir("Users",
		dir("u",
			dir("Library",
				dir("Group Containers",
					node("data.img.raw", 18_700_000_000, 245_000_000_000),
				),
				dir("Caches",
					node("go-build", 4_200_000_000, 4_200_000_000),
				),
				dir("Mobile Documents",
					func() *walk.Node {
						n := node("Keynote.key", 0, 2_000_000_000)
						n.Flags = walk.FlagDataless
						return n
					}(),
				),
			),
			func() *walk.Node {
				d := dir("Desktop")
				d.Small = walk.Small{Files: 12, Dataless: 5, Bytes: 49_152, Apparent: 41_003}
				return d
			}(),
		),
	)
	root := dir(mac.DataRoot, users, dir("private", dir("var", dir("db"))))
	root.Children[1].Children[0].Children[0].Flags = walk.FlagUnreadable | walk.FlagPartial

	tr := fillTree(root)
	tr.LinkGroups = 1_284
	tr.LinkBytesSaved = 2_300_000_000
	tr.Vanished = 3
	tr.Errors = []walk.PathError{
		{Path: mac.DataRoot + "/private/var/db", Op: "readdir", Errno: syscall.EACCES, Class: walk.ErrPermission},
		{Path: mac.DataRoot + "/private/var/folders/zz", Op: "readdir", Errno: syscall.EACCES, Class: walk.ErrPermission},
		{Path: mac.DataRoot + "/Users/u/Library/Mail", Op: "readdir", Errno: syscall.EPERM, Class: walk.ErrTCC},
		{Path: mac.DataRoot + "/Users/u/Library/Caches/com.apple.ap.adprivacyd", Op: "readdir", Errno: syscall.EPERM, Class: walk.ErrProtected},
	}
	tr.SkippedMounts = []walk.SkippedMount{
		{Path: mac.DataRoot + "/home", FSType: "autofs", From: "map auto_home", Reason: "mount point"},
		{Path: mac.DataRoot + "/Users/u/OrbStack", FSType: "nfs", From: "OrbStack:/OrbStack", Reason: "mount point"},
	}
	tr.SkipListed = []string{mac.DataRoot + "/private/var/vm"}

	f := fakeFacts()
	l := ledger.Build(f, tr, units.Decimal)
	return &scan.Result{
		Facts:  f,
		Tree:   tr,
		Ledger: l,
		Timing: scan.Timing{
			Facts:  310 * time.Millisecond,
			Walk:   19 * time.Second,
			Finish: 12 * time.Millisecond,
			Ledger: 4 * time.Millisecond,
			Total:  19_700 * time.Millisecond,
		},
	}
}

// fakeFacts fabricates the volume readings around the walk.
func fakeFacts() *volume.Facts {
	mk := func(mount, device string, used int64) volume.Volume {
		return volume.Volume{
			MountPoint: mount, Device: device, FSType: "apfs", Container: "disk3",
			Bsize: 4096, Total: total, Free: avail, Avail: avail,
			Used: used, UsedStatfs: total - avail, UsedSource: volume.UsedFromGetattrlist,
			Local: true,
		}
	}
	vols := func(data int64) []volume.Volume {
		return []volume.Volume{
			mk("/", "/dev/disk3s1s1", 12_639_330_304),
			mk(mac.DataRoot, "/dev/disk3s5", data),
			mk("/System/Volumes/Preboot", "/dev/disk3s2", 9_042_919_424),
			mk("/System/Volumes/Update", "/dev/disk3s4", 2_899_968),
			mk("/System/Volumes/VM", "/dev/disk3s6", 6_442_450_944),
			{MountPoint: "/System/Volumes/xarts", Device: "/dev/disk1s2", FSType: "apfs",
				Container: "disk1", Bsize: 4096, Total: 524_288_000, Free: 517_988_352,
				Avail: 517_988_352, Used: 6_299_648, UsedSource: volume.UsedFromGetattrlist, Local: true},
		}
	}
	beforeVols, afterVols := vols(dataUsed-132_000_000), vols(dataUsed)
	return &volume.Facts{
		Root:      mac.DataRoot,
		Mounts:    volume.NewMountTable(beforeVols),
		Before:    volume.Snapshot{At: started, Volumes: beforeVols},
		After:     volume.Snapshot{At: finished, Volumes: afterVols},
		Purgeable: volume.Purgeable{Bytes: 1_573_741_824, Known: true, Source: "foundation", ImportantUsage: 36_067_009_792, StatfsAvail: avail},
		Snapshots: volume.Snapshots{Known: true, Names: []string{"com.apple.TimeMachine.2026-09-22-084500.local"}},
		Terminal:  volumeTerminal(),
		FDA:       macProbe(),
		Dataless:  mac.PolicyOff,
		Euid:      501,
	}
}

func volumeTerminal() mac.Terminal {
	return mac.Terminal{Program: "ghostty", BundleID: "com.mitchellh.ghostty", AppName: "Ghostty", PID: 4242}
}

func macProbe() mac.TCCProbe {
	return mac.TCCProbe{Path: "/Users/u/Library/Mail", Granted: false, Err: syscall.EPERM}
}

// golden compares got against the file, rewriting it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // golden files are not secrets
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/report -update` to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestTextGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(&buf, fakeScan(), Options{Units: units.Decimal}); err != nil {
		t.Fatal(err)
	}
	golden(t, "scan.txt", buf.String())
}

func TestJSONGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, fakeScan(), Options{Units: units.Decimal, Version: "v0.0.0-test"}); err != nil {
		t.Fatal(err)
	}
	golden(t, "scan.json", buf.String())
}

func TestJSONIsValidAndCarriesTheSchema(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, fakeScan(), Options{Version: "v0.0.0-test"}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("the document does not parse: %v", err)
	}
	if doc["schema"] != float64(SchemaVersion) {
		t.Errorf("schema = %v, want %d", doc["schema"], SchemaVersion)
	}
	for _, key := range []string{"ledger", "facts", "errors", "skipped_mounts", "counters", "tree"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the document has no %q", key)
		}
	}
}

func TestJSONIdentitiesSurviveTheRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, fakeScan(), Options{}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Ledger struct {
			Scanned   ledger.Line         `json:"scanned"`
			Purgeable ledger.Line         `json:"purgeable"`
			Residual  ledger.Line         `json:"residual"`
			Overhead  ledger.Line         `json:"overhead"`
			Data      ledger.Line         `json:"data"`
			MacOS     []ledger.Line       `json:"macos"`
			Volume    ledger.VolumeLedger `json:"volume"`
			Container ledger.Container    `json:"container"`
		} `json:"ledger"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	l := doc.Ledger
	if got := l.Scanned.Bytes + l.Purgeable.Bytes + l.Residual.Bytes; got != l.Volume.UsedAfter {
		t.Errorf("volume identity broken in JSON: %d != %d", got, l.Volume.UsedAfter)
	}
	sum := l.Data.Bytes + l.Overhead.Bytes
	for _, m := range l.MacOS {
		sum += m.Bytes
	}
	if sum != l.Container.Used {
		t.Errorf("container identity broken in JSON: %d != %d", sum, l.Container.Used)
	}
}

func TestJSONLimitsTheTree(t *testing.T) {
	res := fakeScan()
	var limited, full bytes.Buffer
	if err := JSON(&limited, res, Options{Depth: 1, MinSize: 1_000_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := JSON(&full, res, Options{Full: true}); err != nil {
		t.Fatal(err)
	}
	if limited.Len() >= full.Len() {
		t.Errorf("the limited document (%d B) is not smaller than the full one (%d B)", limited.Len(), full.Len())
	}
	if !strings.Contains(limited.String(), "children_omitted") {
		t.Error("the limited document does not say that children were left out")
	}
	if strings.Contains(limited.String(), "Keynote.key") {
		t.Error("a node below the depth limit made it into the limited document")
	}
	if !strings.Contains(full.String(), "Keynote.key") {
		t.Error("--full dropped a node")
	}
	if !strings.Contains(full.String(), `"dataless"`) {
		t.Error("the dataless flag is missing from the full tree")
	}
}

func TestTextColorIsOffByDefaultAndKeepsAlignment(t *testing.T) {
	res := fakeScan()
	var plain, colored bytes.Buffer
	if err := Text(&plain, res, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Text(&colored, res, Options{Color: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Error("the plain report contains ANSI escapes")
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Error("the colored report contains no ANSI escapes")
	}
	if got, want := len(strings.Split(colored.String(), "\n")), len(strings.Split(plain.String(), "\n")); got != want {
		t.Errorf("colored report has %d lines, plain has %d", got, want)
	}
}

func TestTextTopLimitAndOmission(t *testing.T) {
	res := fakeScan()
	var three, none bytes.Buffer
	if err := Text(&three, res, Options{Top: 3}); err != nil {
		t.Fatal(err)
	}
	if err := Text(&none, res, Options{Top: -1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(three.String(), "TOP 3 DIRECTORIES") {
		t.Error("--top 3 did not limit the listing")
	}
	if strings.Contains(none.String(), "DIRECTORIES") {
		t.Error("a negative Top did not drop the directory section")
	}
}

func TestTextRefusesAnEmptyResult(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(&buf, nil, Options{}); err == nil {
		t.Error("Text accepted a nil result")
	}
	if err := JSON(&buf, &scan.Result{}, Options{}); err == nil {
		t.Error("JSON accepted a result with no tree")
	}
}
