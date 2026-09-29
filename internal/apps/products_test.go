package apps

import (
	"strings"
	"testing"
)

// TestProductTableHasNoCollisions is the invariant the whole alias table
// rests on: an identifier or a name may mean exactly one product. A
// collision would silently attribute one application's data to another,
// which is the worst failure this package can have, so it is a build-time
// error rather than a last-write-wins.
func TestProductTableHasNoCollisions(t *testing.T) {
	t.Parallel()
	if _, err := newProductIndex(Products); err != nil {
		t.Fatalf("alias table: %v", err)
	}
}

func TestProductLookupByID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   string
		slug string
	}{
		{"com.microsoft.VSCode", "vscode"},
		{"com.google.Chrome", "chrome"},
		{"com.google.Keystone.Agent", "chrome"},
		{"com.google.GoogleUpdater.wake", "chrome"},
		{"com.google.android.studio", "android-studio"},
		{"com.openai.codex", "chatgpt"},
		{"com.openai.chat.RemoteFeatureFlags", "chatgpt"},
		{"com.openai.sky.CUAService", "chatgpt"},
		{"com.todesktop.230313mzl4w4u92", "cursor"},
		{"com.electron.kontena-lens", "lens"},
		{"dev.kdrag0n.MacVirt", "orbstack"},
		{"dev.warp.Warp-Stable", "warp"},
		{"com.wondershare.mac-drfoneframe", "wondershare"},
		{"so.cap.desktop", "cap"},
		{"com.paloaltonetworks.GlobalProtect.client", "globalprotect"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			p, ok := defaultIndex.LookupID(tc.id)
			if !ok {
				t.Fatalf("LookupID(%q) found nothing", tc.id)
			}
			if p.Slug != tc.slug {
				t.Errorf("LookupID(%q) = %q, want %q", tc.id, p.Slug, tc.slug)
			}
		})
	}
}

func TestProductLookupByName(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, slug string }{
		{"Code", "vscode"},
		{"GoogleUpdater", "chrome"},
		{"Keystone", "chrome"},
		{"Brave-Browser", "brave"},
		{"Smart Code ltd", "stremio"},
		{"stremio-server", "stremio"},
		{"ChatGPTHelper", "chatgpt"},
		{"lens-desktop", "lens"},
		{"Wondershare", "wondershare"},
		{"PaloAltoNetworks", "globalprotect"},
		{".antigravity", "antigravity"},
		{".cursor", "cursor"},
		// A directory name that carries the release still resolves.
		{"AndroidStudio2025.1.3", "android-studio"},
		// Spaces are matched both kept and removed.
		{"beyondcompare 5", "beyond-compare"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, ok := defaultIndex.LookupName(tc.name)
			if !ok {
				t.Fatalf("LookupName(%q) found nothing", tc.name)
			}
			if p.Slug != tc.slug {
				t.Errorf("LookupName(%q) = %q, want %q", tc.name, p.Slug, tc.slug)
			}
		})
	}
}

// TestPublisherFoldersAreNotProductNames is the invariant behind the split
// between vendorDirs and the alias table.
//
// "Google" holds Chrome's updater, Android Studio's caches and whatever Google
// ships next. While it was also one of Chrome's names, the alias table
// answered "Google" with Chrome, and Chrome was handed the whole 2.68 GB
// folder — Android Studio's live data with it. A publisher folder therefore
// has exactly one meaning, and it is the publisher.
func TestPublisherFoldersAreNotProductNames(t *testing.T) {
	t.Parallel()
	for folder := range vendorDirs {
		if p, ok := defaultIndex.LookupName(folder); ok {
			t.Errorf("the publisher folder %q is also a name of %q; "+
				"one of the two lists has to give it up", folder, p.Slug)
		}
	}
}

// TestPublisherFoldersHaveAPrefix keeps the second half of the bargain. A
// publisher folder is kept while the publisher still has something installed,
// and the reverse-DNS prefix is what answers that; a folder without one would
// be kept or discarded by its name, which is what this whole split exists to
// stop.
func TestPublisherFoldersHaveAPrefix(t *testing.T) {
	t.Parallel()
	for folder, prefix := range vendorDirs {
		if _, ok := ParseReverseDNS(prefix); !ok {
			t.Errorf("the publisher folder %q maps to %q, which is not a reverse-DNS prefix", folder, prefix)
		}
		if folder != strings.ToLower(folder) {
			t.Errorf("the publisher folder key %q is not lower case, so it will never match", folder)
		}
	}
}

// TestProductNegatives covers the attributions that must not happen. Each one
// is a pair that shares a vendor or a packaging platform, and each one would
// have been wrong under a naive prefix rule.
func TestProductNegatives(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id      string
		notSlug string
	}{
		{"com.google.antigravity", "chrome"},
		{"com.openai.atlas", "chatgpt"},
		{"com.openai.atlas.web", "chatgpt"},
		{"com.electron.kontena-lens", "ollama"},
		{"com.electron.ollama", "lens"},
		{"com.microsoft.edgemac", "vscode"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			p, ok := defaultIndex.LookupID(tc.id)
			if !ok {
				t.Fatalf("LookupID(%q) found nothing", tc.id)
			}
			if p.Slug == tc.notSlug {
				t.Errorf("LookupID(%q) resolved to %q, which it must never do", tc.id, tc.notSlug)
			}
		})
	}
}

// TestDistinctProductsAreMarked guards the rows whose whole purpose is to
// refuse a sibling vendor's identity.
func TestDistinctProductsAreMarked(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{
		"antigravity", "atlas", "cursor", "warp", "opera", "vivaldi", "edge",
		"chromium", "wondershare", "tabnine", "cap", "globalprotect",
		"mattermost", "devtoys", "dynamiclakepro", "ui-launcher", "boringnotch",
	} {
		t.Run(slug, func(t *testing.T) {
			t.Parallel()
			for _, p := range defaultIndex.all {
				if p.Slug == slug {
					if !p.Distinct {
						t.Errorf("product %q must be Distinct", slug)
					}
					return
				}
			}
			t.Errorf("product %q is missing from the table", slug)
		})
	}
}

func TestNonAppNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"k9s", "lazygit", "zoxide", "gk", "turborepo", "fastmcp", "go", "storix"} {
		if !defaultIndex.IsNonApp(name) {
			t.Errorf("IsNonApp(%q) = false, want true", name)
		}
	}
	if !defaultIndex.IsNonApp("com.segment.storage.abc") {
		t.Error("IsNonApp did not match the com.segment.storage.* family")
	}
	if defaultIndex.IsNonApp("Wondershare") {
		t.Error("Wondershare is an application family, not command-line software")
	}
}

func TestProductKey(t *testing.T) {
	t.Parallel()
	p, ok := defaultIndex.LookupID("dev.warp.Warp-Stable")
	if !ok {
		t.Fatal("Warp is missing from the table")
	}
	if got := p.Key(); got != "product:warp" {
		t.Errorf("Key = %q, want %q", got, "product:warp")
	}
	cli, ok := defaultIndex.LookupName(".codex")
	if !ok {
		t.Fatal("the Codex CLI is missing from the table")
	}
	if got := cli.Key(); got != "cli:codex-cli" {
		t.Errorf("Key = %q, want %q", got, "cli:codex-cli")
	}
}
