package apps

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/walk"
)

// corpus is an offline replica of one real machine's application layout.
//
// It exists because the interesting cases cannot be reproduced any other way:
// a cask whose application is missing, a group container named after a team
// id, a staged update copy, a bundle in a Trash the test process cannot even
// read. The names come from a real machine, the bytes do not, and nothing in
// the test touches anything outside its temporary directory.
type corpus struct {
	Root string
	Home string
	// Caskroom is the fixture's Caskroom in display form.
	Caskroom string
}

// buildCorpus materialises the fixture from the three manifests.
func buildCorpus(t *testing.T) *corpus {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the fixture root: %v", err)
	}
	c := &corpus{
		Root:     root,
		Home:     filepath.Join(root, "Users", "andrewsam"),
		Caskroom: filepath.Join(root, "opt", "homebrew", "Caskroom"),
	}
	c.buildTree(t)
	c.buildBundles(t)
	c.buildCasks(t)
	return c
}

// buildTree creates the directories and files of machine1.txt.
func (c *corpus) buildTree(t *testing.T) {
	t.Helper()
	for _, line := range manifestLines(t, "machine1.txt") {
		if rel, target, isLink := strings.Cut(line, " -> "); isLink {
			full := filepath.Join(c.Root, rel)
			mkdirAll(t, filepath.Dir(full))
			if err := os.Symlink(target, full); err != nil {
				t.Fatalf("symlink %s: %v", rel, err)
			}
			continue
		}
		rel, sizeText, isFile := strings.Cut(line, "\t")
		full := filepath.Join(c.Root, rel)
		if !isFile {
			mkdirAll(t, full)
			continue
		}
		size, err := strconv.Atoi(strings.TrimSpace(sizeText))
		if err != nil {
			t.Fatalf("manifest line %q: %v", line, err)
		}
		writeFile(t, full, make([]byte, size))
	}
}

// buildBundles creates an application bundle per row of bundles.tsv, each
// with a real XML Info.plist so the plist decoder is exercised end to end.
func (c *corpus) buildBundles(t *testing.T) {
	t.Helper()
	for _, line := range manifestLines(t, "bundles.tsv") {
		cols := strings.Split(line, "\t")
		if len(cols) < 5 {
			t.Fatalf("bundles.tsv line %q has %d columns, want 5", line, len(cols))
		}
		rel, id, name, version, mas := cols[0], cols[1], cols[2], cols[3], cols[4]
		bundle := filepath.Join(c.Root, rel)

		var body strings.Builder
		if id != "" {
			body.WriteString("\t<key>CFBundleIdentifier</key><string>" + id + "</string>\n")
		}
		if name != "" {
			body.WriteString("\t<key>CFBundleName</key><string>" + name + "</string>\n")
		}
		if version != "" {
			body.WriteString("\t<key>CFBundleShortVersionString</key><string>" + version + "</string>\n")
		}
		writeFile(t, filepath.Join(bundle, "Contents", "Info.plist"), plistXML(body.String()))
		writeFile(t, filepath.Join(bundle, "Contents", "MacOS", "stub"), make([]byte, 1024))
		if mas == "1" {
			writeFile(t, filepath.Join(bundle, "Contents", "_MASReceipt", "receipt"), []byte("receipt"))
		}
	}
}

// buildCasks copies this machine's real install receipts into the fixture
// Caskroom, and adds the two casks whose application is missing.
func (c *corpus) buildCasks(t *testing.T) {
	t.Helper()
	for _, token := range manifestLines(t, "casks.txt") {
		src := filepath.Join("testdata", "caskroom", token, ".metadata", "INSTALL_RECEIPT.json")
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("reading the %s receipt: %v", token, err)
		}
		writeFile(t, filepath.Join(c.Caskroom, token, ".metadata", "INSTALL_RECEIPT.json"), data)
	}
	// A font cask, which the probe must skip entirely.
	mkdirAll(t, filepath.Join(c.Caskroom, "font-jetbrains-mono-nerd-font", "3.4.0"))
}

