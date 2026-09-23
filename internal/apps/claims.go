package apps

import (
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/walk"
)

// Claims turns the analysis into claims the classification engine resolves
// against the rule catalog and the other detectors.
//
// Two buckets are claimed. Bucket 2 holds application bundles wherever they
// are installed, plus the Caskroom directories Homebrew keeps them in.
// Bucket 3 holds the Library data each application owns. Nothing else is
// claimed: the bytes a dev or container detector understands better are left
// to it, and this package still emits its own claim there so the engine's
// conflict log shows whether the two agree about the owner.
//
// Every claim carries [classify.SourceApps], which loses to a detector and
// beats a catalog rule. That is the whole precedence story: a rule saying
// "~/Library/Caches/{name} is a cache owned by {name}" is a floor, and a
// claim saying "that name is Cursor, whose application is gone" replaces it.
func (a *Analysis) Claims() []classify.Claim {
	var out []classify.Claim
	out = append(out, a.bundleClaims()...)
	out = append(out, a.caskClaims()...)
	out = append(out, a.dataClaims()...)
	return out
}

// bundleClaims puts every installed application bundle in bucket 2.
func (a *Analysis) bundleClaims() []classify.Claim {
	var out []classify.Claim
	for _, b := range a.Inventory.Bundles {
		if b.Node == nil || !b.Source.Installed() {
			continue
		}
		key, label := a.bundleOwner(b)
		ev := []string{"Info.plist CFBundleIdentifier=" + b.ID + " at " + b.Path}
		if b.ID == "" {
			ev = []string{"application bundle at " + b.Path + " with no readable identifier"}
		}
		if b.Cask != nil {
			ev = append(ev, "installed by the Homebrew cask "+b.Cask.Token)
		}
		if b.MASReceipt {
			ev = append(ev, "carries an App Store receipt")
		}
		out = append(out, a.claim(b.Node, classify.BucketApps, bundleCategory(b),
			label, a.ownerKeys(key, b.ID, b.TeamID), classify.UserData,
			classify.Strong, "apps/bundle", ev))
	}
	return out
}

// bundleCategory names what sort of installation a bundle is, which is the
// sub-heading the Applications bucket groups by.
func bundleCategory(b *Bundle) string {
	switch {
	case b.MASReceipt:
		return "App Store"
	case b.Source == SourceCaskroom:
		return "Homebrew cask"
	case b.Source == SourceVendorFolder:
		return "Vendor folder"
	case b.Source == SourceSetapp:
		return "Setapp"
	case b.Cask != nil:
		return "Homebrew cask"
	default:
		return "Application"
	}
}

// caskClaims puts each Caskroom token directory in bucket 2.
//
// The Caskroom belongs to apps rather than to the homebrew detector (R3),
// because the receipt inside it is what says which application the token
// installed. Homebrew keeps the Cellar, its cache, its logs and its taps; the
// Caskroom is an application directory that happens to live under /opt.
func (a *Analysis) caskClaims() []classify.Claim {
	var out []classify.Claim
	for _, c := range a.Inventory.Casks {
		n, ok := lookupDisplay(a.Tree, c.Dir)
		if !ok {
			continue
		}
		key, label := "cask:"+c.Token, c.Token
		category, reclaim := "Homebrew cask", classify.UserData
		if p, found := defaultIndex.LookupCask(c.Token); found {
			label = p.Label
		}
		ev := []string{"Homebrew cask " + c.Token}
		if !c.InstalledAt.IsZero() {
			ev[0] += ", installed " + c.InstalledAt.Format("2006-01-02")
		}
		switch {
		case c.BinaryOnly():
			// A cask that installs a command-line binary is not a
			// missing application: it never had one.
			key, category = "cli:"+c.Token, "Homebrew cask (binary)"
			ev = append(ev, "installs a binary, not an application")
		case c.ReceiptErr != "":
			ev = append(ev, "its install receipt could not be read: "+c.ReceiptErr)
		}
		// A cask whose application is gone is reclaimable: the directory
		// is a stub and a receipt.
		if v, found := a.Verdicts[key]; found && v.State.Reclaimable() {
			reclaim = classify.Orphaned
			ev = append(ev, v.Evidence...)
		}
		out = append(out, a.claim(n, classify.BucketApps, category, label,
			[]string{key}, reclaim, classify.Strong, "apps/cask", ev))
	}
	return out
}

