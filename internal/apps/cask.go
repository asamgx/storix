package apps

import (
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Cask is one Homebrew cask as its install receipt describes it.
//
// The receipt is the linking evidence this package leans on hardest: it names
// the application a token installed, the bundle ids that application answers
// to, and the data paths uninstalling it would remove. That is enough to
// attribute a directory like ~/Library/Caches/com.todesktop.230313mzl4w4u92
// to Cursor without the application being installed at all.
type Cask struct {
	Token   string `json:"token"`
	Version string `json:"version,omitempty"`
	// Dir is the display path of <caskroom>/<token>.
	Dir string `json:"dir,omitempty"`
	// Apps are the basenames of the application artifacts.
	Apps []string `json:"apps,omitempty"`
	// QuitIDs are bundle ids from uninstall.quit and zap.quit. They may be
	// globs ("io.balena.etcher.*").
	QuitIDs []string `json:"quitIds,omitempty"`
	// Binaries are the basenames of binary artifacts. A cask with binaries
	// and no application is command-line software, not a missing bundle.
	Binaries []string `json:"binaries,omitempty"`
	// Pkgs are installer package names.
	Pkgs []string `json:"pkgs,omitempty"`
	// PkgutilIDs are receipt ids the uninstall stanza forgets.
	PkgutilIDs []string `json:"pkgutilIds,omitempty"`
	// LaunchctlLabels are launchd labels the cask manages.
	LaunchctlLabels []string `json:"launchctlLabels,omitempty"`
	// ZapPaths are the data paths "brew uninstall --zap" would remove,
	// tilde-relative and possibly globbed.
	ZapPaths []string `json:"zapPaths,omitempty"`
	// DeletePaths are the paths the plain uninstall stanza removes. They
	// carry the same attribution weight as a zap path.
	DeletePaths []string `json:"deletePaths,omitempty"`
	// InstalledAt is the receipt's install time.
	InstalledAt time.Time `json:"installedAt,omitempty"`
	// ReceiptErr is why the receipt could not be read, empty when it was.
	// It is a string rather than an error so Facts round-trips through the
	// cache; a cask with it set matches on its token alone.
	ReceiptErr string `json:"receiptErr,omitempty"`
}

// HasApp reports whether the cask installs an application bundle.
func (c *Cask) HasApp() bool { return len(c.Apps) > 0 }

// BinaryOnly reports whether the cask is command-line software: binaries and
// no application. Such a cask is a non-app owner, never a missing bundle.
func (c *Cask) BinaryOnly() bool { return len(c.Apps) == 0 && len(c.Binaries) > 0 }

// receiptDoc is the tolerant shape of INSTALL_RECEIPT.json.
//
// Only three fields are read and each artifact element stays a RawMessage
// until its key is known, because Homebrew's artifact stanzas are a union of
// shapes: "binary" is an array mixing strings and {"target": …} objects,
// "quit" is a string here and an array there, "rmdir" appears where "trash"
// was expected. Decoding into fixed types would fail the whole receipt over
// one unfamiliar stanza, and a failed receipt is a lost attribution.
type receiptDoc struct {
	Time   int64 `json:"time"`
	Source struct {
		Version string `json:"version"`
	} `json:"source"`
	UninstallArtifacts []map[string]json.RawMessage `json:"uninstall_artifacts"`
}

// DecodeReceipt parses one cask install receipt. A receipt that cannot be
// decoded at all yields a cask carrying only its token and ReceiptErr set:
// the caller still knows the cask is installed, which is most of the value.
func DecodeReceipt(token string, data []byte) (Cask, error) {
	c := Cask{Token: token}
	var doc receiptDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		c.ReceiptErr = err.Error()
		return c, err
	}
	c.Version = doc.Source.Version
	if doc.Time > 0 {
		c.InstalledAt = time.Unix(doc.Time, 0).UTC()
	}
	for _, artifact := range doc.UninstallArtifacts {
		for key, raw := range artifact {
			c.absorb(key, raw)
		}
	}
	dedupeInPlace(&c.Apps, &c.QuitIDs, &c.Binaries, &c.Pkgs, &c.PkgutilIDs,
		&c.LaunchctlLabels, &c.ZapPaths, &c.DeletePaths)
	return c, nil
}

// absorb folds one top-level artifact stanza into the cask.
func (c *Cask) absorb(key string, raw json.RawMessage) {
	switch key {
	case "app":
		for _, s := range decodeStrings(raw) {
			// "AeroSpace-v0.19.2-Beta/AeroSpace.app" names the app
			// inside the downloaded folder; only the basename is
			// what lands in /Applications.
			c.Apps = append(c.Apps, path.Base(s))
		}
	case "binary":
		for _, s := range decodeStrings(raw) {
			c.Binaries = append(c.Binaries, path.Base(s))
		}
	case "pkg":
		for _, s := range decodeStrings(raw) {
			c.Pkgs = append(c.Pkgs, path.Base(s))
		}
	case "uninstall", "zap", "preflight", "postflight", "uninstall_preflight":
		c.absorbStanzas(key == "zap", raw)
	}
}