// manifestLines reads a manifest, dropping comments and blank lines.
func manifestLines(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "corpus", name))
	if err != nil {
		t.Fatalf("opening %s: %v", name, err)
	}
	defer func() { _ = f.Close() }()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return out
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, full string, data []byte) {
	t.Helper()
	mkdirAll(t, filepath.Dir(full))
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// replay answers the probe's commands from the fixture rather than the
// machine. Building it here rather than from a static file is deliberate: the
// paths a command prints depend on the temporary root, so the fixture has to
// know where the corpus landed.
func (c *corpus) replay(t *testing.T) *probe.Replay {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "probes", "lsregister-dump.txt"))
	if err != nil {
		t.Fatalf("reading the dump fixture: %v", err)
	}
	// The dump names absolute paths, and whether each one exists is what
	// decides a stale registration. Rewriting them into the fixture keeps
	// that answer independent of whatever this machine has installed.
	dump := strings.ReplaceAll(string(raw), "/Users/andrewsam/", c.Home+"/")
	dump = strings.ReplaceAll(dump, "/Applications/", c.Root+"/Applications/")
	dump = strings.ReplaceAll(dump, "/opt/homebrew/", c.Root+"/opt/homebrew/")
	records := map[string]probe.Result{
		"brew --caskroom":                 {Stdout: c.Caskroom + "\n"},
		"pkgutil --pkgs":                  {Stdout: "com.apple.pkg.CLTools\ncom.autodesk.AutoCAD2027\ncom.paloaltonetworks.globalprotect.pkg\n"},
		string(lsregisterPath) + " -dump": {Stdout: dump},
		"mdfind kMDItemContentType == 'com.apple.application-bundle'": {Stdout: ""},
		"pkgutil --pkg-info com.autodesk.AutoCAD2027": {Stdout: "package-id: com.autodesk.AutoCAD2027\nversion: 26.0.60.161\nvolume: " +
			c.Root + "/\nlocation: Applications/Autodesk/AutoCAD 2027\ninstall-time: 1779494033\n"},
		"pkgutil --pkg-info com.paloaltonetworks.globalprotect.pkg": {Stdout: "package-id: com.paloaltonetworks.globalprotect.pkg\n" +
			"version: 6.3.2-525\nvolume: " + c.Root + "/\nlocation: Applications/GlobalProtect.app\ninstall-time: 1761159020\n"},
		"pkgutil --files com.paloaltonetworks.globalprotect.pkg": {Stdout: "Applications/GlobalProtect.app\nApplications/GlobalProtect.app/Contents\n"},
	}
	// codesign answers for the two bundles whose team id the corpus needs.
	teams := map[string]string{
		"Applications/ChatGPT.app":  "2DC432GLL2",
		"Applications/OrbStack.app": "HUAQ24HBR6",
	}
	for rel, team := range teams {
		key := "codesign -dv --verbose=4 " + filepath.Join(c.Root, rel)
		records[key] = probe.Result{Stderr: "Identifier=x\nTeamIdentifier=" + team + "\n"}
	}
	return &probe.Replay{Records: records}
}

// env is the detector environment pointed at the fixture. ReadFile and Stat
// are the real ones from internal/detect, so the corpus exercises the
// sanctioned reader rather than a stand-in for it.
func (c *corpus) env(t *testing.T) detect.Env {
	t.Helper()
	return detect.Env{
		Runner:   c.replay(t),
		Home:     c.Home,
		Euid:     os.Geteuid(),
		ReadFile: detect.ReadFile,
		ReadDir:  detect.ReadDir,
		Stat:     detect.Stat,
		LookPath: func(name string) (string, error) { return name, nil },
	}
}

// detector is the apps detector with a directory reader pointed at the real
// filesystem, which inside a test is the fixture and nothing else.
func (c *corpus) detector(t *testing.T) *Detector {
	t.Helper()
	return &Detector{
		TeamCachePath: filepath.Join(t.TempDir(), "teamids.json"),
		Opts:          Options{Root: c.Root},
	}
}

// walkFixture walks the corpus the way a scan would.
func (c *corpus) walk(t *testing.T) *walk.Tree {
	t.Helper()
	tree, err := walk.Walk(context.Background(), walk.Options{Root: c.Root})
	if err != nil {
		t.Fatalf("walking the fixture: %v", err)
	}
	return tree
}

// canonical turns a fixture path into the form the expectations use: the root
// stripped and the home written as "~".
func (c *corpus) canonical(p string) string {
	if rest, ok := strings.CutPrefix(p, c.Home); ok {
		return "~" + rest
	}
	if rest, ok := strings.CutPrefix(p, c.Root); ok {
		return rest
	}
	return p
}

// contextFor is the classify context a scan of the fixture would build.
func contextFor(c *corpus) classify.Context {
	return classify.Context{Home: c.Home, CodeRoots: []string{filepath.Join(c.Home, "code")}}
}