// dataClaims puts each resolved candidate in bucket 3.
func (a *Analysis) dataClaims() []classify.Claim {
	out := make([]classify.Claim, 0, len(a.Candidates))
	for i, c := range a.Candidates {
		if c.Node == nil {
			continue
		}
		m := a.Matches[i]
		v := a.Verdicts[m.Owner.Key]

		category := c.Loc.Category
		if c.Category != "" {
			category = c.Category
		}
		if m.Category != "" {
			category = m.Category
		}

		ev := m.Evidence
		if v != nil {
			ev = append(append([]string{}, ev...), v.Evidence...)
			for _, k := range v.Keep {
				ev = append(ev, "keep signal: "+k)
			}
		}
		label := m.Owner.Label
		if v != nil && v.State != StateInstalled {
			label += " (" + v.State.String() + ")"
		}
		out = append(out, a.claim(c.Node, classify.BucketAppData, category, label,
			a.ownerKeys(m.Owner.Key, "", ""), a.reclaimFor(c, m, v),
			m.Confidence, m.Rule, ev))
	}
	return out
}

// reclaimFor decides how safely a candidate's bytes could be freed.
//
// The location's default answers it for software that is still here: a cache
// is regenerable whoever owns it, a container is the user's data. What the
// verdict changes is the case where the software is gone, and there the
// answer is the same whatever the directory was for.
//
// A claim carries one tag for its whole subtree, which is the coarsest part
// of this design and a known limitation rather than an oversight. A directory
// that mixes regenerable and irreplaceable bytes is reported as whichever the
// claim says, and the report cannot split it: OrbStack's group container
// shows all 18.76 GB as tool-managed even though only part of it is. Splitting
// a claim would mean a per-node tag rather than a per-claim one, which is a
// change to the engine's model and to the cache format, so it is recorded
// here and left to a later milestone. The conservative direction is the one
// already taken everywhere a choice exists: Unknown rather than a guess, and
// UserData rather than Regenerable.
func (a *Analysis) reclaimFor(c Candidate, m Match, v *Verdict) classify.Reclaim {
	if v != nil && v.State.Reclaimable() {
		return classify.Orphaned
	}
	if v != nil && v.State == StateUnknown {
		return classify.Unknown
	}
	if m.HasReclaim {
		return m.Reclaim
	}
	return c.Loc.Reclaim
}

// bundleOwner is the owner key and label for an installed bundle.
func (a *Analysis) bundleOwner(b *Bundle) (key, label string) {
	key, label = "app:"+b.ID, b.DisplayName
	if b.ID == "" {
		key = "app:name:" + BundleBaseName(b.Path)
	}
	if p, ok := defaultIndex.LookupID(b.ID); ok {
		label = p.Label
	} else if p, ok := defaultIndex.LookupName(label); ok {
		label = p.Label
	}
	return key, label
}

// ownerKeys builds the join keys a claim carries.
//
// The primary key comes first and the rest are the other identities the same
// owner answers to, because a footprint joins on any of them: a detector that
// knows only "app:dev.kdrag0n.MacVirt" and a group container that knows only
// "team:HUAQ24HBR6" have to land on one owner.
func (a *Analysis) ownerKeys(primary, bundleID, teamID string) []string {
	keys := []string{primary}
	if bundleID != "" && primary != "app:"+bundleID {
		keys = append(keys, "app:"+bundleID)
	}
	if teamID != "" {
		keys = append(keys, "team:"+teamID)
	}
	if o, ok := a.Owners[primary]; ok {
		if o.Owner.Slug != "" {
			keys = append(keys, "product:"+o.Owner.Slug)
		}
		for _, id := range o.IDs {
			if k := "app:" + id; k != primary {
				keys = append(keys, k)
			}
		}
	}
	dedupeInPlace(&keys)
	return keys
}

// claim assembles one claim. Depth and Literals are the specificity the
// engine breaks ties with: an apps claim is always a fully literal path, so
// the two are the same number and a deeper claim wins, which is what makes a
// claim on "Google/Chrome" beat one on "Google".
func (a *Analysis) claim(n *walk.Node, bucket classify.Bucket, category, owner string,
	keys []string, reclaim classify.Reclaim, conf classify.Confidence,
	rule string, ev []string) classify.Claim {
	depth := uint16(strings.Count(strings.Trim(n.Display(), "/"), "/") + 1)
	return classify.Claim{
		Node:       n,
		Bucket:     bucket,
		Category:   category,
		Owner:      owner,
		OwnerKeys:  keys,
		Reclaim:    reclaim,
		Confidence: conf,
		Source:     classify.Source{Kind: classify.SourceApps, ID: rule},
		Evidence:   ev,
		Depth:      depth,
		Literals:   depth,
	}
}