// absorbStanzas walks the objects inside an "uninstall" or "zap" array.
func (c *Cask) absorbStanzas(isZap bool, raw json.RawMessage) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		// A stanza given as a bare object rather than an array.
		elems = []json.RawMessage{raw}
	}
	for _, elem := range elems {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(elem, &obj); err != nil {
			continue
		}
		for key, val := range obj {
			switch key {
			case "quit":
				c.QuitIDs = append(c.QuitIDs, decodeStrings(val)...)
			case "launchctl":
				c.LaunchctlLabels = append(c.LaunchctlLabels, decodeStrings(val)...)
			case "pkgutil":
				c.PkgutilIDs = append(c.PkgutilIDs, decodeStrings(val)...)
			case "trash", "delete", "rmdir":
				if isZap {
					c.ZapPaths = append(c.ZapPaths, decodeStrings(val)...)
				} else {
					c.DeletePaths = append(c.DeletePaths, decodeStrings(val)...)
				}
			}
		}
	}
}

// decodeStrings flattens a value that Homebrew writes as a string, an array
// of strings, an array mixing strings with {"target": …} objects, or a nested
// array. Anything else yields nothing rather than an error, which is what
// keeps one unfamiliar shape from costing the whole receipt.
func decodeStrings(raw json.RawMessage) []string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err == nil {
		var out []string
		for _, e := range elems {
			out = append(out, decodeStrings(e)...)
		}
		return out
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		// {"target": "cursor"} names the symlink Homebrew creates;
		// {"path": …} appears in a few older receipts.
		for _, key := range []string{"target", "path", "script", "executable"} {
			if v, ok := obj[key]; ok {
				return decodeStrings(v)
			}
		}
	}
	return nil
}

// dedupeInPlace removes duplicates from each list, preserving order. The
// lists are a handful of entries long, so the quadratic scan is free and the
// stable order keeps the report and the goldens deterministic.
func dedupeInPlace(lists ...*[]string) {
	for _, l := range lists {
		src := *l
		if len(src) < 2 {
			continue
		}
		out := src[:0]
		seen := make(map[string]bool, len(src))
		for _, s := range src {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
		*l = out
	}
}

// MatchesID reports whether the cask's quit ids cover a bundle id, and which
// pattern did it. Quit ids may be globs: balenaEtcher's is "io.balena.etcher.*".
func (c *Cask) MatchesID(id string) (string, bool) {
	for _, q := range c.QuitIDs {
		if q == id {
			return q, true
		}
		if ok, _ := path.Match(q, id); ok {
			return q, true
		}
		// A quit id of "io.balena.etcher.*" names the application and
		// its helpers together, so the base identifier is a member of
		// the family even though the glob does not spell it out.
		if base, ok := strings.CutSuffix(q, ".*"); ok && base == id {
			return q, true
		}
	}
	return "", false
}

// MatchesPath reports whether one of the cask's data paths covers a display
// path, and which pattern did it. Patterns are tilde-relative and may glob a
// whole family of identifiers, which is how "~/Library/Caches/com.todesktop.*"
// attributes an opaque ToDesktop id to Cursor.
func (c *Cask) MatchesPath(displayPath, home string) (string, bool) {
	for _, list := range [][]string{c.ZapPaths, c.DeletePaths} {
		for _, p := range list {
			if matchZapPath(p, displayPath, home) {
				return p, true
			}
		}
	}
	return "", false
}

// matchZapPath compares one tilde-relative pattern against a display path.
// The comparison is per segment so that a glob never crosses a directory
// boundary, and a pattern that names a directory also matches everything
// under it: zapping "~/Library/Application Support/Cursor" owns its contents.
func matchZapPath(pattern, displayPath, home string) bool {
	pat := expandTilde(pattern, home)
	if pat == "" {
		return false
	}
	patSegs := strings.Split(strings.Trim(filepath.Clean(pat), "/"), "/")
	pathSegs := strings.Split(strings.Trim(filepath.Clean(displayPath), "/"), "/")
	if len(pathSegs) < len(patSegs) {
		return false
	}
	for i, seg := range patSegs {
		if seg == pathSegs[i] {
			continue
		}
		if ok, _ := path.Match(seg, pathSegs[i]); !ok {
			return false
		}
	}
	return true
}

// expandTilde replaces a leading "~" with home. A pattern with no tilde is
// already absolute or is a bare name, and is returned unchanged.
func expandTilde(p, home string) string {
	if home == "" {
		home = "/Users/unknown"
	}
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return path.Join(home, p[2:])
	}
	return p
}

// ZapNames are the leaf names of the cask's data paths that carry no glob, in
// the form a candidate directory would have. They let a cask claim
// "~/Library/Application Support/Cursor" by name as well as by path.
func (c *Cask) ZapNames() []string {
	var out []string
	for _, list := range [][]string{c.ZapPaths, c.DeletePaths} {
		for _, p := range list {
			if strings.ContainsAny(p, "*?") {
				continue
			}
			out = append(out, path.Base(p))
		}
	}
	dedupeInPlace(&out)
	return out
}

// AppNames are the cask's application basenames without the ".app" suffix,
// which is the form the inventory indexes display names under.
func (c *Cask) AppNames() []string {
	out := make([]string, 0, len(c.Apps))
	for _, a := range c.Apps {
		out = append(out, strings.TrimSuffix(a, ".app"))
	}
	return out
}

// IsFontCask reports whether a token names a font cask. Fonts install into
// ~/Library/Fonts and own no application data, so probing them is wasted work.
func IsFontCask(token string) bool { return strings.HasPrefix(token, "font-") }