// analyze runs the probe and the analysis over the fixture.
func (c *corpus) analyze(t *testing.T) (*Facts, *Analysis) {
	t.Helper()
	d := c.detector(t)
	raw, _ := d.Probe(context.Background(), c.env(t))
	facts, ok := raw.(*Facts)
	if !ok {
		t.Fatalf("Probe returned %T, want *Facts", raw)
	}
	cx := contextFor(c)
	opts := d.Opts
	opts.CodesignAvailable = true
	opts.Now = time.Now().Add(365 * 24 * time.Hour)
	return facts, Analyze(c.walk(t), facts, cx, opts)
}

// TestCorpusProbeFindsTheInventory is milestone A1: the probe alone, with no
// linking, has to find the applications, the casks, the receipts and the
// launch items.
func TestCorpusProbeFindsTheInventory(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	facts, _ := c.analyze(t)

	if facts.CaskroomDir != c.Caskroom {
		t.Errorf("CaskroomDir = %q, want %q", facts.CaskroomDir, c.Caskroom)
	}
	// Six tokens exist; the font cask is skipped and the other five,
	// including devtoys with no receipt fixture, are kept.
	byToken := make(map[string]*Cask, len(facts.Casks))
	for i := range facts.Casks {
		byToken[facts.Casks[i].Token] = &facts.Casks[i]
	}
	if _, bad := byToken["font-jetbrains-mono-nerd-font"]; bad {
		t.Error("a font cask was probed")
	}
	for _, token := range []string{"cursor", "arc", "stremioservice", "codex", "balenaetcher", "devtoys", "mattermost"} {
		cask, ok := byToken[token]
		if !ok {
			t.Errorf("cask %q is missing", token)
			continue
		}
		if cask.ReceiptErr != "" {
			t.Errorf("cask %q: %s", token, cask.ReceiptErr)
		}
	}
	if cursor := byToken["cursor"]; cursor == nil || !cursor.HasApp() {
		t.Error("the cursor cask should name an application artifact")
	}
	if codex := byToken["codex"]; codex == nil || !codex.BinaryOnly() {
		t.Error("the codex cask is binary-only")
	}

	// Every bundle of bundles.tsv, including the one nested three deep in
	// a publisher folder and the one with no identifier.
	ids := make(map[string]bool, len(facts.AppDirBundles))
	names := make(map[string]bool, len(facts.AppDirBundles))
	for _, b := range facts.AppDirBundles {
		ids[b.ID] = true
		names[BundleBaseName(b.Path)] = true
	}
	for _, id := range []string{
		"com.microsoft.VSCode", "com.google.Chrome", "com.google.android.studio",
		"com.westbridge.stremio5-mac", "com.openai.codex", "dev.kdrag0n.MacVirt",
		"com.electron.kontena-lens", "com.brave.Browser", "org.localsend.localsendApp",
		"com.autodesk.AutoCAD2027", "net.sourceforge.grandperspective",
	} {
		if !ids[id] {
			t.Errorf("bundle %q was not found", id)
		}
	}
	if !names["Claude Code URL Handler"] {
		t.Error("the bundle with no identifier was dropped")
	}

	// The App Store marker.
	var bitwarden BundleInfo
	for _, b := range facts.AppDirBundles {
		if b.ID == "com.bitwarden.desktop" {
			bitwarden = b
		}
	}
	if !bitwarden.MASReceipt {
		t.Error("the _MASReceipt marker was not detected")
	}

	// Receipts, with the presence ratio measured only for the one whose
	// install location is gone.
	var global, autocad Receipt
	for _, r := range facts.Receipts {
		switch r.PkgID {
		case "com.paloaltonetworks.globalprotect.pkg":
			global = r
		case "com.autodesk.AutoCAD2027":
			autocad = r
		}
	}
	if global.PkgID == "" || global.LocationExists {
		t.Errorf("GlobalProtect receipt = %+v, want a missing install location", global)
	}
	if !global.FilesChecked {
		t.Error("the file listing should run for a receipt whose location is gone")
	}
	if autocad.PkgID == "" || !autocad.LocationExists {
		t.Errorf("AutoCAD receipt = %+v, want an existing install location", autocad)
	}
	if autocad.FilesChecked {
		t.Error("the file listing must not run for a receipt whose location exists")
	}

	// Launch items, with the keystone plist reporting no program.
	byLabel := make(map[string]LaunchItem, len(facts.LaunchItems))
	for _, item := range facts.LaunchItems {
		byLabel[item.Label] = item
	}
	if len(facts.LaunchItems) != 4 {
		t.Errorf("found %d launch items, want 4: %v", len(facts.LaunchItems), byLabel)
	}
	if item, ok := byLabel["com.google.keystone.agent"]; !ok || item.Program != "" {
		t.Errorf("the keystone agent should have no readable program: %+v", item)
	}

	if len(facts.GroupContainerNames) != 6 {
		t.Errorf("found %d group containers, want 6: %v", len(facts.GroupContainerNames), facts.GroupContainerNames)
	}
}

