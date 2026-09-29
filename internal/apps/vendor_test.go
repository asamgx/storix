package apps

import "testing"

func TestParseReverseDNS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		in       string
		ok       bool
		wantName string
		vendor   string
		platform bool
		team     string
	}{
		{name: "three labels", in: "com.openai.sky.CUAService", ok: true,
			wantName: "com.openai.sky.CUAService", vendor: "com.openai"},
		{name: "uuid suffix", in: "com.openai.chat.RemoteFeatureFlags.3F2A1B", ok: true,
			wantName: "com.openai.chat.RemoteFeatureFlags.3F2A1B", vendor: "com.openai"},
		{name: "github is a platform", in: "com.github.kmikiy.SpotMenu", ok: true,
			wantName: "com.github.kmikiy.SpotMenu", vendor: "com.github.kmikiy", platform: true},
		{name: "electron is a platform", in: "com.electron.kontena-lens", ok: true,
			wantName: "com.electron.kontena-lens", vendor: "com.electron.kontena-lens", platform: true},
		{name: "todesktop is a platform", in: "com.todesktop.230313mzl4w4u92", ok: true,
			wantName: "com.todesktop.230313mzl4w4u92", vendor: "com.todesktop.230313mzl4w4u92", platform: true},
		// A two-label identifier is its own vendor. "notion.id" and
		// "notion.id.ShipIt" have to agree on one vendor prefix, which
		// they only do if the shorter one keeps both labels.
		{name: "two labels", in: "bobko.aerospace", ok: true,
			wantName: "bobko.aerospace", vendor: "bobko.aerospace"},
		{name: "two labels notion", in: "notion.id", ok: true,
			wantName: "notion.id", vendor: "notion.id"},
		{name: "notion updater shares the vendor", in: "notion.id.ShipIt", ok: true,
			wantName: "notion.id.ShipIt", vendor: "notion.id"},
		{name: "team id prefix", in: "HUAQ24HBR6.dev.orbstack", ok: true,
			wantName: "dev.orbstack", vendor: "dev.orbstack", team: "HUAQ24HBR6"},
		{name: "group prefix stripped", in: "group.net.whatsapp.WhatsApp.shared", ok: true,
			wantName: "net.whatsapp.WhatsApp.shared", vendor: "net.whatsapp"},
		{name: "display name is not an id", in: "Smart Code ltd"},
		{name: "single capitalised word is not an id", in: "Code"},
		{name: "capitalised first label is not an id", in: "Wondershare.Installer"},
		{name: "one label", in: "storix"},
		{name: "plist file", in: "com.google.Chrome.plist"},
		{name: "empty", in: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseReverseDNS(tc.in)
			if ok != tc.ok {
				t.Fatalf("ParseReverseDNS(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if got.Vendor != tc.vendor {
				t.Errorf("Vendor = %q, want %q", got.Vendor, tc.vendor)
			}
			if got.Platform != tc.platform {
				t.Errorf("Platform = %v, want %v", got.Platform, tc.platform)
			}
			if got.TeamID != tc.team {
				t.Errorf("TeamID = %q, want %q", got.TeamID, tc.team)
			}
		})
	}
}

// TestPlatformVendorsStayApart is the invariant the platform-prefix rule
// exists for: two unrelated applications published through the same
// packaging platform must never share a vendor prefix, because the vendor
// rule would then attribute one's data to the other.
func TestPlatformVendorsStayApart(t *testing.T) {
	t.Parallel()
	lens, _ := ParseReverseDNS("com.electron.kontena-lens")
	ollama, _ := ParseReverseDNS("com.electron.ollama")
	if lens.Vendor == ollama.Vendor {
		t.Fatalf("Lens and Ollama share vendor %q", lens.Vendor)
	}
}

func TestSuffix(t *testing.T) {
	t.Parallel()
	rdns, ok := ParseReverseDNS("com.openai.sky.CUAService")
	if !ok {
		t.Fatal("parse failed")
	}
	if got := rdns.Suffix(); got != "sky.CUAService" {
		t.Errorf("Suffix = %q, want %q", got, "sky.CUAService")
	}
}

func TestTeamIDPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		team string
		rest string
		ok   bool
	}{
		{in: "HUAQ24HBR6.dev.orbstack", team: "HUAQ24HBR6", rest: "dev.orbstack", ok: true},
		{in: "2DC432GLL2.com.openai.codex.notifications", team: "2DC432GLL2",
			rest: "com.openai.codex.notifications", ok: true},
		{in: "SY64MV22J9.com.raycast.macos.shared", team: "SY64MV22J9",
			rest: "com.raycast.macos.shared", ok: true},
		// Apple's own team-id containers must not open the codesign gate.
		{in: "243LU875E5.groups.com.apple.podcasts"},
		{in: "74J34U3R6X.com.apple.iWork"},
		{in: "group.com.facebook.family"},
		{in: "com.spotify.client"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			team, rest, ok := TeamIDPrefix(tc.in)
			if ok != tc.ok || team != tc.team || rest != tc.rest {
				t.Errorf("TeamIDPrefix(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, team, rest, ok, tc.team, tc.rest, tc.ok)
			}
		})
	}
}

func TestIsAppleName(t *testing.T) {
	t.Parallel()
	apple := []string{
		"com.apple.Safari", "group.com.apple.notes", "group.is.workflow.shortcuts",
		"243LU875E5.groups.com.apple.podcasts", "74J34U3R6X.com.apple.iWork",
		"CloudDocs", "MobileSync", "App Store",
	}
	for _, name := range apple {
		if !IsAppleName(name) {
			t.Errorf("IsAppleName(%q) = false, want true", name)
		}
	}
	notApple := []string{
		"com.spotify.client", "HUAQ24HBR6.dev.orbstack", "group.net.whatsapp.WhatsApp.shared",
		"Code", "Wondershare", "group.com.facebook.family",
	}
	for _, name := range notApple {
		if IsAppleName(name) {
			t.Errorf("IsAppleName(%q) = true, want false", name)
		}
	}
}
