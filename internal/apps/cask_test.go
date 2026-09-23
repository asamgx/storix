package apps

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// readReceipt loads one of the fixture receipts, which are byte-for-byte
// copies of this machine's own. They are the reason the decoder is written
// the way it is: every awkward shape it tolerates appears in one of them.
func readReceipt(t *testing.T, token string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "caskroom", token, ".metadata", "INSTALL_RECEIPT.json"))
	if err != nil {
		t.Fatalf("reading the %s receipt: %v", token, err)
	}
	return data
}

func TestDecodeReceiptCursor(t *testing.T) {
	t.Parallel()
	c, err := DecodeReceipt("cursor", readReceipt(t, "cursor"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Contains(c.Apps, "Cursor.app") {
		t.Errorf("Apps = %v, want Cursor.app", c.Apps)
	}
	// Cursor's receipt has no uninstall.quit at all: it resolves through
	// its zap paths alone, which is exactly why the zap globs matter.
	if len(c.QuitIDs) != 0 {
		t.Errorf("QuitIDs = %v, want none", c.QuitIDs)
	}
	for _, want := range []string{
		"~/.cursor",
		"~/Library/Application Support/Cursor",
		"~/Library/Caches/com.todesktop.*",
	} {
		if !slices.Contains(c.ZapPaths, want) {
			t.Errorf("ZapPaths is missing %q: %v", want, c.ZapPaths)
		}
	}
	// The binary stanza mixes a plain string with a {"target": …} object.
	if !slices.Contains(c.Binaries, "code") || !slices.Contains(c.Binaries, "cursor") {
		t.Errorf("Binaries = %v, want both the path basename and the target", c.Binaries)
	}
	if c.InstalledAt.IsZero() {
		t.Error("InstalledAt was not decoded")
	}
	if c.Version == "" {
		t.Error("Version was not decoded")
	}
}

func TestDecodeReceiptQuitIDs(t *testing.T) {
	t.Parallel()
	arc, err := DecodeReceipt("arc", readReceipt(t, "arc"))
	if err != nil {
		t.Fatalf("decode arc: %v", err)
	}
	if !slices.Contains(arc.QuitIDs, "company.thebrowser.Browser") {
		t.Errorf("arc QuitIDs = %v", arc.QuitIDs)
	}

	// balenaEtcher's quit id is a glob over a family of helper ids.
	etcher, err := DecodeReceipt("balenaetcher", readReceipt(t, "balenaetcher"))
	if err != nil {
		t.Fatalf("decode balenaetcher: %v", err)
	}
	if _, ok := etcher.MatchesID("io.balena.etcher.helper"); !ok {
		t.Errorf("quit glob did not match a helper id: %v", etcher.QuitIDs)
	}
	if _, ok := etcher.MatchesID("io.balena.something-else.app"); ok {
		t.Error("quit glob matched an unrelated id")
	}
}

func TestDecodeReceiptBinaryOnly(t *testing.T) {
	t.Parallel()
	c, err := DecodeReceipt("codex", readReceipt(t, "codex"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !c.BinaryOnly() {
		t.Errorf("codex should be binary-only: apps=%v binaries=%v", c.Apps, c.Binaries)
	}
	// Its zap stanza uses "rmdir" rather than "trash", which an earlier
	// shape-specific decoder would have dropped.
	if !slices.Contains(c.ZapPaths, "~/.codex") {
		t.Errorf("ZapPaths = %v, want ~/.codex from the rmdir stanza", c.ZapPaths)
	}
}

func TestDecodeReceiptLaunchctl(t *testing.T) {
	t.Parallel()
	c, err := DecodeReceipt("stremioservice", readReceipt(t, "stremioservice"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Contains(c.Apps, "StremioService.app") {
		t.Errorf("Apps = %v", c.Apps)
	}
	if !slices.Contains(c.LaunchctlLabels, "com.stremio.service") {
		t.Errorf("LaunchctlLabels = %v", c.LaunchctlLabels)
	}
	if !slices.Contains(c.ZapPaths, "~/Library/Application Support/stremio-server") {
		t.Errorf("ZapPaths = %v", c.ZapPaths)
	}
}

func TestDecodeReceiptBroken(t *testing.T) {
	t.Parallel()
	c, err := DecodeReceipt("mattermost", []byte("{this is not json"))
	if err == nil {
		t.Fatal("a broken receipt should report an error")
	}
	// The cask is still usable: its token is the evidence that something
	// was installed, which is most of what the receipt was for.
	if c.Token != "mattermost" {
		t.Errorf("Token = %q, want mattermost", c.Token)
	}
	if c.ReceiptErr == "" {
		t.Error("ReceiptErr was not recorded")
	}
	if len(c.Apps) != 0 || len(c.ZapPaths) != 0 {
		t.Error("a broken receipt must yield no artifacts")
	}
}

func TestDecodeReceiptUnfamiliarShapes(t *testing.T) {
	t.Parallel()
	// Nothing here is a shape Homebrew writes today. The point is that one
	// unfamiliar stanza costs that stanza and not the whole receipt.
	raw := []byte(`{
	  "time": 1700000000,
	  "source": {"version": "9.9"},
	  "uninstall_artifacts": [
	    {"app": [{"target": "Renamed.app"}, "Sub/Dir/Plain.app"]},
	    {"uninstall": {"quit": "com.example.single"}},
	    {"zap": [{"trash": "~/Library/Caches/com.example"}, {"unknown_stanza": {"deep": [1, 2]}}]},
	    {"future_artifact": {"anything": true}}
	  ]
	}`)
	c, err := DecodeReceipt("example", raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Contains(c.Apps, "Renamed.app") || !slices.Contains(c.Apps, "Plain.app") {
		t.Errorf("Apps = %v, want both the target object and the path basename", c.Apps)
	}
	if !slices.Contains(c.QuitIDs, "com.example.single") {
		t.Errorf("QuitIDs = %v, want the bare-object uninstall stanza", c.QuitIDs)
	}
	if !slices.Contains(c.ZapPaths, "~/Library/Caches/com.example") {
		t.Errorf("ZapPaths = %v, want the string-valued trash stanza", c.ZapPaths)
	}
}

func TestMatchesPath(t *testing.T) {
	t.Parallel()
	c, err := DecodeReceipt("cursor", readReceipt(t, "cursor"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	const home = "/Users/andrewsam"
	tests := []struct {
		path string
		want bool
	}{
		{"/Users/andrewsam/Library/Caches/com.todesktop.230313mzl4w4u92", true},
		{"/Users/andrewsam/Library/Application Support/Cursor", true},
		// A zapped directory owns what is under it.
		{"/Users/andrewsam/Library/Application Support/Cursor/User/settings.json", true},
		{"/Users/andrewsam/.cursor", true},
		{"/Users/andrewsam/Library/Caches/com.microsoft.VSCode", false},
		// A glob must not cross a directory boundary.
		{"/Users/andrewsam/Library/Caches/com.todesktop.abc/nested", true},
		{"/Users/andrewsam/Library/Preferences/com.google.Chrome.plist", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			_, got := c.MatchesPath(tc.path, home)
			if got != tc.want {
				t.Errorf("MatchesPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsFontCask(t *testing.T) {
	t.Parallel()
	if !IsFontCask("font-jetbrains-mono-nerd-font") {
		t.Error("font cask not recognised")
	}
	if IsFontCask("fontforge") {
		t.Error("fontforge is not a font cask")
	}
}

// TestAnUnexpectedScalarTypeCostsOneFieldNotTheReceipt is finding 7.
//
// Homebrew writes the install time as an integer, and a receipt rewritten by
// anything that round-trips JSON through a single number type carries it as a
// float. Decoding the document in one call meant that one float failed the
// whole receipt — and a cask receipt is where Cursor's zap paths live, which
// is the only thing that attributes an opaque ToDesktop identifier to Cursor.
func TestAnUnexpectedScalarTypeCostsOneFieldNotTheReceipt(t *testing.T) {
	t.Parallel()
	const body = `{
	  "source": {"version": "1.2.3"},
	  "uninstall_artifacts": [
	    {"uninstall": [{"quit": "com.todesktop.230313mzl4w4u92"}]},
	    {"zap": [{"trash": ["~/Library/Application Support/Cursor"]}]}
	  ]`

	cases := []struct {
		name string
		time string
		want int64
	}{
		{"an integer, as Homebrew writes it", `1753303598`, 1753303598},
		{"a float, as a round trip leaves it", `1753303598.0`, 1753303598},
		{"a float with a fraction", `1753303598.75`, 1753303598},
		{"a string", `"1753303598"`, 1753303598},
		{"a type nothing can read as a time", `{"epoch": 1753303598}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := DecodeReceipt("cursor", []byte(body+`,"time": `+tc.time+"}"))
			if err != nil {
				t.Fatalf("DecodeReceipt: %v", err)
			}
			if c.ReceiptErr != "" {
				t.Errorf("ReceiptErr = %q, want the receipt kept", c.ReceiptErr)
			}
			// Whatever the time did, the fields that attribute data survive.
			if len(c.ZapPaths) != 1 || c.ZapPaths[0] != "~/Library/Application Support/Cursor" {
				t.Errorf("ZapPaths = %v, want the zap path the attribution needs", c.ZapPaths)
			}
			if len(c.QuitIDs) != 1 {
				t.Errorf("QuitIDs = %v, want the quit id", c.QuitIDs)
			}
			if c.Version != "1.2.3" {
				t.Errorf("Version = %q", c.Version)
			}
			switch {
			case tc.want == 0 && !c.InstalledAt.IsZero():
				t.Errorf("InstalledAt = %v, want zero when the time cannot be read", c.InstalledAt)
			case tc.want != 0 && c.InstalledAt.Unix() != tc.want:
				t.Errorf("InstalledAt = %v, want unix %d", c.InstalledAt, tc.want)
			}
		})
	}
}

// TestAScalarWhereAnObjectWasExpectedCostsOneField is the same rule for the
// other fields: a "source" that is a bare string loses the version and nothing
// else.
func TestAScalarWhereAnObjectWasExpectedCostsOneField(t *testing.T) {
	t.Parallel()
	c, err := DecodeReceipt("cursor", []byte(
		`{"source": "https://example.invalid/cursor.dmg", "time": 1753303598,
		  "uninstall_artifacts": [{"app": ["Cursor.app"]}]}`))
	if err != nil {
		t.Fatalf("DecodeReceipt: %v", err)
	}
	if c.ReceiptErr != "" {
		t.Errorf("ReceiptErr = %q, want the receipt kept", c.ReceiptErr)
	}
	if len(c.Apps) != 1 || c.Apps[0] != "Cursor.app" {
		t.Errorf("Apps = %v, want the artifact that survived", c.Apps)
	}
	if c.InstalledAt.Unix() != 1753303598 {
		t.Errorf("InstalledAt = %v", c.InstalledAt)
	}
	if c.Version != "" {
		t.Errorf("Version = %q, want empty: the field could not be read", c.Version)
	}
}

// TestADocumentThatIsNotAnObjectIsStillAnError keeps the one case that must
// fail: a receipt that is not JSON at all tells the caller nothing, and the
// cask matches on its token alone.
func TestADocumentThatIsNotAnObjectIsStillAnError(t *testing.T) {
	t.Parallel()
	c, err := DecodeReceipt("cursor", []byte("not json"))
	if err == nil {
		t.Fatal("a receipt that is not JSON decoded without error")
	}
	if c.Token != "cursor" || c.ReceiptErr == "" {
		t.Errorf("cask = %+v, want the token and the reason", c)
	}
}