// TestCorpusResolvesOwners is milestone A2 in one assertion: every candidate
// the corpus holds, resolved to the owner key, confidence and rule the
// expectations file names, row for row.
func TestCorpusResolvesOwners(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	got := make(map[string]string, len(a.Candidates))
	var gotRows []string
	for i, cand := range a.Candidates {
		m := a.Matches[i]
		dir := c.canonical(cand.Path)
		row := strings.Join([]string{dir, m.Owner.Key, m.Confidence.String(), m.Rule}, "\t")
		got[dir] = row
		gotRows = append(gotRows, row)
	}
	sort.Strings(gotRows)

	want := manifestLines(t, "machine1.expected.tsv")
	sort.Strings(want)

	wantDirs := make(map[string]bool, len(want))
	for _, row := range want {
		dir := row[:strings.IndexByte(row, '\t')]
		wantDirs[dir] = true
		if got[dir] == "" {
			t.Errorf("no candidate for %s", dir)
			continue
		}
		if got[dir] != row {
			t.Errorf("mismatch for %s\n got: %s\nwant: %s", dir, got[dir], row)
		}
	}
	for dir := range got {
		if !wantDirs[dir] {
			t.Errorf("unexpected candidate %s resolved as %s", dir, got[dir])
		}
	}
}

// TestCorpusSkipsAppleNames guards the line the apps detector must not cross.
// Apple's own containers belong to the rule catalog; a claim here would
// attribute macOS's data to an application, and finding no application would
// then report macOS as an orphan.
func TestCorpusSkipsAppleNames(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)
	for _, cand := range a.Candidates {
		if IsAppleName(cand.Name) {
			t.Errorf("Apple-owned %q became a candidate at %s", cand.Name, cand.Path)
		}
	}
}

// TestCorpusTrashBundleIsNotInstalled is the orphan precondition (D36). A
// bundle in the Trash is found by the backstop so the user can be told where
// it went, but it must never satisfy "this application is installed".
func TestCorpusTrashBundleIsNotInstalled(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	var trashed *Bundle
	for _, b := range a.Inventory.Bundles {
		if BundleBaseName(b.Path) == "DynamicLakePro" {
			trashed = b
		}
	}
	if trashed == nil {
		t.Fatal("the backstop did not find the bundle in the Trash")
	}
	if trashed.Source != SourceTrash {
		t.Errorf("Source = %v, want %v", trashed.Source, SourceTrash)
	}
	if trashed.Source.Installed() {
		t.Error("a bundle in the Trash must not count as installed")
	}
}

// TestCorpusCaskroomBundleCountsAsInstalled is the other half of the same
// rule: a cask that keeps its application inside the Caskroom, as the
// StremioService cask does, is installed.
func TestCorpusCaskroomBundleCountsAsInstalled(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	b, ok := a.Inventory.Installed("net.sourceforge.grandperspective")
	if !ok {
		t.Fatal("the Caskroom application was not treated as installed")
	}
	if b.Source != SourceCaskroom {
		t.Errorf("Source = %v, want %v", b.Source, SourceCaskroom)
	}
	if b.Cask == nil || b.Cask.Token != "grandperspective" {
		t.Errorf("the cask was not attached to its application: %+v", b.Cask)
	}
}

// TestCorpusDanglingCaskSymlinkIsNotInstalled is the case that broke on the
// real machine. Homebrew leaves "Cursor.app -> /Applications/Cursor.app" in
// the Caskroom after the application is dragged to the Trash. A search that
// trusted the name would report the application as installed, and the cask
// whose data is the orphan would never be flagged.
func TestCorpusDanglingCaskSymlinkIsNotInstalled(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	for _, name := range []string{"Cursor", "DevToys"} {
		if b, ok := a.Inventory.InstalledByName(name); ok {
			t.Errorf("%s was treated as installed at %s", name, b.Path)
		}
		if _, ok := a.Inventory.AnyByName(name); ok {
			t.Errorf("a dangling symlink became a bundle named %s", name)
		}
	}
	// The cask is still known, which is what makes the data attributable.
	if _, ok := a.Inventory.CaskByToken["cursor"]; !ok {
		t.Error("the cursor cask went missing")
	}
}

// TestCorpusVendorFolderBundle covers the AutoCAD shape: a publisher folder
// under /Applications holding the application two levels down.
func TestCorpusVendorFolderBundle(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	b, ok := a.Inventory.Installed("com.autodesk.AutoCAD2027")
	if !ok {
		t.Fatal("the bundle inside the publisher folder was not found")
	}
	if b.Source != SourceVendorFolder {
		t.Errorf("Source = %v, want %v", b.Source, SourceVendorFolder)
	}
}

// TestCorpusCodesignRunsOnceThenNever is the A2 acceptance criterion for the
// team id cache: the first scan reads the signatures, the second reads the
// file.
func TestCorpusCodesignRunsOnceThenNever(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	cachePath := filepath.Join(t.TempDir(), "teamids.json")

	first := c.detector(t)
	first.TeamCachePath = cachePath
	firstFacts, err := first.Probe(context.Background(), c.env(t))
	if err != nil {
		t.Logf("probe degraded, which the corpus tolerates: %v", err)
	}
	if codesignCalls(t, firstFacts) == 0 {
		t.Fatal("the first scan should have read some signatures")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("the team id cache was not written: %v", err)
	}

	second := c.detector(t)
	second.TeamCachePath = cachePath
	secondFacts, err := second.Probe(context.Background(), c.env(t))
	if err != nil {
		t.Logf("probe degraded: %v", err)
	}
	if got := codesignCalls(t, secondFacts); got != 0 {
		t.Errorf("the second scan invoked codesign %d times, want 0", got)
	}
}

// codesignCalls reads how many signatures a probe read, from the facts that
// probe produced rather than from the detector it ran on. The detector is a
// process-wide singleton and would answer for whichever scan finished last.
func codesignCalls(t *testing.T, raw detect.Facts) int {
	t.Helper()
	f, ok := raw.(*Facts)
	if !ok || f == nil {
		t.Fatalf("Probe returned %T, want *Facts", raw)
	}
	return f.CodesignCalls
}

// TestCorpusVerdicts is milestone A3's acceptance criterion: the state of
// every owner, row for row against the list the plan fixed. It is asserted
// exactly rather than as a subset, because both directions matter. An owner
// that appears in the orphan list and should not is a false accusation about
// software someone still uses; one that is missing is the whole feature
// failing quietly.
func TestCorpusVerdicts(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	got := make(map[string]string, len(a.Verdicts))
	for _, key := range a.OwnerKeys() {
		v := a.Verdicts[key]
		if v == nil {
			t.Errorf("owner %s has no verdict", key)
			continue
		}
		got[key] = strings.Join([]string{key, a.Owners[key].Owner.Label, v.State.String(), v.Confidence.String()}, "\t")
	}

	wantKeys := make(map[string]bool)
	for _, row := range manifestLines(t, "machine1.verdicts.tsv") {
		key := row[:strings.IndexByte(row, '\t')]
		wantKeys[key] = true
		switch {
		case got[key] == "":
			t.Errorf("no verdict for %s", key)
		case got[key] != row:
			t.Errorf("mismatch for %s\n got: %s\nwant: %s", key, got[key], row)
		}
	}
	for key := range got {
		if !wantKeys[key] {
			t.Errorf("unexpected owner %s: %s", key, got[key])
		}
	}
}

// TestCorpusOrphanSetIsExact states the orphan list in the terms a person
// reads it in, so a regression names the product rather than an owner key.
func TestCorpusOrphanSetIsExact(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	want := map[string]string{
		"GlobalProtect":  "likely",
		"Warp":           "likely",
		"Antigravity":    "likely",
		"Opera":          "likely",
		"Vivaldi":        "likely",
		"Microsoft Edge": "likely",
		"Chromium":       "likely",
		"Wondershare":    "likely",
		"Cap":            "likely",
		"Atlas":          "likely",
		// "possible" in the report: the only evidence is a name.
		"TabNine":     "corroborating",
		"UI Launcher": "corroborating",
		"boringNotch": "corroborating",
	}
	got := make(map[string]string)
	for _, key := range a.OwnersInState(StateOrphanLikely) {
		got[a.Owners[key].Owner.Label] = a.Verdicts[key].Confidence.String()
	}
	for label, conf := range want {
		switch {
		case got[label] == "":
			t.Errorf("%s is not in the orphan list", label)
		case got[label] != conf:
			t.Errorf("%s confidence = %s, want %s", label, got[label], conf)
		}
	}
	for label := range got {
		if _, ok := want[label]; !ok {
			t.Errorf("%s was reported as an orphan and should not be", label)
		}
	}
}

// TestCorpusNotFlagged names the software that must never reach the orphan
// list. Every entry is something a person still uses, and reporting any of
// them would cost more trust than the whole feature earns.
func TestCorpusNotFlagged(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	want := map[string]State{
		"cli:k9s":                         StateNonApp,
		"cli:lazygit":                     StateNonApp,
		"cli:zoxide":                      StateNonApp,
		"cli:gk":                          StateNonApp,
		"cli:gitkrakencli":                StateNonApp,
		"cli:turborepo":                   StateNonApp,
		"cli:fastmcp":                     StateNonApp,
		"cli:go":                          StateNonApp,
		"cli:storix":                      StateNonApp,
		"cli:codex-cli":                   StateNonApp,
		"project:mochi":                   StateOwnBuild,
		"app:com.autodesk.AutoCAD2027":    StateInstalled,
		"app:com.google.Chrome":           StateInstalled,
		"app:com.microsoft.VSCode":        StateInstalled,
		"app:com.westbridge.stremio5-mac": StateInstalled,
	}
	for key, state := range want {
		v, ok := a.Verdicts[key]
		if !ok {
			t.Errorf("%s has no verdict at all", key)
			continue
		}
		if v.State != state {
			t.Errorf("%s state = %v, want %v", key, v.State, state)
		}
	}

	// A publisher folder belongs to the publisher, not to whichever of its
	// products the alias table happens to name first, and it stays out of
	// the orphan list for as long as any of those products is installed.
	// Both halves matter: attributing ~/Library/Application Support/Google
	// to Chrome handed Chrome the whole folder, Android Studio's data
	// included, and offering it for deletion when Chrome went would have
	// taken data whose application is still on the machine.
	for _, dir := range []string{
		"~/Library/Application Support/Google",
		"~/Library/Application Support/Microsoft",
		"~/Library/Application Support/Autodesk",
	} {
		found := false
		for i, cand := range a.Candidates {
			if c.canonical(cand.Path) != dir {
				continue
			}
			found = true
			key := a.Matches[i].Owner.Key
			if a.Matches[i].Owner.Kind != KindVendor {
				t.Errorf("%s resolved to %s, want the publisher", dir, key)
			}
			if v := a.Verdicts[key]; v == nil || v.State != StateVendor {
				t.Errorf("%s is owned by %s, whose state is %v, want vendor", dir, key, v)
			}
		}
		if !found {
			t.Errorf("%s was not a candidate", dir)
		}
	}

	// A directory that names one product stays that product's, whether or
	// not the product publishes under its own reverse-DNS prefix.
	for _, dir := range []string{"~/Library/Application Support/stremio-server"} {
		for i, cand := range a.Candidates {
			if c.canonical(cand.Path) != dir {
				continue
			}
			v := a.Verdicts[a.Matches[i].Owner.Key]
			if v == nil || v.State != StateInstalled {
				t.Errorf("%s is owned by %s, whose state is %v", dir, a.Matches[i].Owner.Key, v)
			}
		}
	}
}

// TestCorpusCaskOnlyAndTrash covers the two states that are not orphans but
// still mean the application is gone.
func TestCorpusCaskOnlyAndTrash(t *testing.T) {
	t.Parallel()
	c := buildCorpus(t)
	_, a := c.analyze(t)

	var tokens []string
	for _, ck := range a.CaskOnlyCasks() {
		tokens = append(tokens, ck.Token)
	}
	want := []string{"cursor", "devtoys", "mattermost", "stremioservice"}
	if strings.Join(tokens, " ") != strings.Join(want, " ") {
		t.Errorf("cask-only tokens = %v, want %v", tokens, want)
	}

	// The stremioservice cask's application is gone, but Stremio is
	// installed and still owns the streaming-server data. A cask-only
	// cask is not the same thing as orphaned data.
	if v := a.Verdicts["app:com.westbridge.stremio5-mac"]; v == nil || v.State != StateInstalled {
		t.Errorf("Stremio should be installed despite its helper cask: %v", v)
	}

	trash := a.OwnersInState(StateInTrash)
	if len(trash) != 1 || trash[0] != "app:com.aviorrok.DynamicLakePro" {
		t.Errorf("in-trash owners = %v, want the DynamicLakePro bundle", trash)
	}
}
