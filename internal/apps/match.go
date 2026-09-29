package apps

import (
	"strconv"
	"strings"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/walk"
)

// Owner is who a directory belongs to.
type Owner struct {
	// Key is the prefixed join key every footprint groups on:
	// "app:<bundleid>", "product:<slug>", "cask:<token>", "vendor:<prefix>",
	// "cli:<name>", "project:<name>", "unknown:<dirname>".
	Key string
	// Kind is what sort of thing the owner is.
	Kind OwnerKind
	// Label is what the report prints.
	Label string
	// Slug is the alias-table row behind the owner, empty when there is none.
	Slug string
}

// Match is one candidate's resolution.
type Match struct {
	Owner      Owner
	Confidence classify.Confidence
	// Rule names the step that resolved the candidate, for the why panel
	// and the acceptance table: "apps/bundle-id", "apps/cask-zap", …
	Rule string
	// Evidence are verbatim lines, in the order the steps produced them.
	Evidence []string
	// Category overrides the location's category, which the updater rule
	// uses to relabel a cache as an updater cache.
	Category string
	// Reclaim overrides the location's default, same rule.
	Reclaim    classify.Reclaim
	HasReclaim bool
	// IDMatch records that the evidence was a reverse-DNS identifier
	// rather than a display name. Only an installation puts an identifier
	// on disk, so it is the difference between an orphan verdict that is
	// likely and one that is only possible.
	IDMatch bool
}

// Candidate is one directory or file whose owner is in question: a direct
// child of an application data location, or a child of a vendor folder.
type Candidate struct {
	// Name is the normalized name: the identifier or display name, with
	// the file extension of a preference or saved-state file removed.
	Name string
	// RawName is the name on disk.
	RawName string
	// Path is the display path.
	Path string
	// Loc is the location the candidate was found under.
	Loc Location
	// Category overrides the location's, for a vendor folder child.
	Category string
	// Vendor is the publisher folder this candidate sits in, empty for a
	// direct child of a location.
	Vendor string
	// Node is the candidate's node; nil when the walk did not retain it.
	Node *walk.Node
}

// Bytes is the candidate's size, zero when it has no node.
func (c Candidate) Bytes() int64 {
	if c.Node == nil {
		return 0
	}
	return c.Node.Bytes
}

// resolver carries what every step needs.
type resolver struct {
	inv   *Inventory
	prods *productIndex
	// codesignAvailable is false when the team id probe was degraded, so
	// an unresolved team id can be reported as unresolved rather than as
	// an owner that does not exist.
	codesignAvailable bool
	// codeRoots are the display paths of the user's source directories,
	// which the own-build rule matches project names against.
	codeRoots []string
	// projectNames are the directory names directly under the code roots.
	projectNames map[string]string
}

// Resolve attributes one candidate to an owner.
//
// The steps run in decreasing order of how directly the evidence names an
// application, and the first hit wins. The order is the whole design: a
// bundle id that matches an installed application is not a guess, a cask
// receipt that lists a data path is nearly as good, a shared vendor prefix is
// a guess that is only safe when the vendor publishes one application, and
// everything after that exists to stop software that never had a bundle from
// being reported as an orphan.
func (r *resolver) Resolve(c Candidate) Match {
	var ev []string

	// 1. Exact bundle id, including one- and two-label helper suffixes.
	if m, ok := r.byBundleID(c.Name, ev); ok {
		return m
	}

	// 2. Cask receipt: quit ids, zapped data paths, application names.
	//
	// Inside a publisher folder the zapped paths are held back until step
	// 5b. A cask globs the folder — google-chrome zaps
	// "~/Library/Caches/Google/*" — which is a claim about the publisher's
	// directory and not about each product inside it, and letting it win
	// here handed Chrome the whole of Android Studio's caches. The quit ids
	// and the application names name one product, so they stay.
	if m, ok := r.byCask(c, ev, c.Vendor == ""); ok {
		return m
	}

	// 3. Group containers: a team id first, then the remainder as an id.
	if c.Loc.Key == KeyGroupContainer {
		var gcEv []string
		m, gcEv, ok := r.byGroupContainer(c, ev)
		if ok {
			return m
		}
		ev = gcEv
		if team, rest, ok := TeamIDPrefix(c.Name); ok {
			ev = evidence(ev, "team id "+team+" matches no installed application")
			if !r.codesignAvailable {
				ev = append(ev, "team id unresolved: codesign unavailable")
			}
			c.Name = rest
		}
		c.Name = stripGroupPrefixes(c.Name)
		if m, ok := r.byBundleID(c.Name, ev); ok {
			return m
		}
	}

	// 4. Product alias table.
	if m, ok := r.byProduct(c.Name, "apps/alias", ev); ok {
		return m
	}

	// 5. Display name of an installed bundle.
	if b, ok := r.inv.InstalledByName(c.Name); ok {
		return r.fromBundle(b, classify.Likely, "apps/display-name",
			evidence(ev, "directory name "+c.Name+" matches "+b.Path))
	}

	// 5b. The cask paths held back at step 2, now that everything which
	//     names a single product has had its turn. A glob over a publisher
	//     folder is the right answer for a directory nothing else claims.
	if c.Vendor != "" {
		if m, ok := r.byCask(c, ev, true); ok {
			return m
		}
	}

	// 6. Updater suffix: recurse on the application before the suffix.
	if base, ok := TrimUpdaterSuffix(c.Name); ok {
		sub := evidence(ev, "updater directory for "+base)
		if m, found := r.byBundleID(base, sub); found {
			m.Rule, m.Category = "apps/updater-suffix", "Updater cache"
			m.Reclaim, m.HasReclaim = classify.Regenerable, true
			m.Confidence = classify.Likely
			return m
		}
		if m, found := r.byProduct(base, "apps/updater-suffix", sub); found {
			m.Category = "Updater cache"
			m.Reclaim, m.HasReclaim = classify.Regenerable, true
			m.Confidence = classify.Likely
			return m
		}
		if b, found := r.inv.InstalledByName(base); found {
			m := r.fromBundle(b, classify.Likely, "apps/updater-suffix", sub)
			m.Category = "Updater cache"
			m.Reclaim, m.HasReclaim = classify.Regenerable, true
			return m
		}
	}

	// 7. Vendor prefix, only when the vendor publishes exactly one
	//    installed application and the suffix is not a distinct product.
	if m, ok := r.byVendor(c, ev); ok {
		return m
	}

	// 8. Installer package receipt.
	if m, ok := r.byReceipt(c.Name, ev); ok {
		return m
	}

	// 9. The user's own build.
	if m, ok := r.byOwnBuild(c.Name, ev); ok {
		return m
	}

	// 10. Software that never had a bundle.
	if r.prods.IsNonApp(c.Name) {
		return Match{
			Owner:      Owner{Key: "cli:" + strings.ToLower(c.Name), Kind: KindNonApp, Label: c.Name},
			Confidence: classify.Likely,
			Rule:       "apps/non-app",
			Evidence:   evidence(ev, c.Name+" is command-line software and never had an application bundle"),
		}
	}

	// 11. LaunchServices, corroborating only: it never names the owner by
	//     itself, it only says what macOS still believes about the name.
	for _, e := range r.inv.RegistryFor(c.Name) {
		switch {
		case e.InTrash:
			ev = append(ev, "LaunchServices lists "+c.Name+" in the Trash at "+e.Path)
		case !e.Exists:
			ev = append(ev, "LaunchServices still lists "+c.Name+" at "+e.Path+" (missing)")
		}
	}

	// 12. Unknown, with what was tried.
	return Match{
		Owner:      Owner{Key: "unknown:" + c.Name, Kind: KindUnknown, Label: c.Name},
		Confidence: classify.UnknownOwner,
		Rule:       "apps/unknown",
		Evidence:   evidence(ev, "no bundle, cask, receipt or alias matches "+c.Name),
	}
}

// byBundleID is step 1: the candidate name is an identifier some bundle
// carries, possibly with a helper suffix of one or two labels.
func (r *resolver) byBundleID(name string, ev []string) (Match, bool) {
	if b, ok := r.inv.AnyBundle(name); ok {
		m := r.fromBundle(b, classify.Strong, "apps/bundle-id",
			evidence(ev, "bundle id "+name+" matches "+b.Path))
		m.IDMatch = true
		return m, true
	}
	labels := strings.Split(name, ".")
	for drop := 1; drop <= 2 && len(labels)-drop >= 2; drop++ {
		base := strings.Join(labels[:len(labels)-drop], ".")
		if b, ok := r.inv.AnyBundle(base); ok {
			m := r.fromBundle(b, classify.Strong, "apps/bundle-id",
				evidence(ev, name+" is a helper of bundle id "+base+" at "+b.Path))
			m.IDMatch = true
			return m, true
		}
	}
	return Match{}, false
}

// byCask is step 2, and step 5b for a candidate inside a publisher folder. A
// cask receipt is the only evidence that survives the application being
// deleted, which is why a cask whose bundle is missing still yields an owner
// instead of an unknown.
//
// allowPaths says whether the receipt's zapped data paths count. They always
// do at step 2 and never inside a publisher folder, where a glob over the
// folder is a claim about the publisher rather than about each product in it;
// those candidates come back at step 5b with allowPaths set, once the rules
// that name one product have had their turn.
func (r *resolver) byCask(c Candidate, ev []string, allowPaths bool) (Match, bool) {
	for _, cask := range r.inv.Casks {
		pattern, matched := "", false
		rule := ""
		zapPattern, zapped := "", false
		if allowPaths {
			zapPattern, zapped = cask.MatchesPath(c.Path, r.inv.Paths.Home)
		}
		if p, ok := cask.MatchesID(c.Name); ok {
			pattern, matched, rule = p, true, "apps/cask-quit"
		} else if zapped {
			pattern, matched, rule = zapPattern, true, "apps/cask-zap"
		} else if hasFold(cask.AppNames(), c.Name) {
			pattern, matched, rule = c.Name+".app", true, "apps/cask-app-name"
		}
		if !matched {
			continue
		}
		when := ""
		if !cask.InstalledAt.IsZero() {
			when = " (installed " + cask.InstalledAt.Format("2006-01-02") + ")"
		}
		line := "cask " + cask.Token + when + " lists " + pattern
		// An installed application turns the cask into corroboration of a
		// strong match; a missing one makes the cask the owner.
		for _, appName := range cask.AppNames() {
			if b, ok := r.inv.InstalledByName(appName); ok {
				return r.fromBundle(b, classify.Strong, rule,
					evidence(ev, line, "cask "+cask.Token+" installed "+b.Path)), true
			}
		}
		// The cask names a product, and the product may be installed by a
		// bundle the cask did not place. The stremioservice cask is the
		// case: its own StremioService.app is gone, but Stremio.app is
		// installed and the four gigabytes of streaming-server data
		// under Application Support belong to it. Calling that orphaned
		// would be the most expensive kind of wrong answer.
		if p, known := r.prods.LookupCask(cask.Token); known {
			if b, found := r.installedForProduct(p); found {
				m := r.fromBundle(b, classify.Likely, rule,
					evidence(ev, line, "cask "+cask.Token+" installs "+p.Label+
						", which is installed at "+b.Path))
				m.IDMatch = true
				return m, true
			}
		}
		if cask.BinaryOnly() {
			token := cask.Token
			label := token
			if p, ok := r.prods.LookupCask(token); ok {
				label = p.Label
			}
			return Match{
				Owner:      Owner{Key: "cli:" + token, Kind: KindNonApp, Label: label},
				Confidence: classify.Likely,
				Rule:       rule,
				Evidence:   evidence(ev, line, "cask "+token+" installs a binary, not an application"),
			}, true
		}
		label, slug := cask.Token, ""
		if p, ok := r.prods.LookupCask(cask.Token); ok {
			label, slug = p.Label, p.Slug
		}
		missing := "no application bundle found for cask " + cask.Token
		if len(cask.Apps) > 0 {
			missing = cask.Apps[0] + " not found in /Applications, ~/Applications, the Caskroom or anywhere on the volume"
		}
		return Match{
			Owner:      Owner{Key: "cask:" + cask.Token, Kind: KindCask, Label: label, Slug: slug},
			Confidence: classify.Likely,
			Rule:       rule,
			Evidence:   evidence(ev, line, missing),
		}, true
	}
	return Match{}, false
}

// installedForProduct finds an installed bundle for a product, by any of the
// identifiers or names the alias table lists for it.
func (r *resolver) installedForProduct(p *Product) (*Bundle, bool) {
	for _, id := range p.BundleIDs {
		if strings.ContainsAny(id, "*?") {
			continue
		}
		if b, ok := r.inv.Installed(id); ok {
			return b, true
		}
	}
	for _, n := range p.Names {
		if b, ok := r.inv.InstalledByName(n); ok {
			return b, true
		}
	}
	return nil, false
}

// byGroupContainer is step 3's first half: a team id that an installed
// application's signature matches.
func (r *resolver) byGroupContainer(c Candidate, ev []string) (Match, []string, bool) {
	team, _, ok := TeamIDPrefix(c.Name)
	if !ok {
		return Match{}, ev, false
	}
	signed := r.inv.InstalledForTeam(team)
	if len(signed) == 1 {
		return r.fromBundle(signed[0], classify.Strong, "apps/team-id",
			evidence(ev, "team id "+team+" matches "+signed[0].Path)), ev, true
	}
	if len(signed) > 1 {
		// Several applications share the team, so the remainder has to
		// pick between them; that is steps 1 and 4's job. The note is
		// carried back so the why panel can say why the team id alone
		// was not enough.
		ev = evidence(ev, "team id "+team+" is shared by "+strconv.Itoa(len(signed))+" applications")
	}
	return Match{}, ev, false
}

// byProduct is steps 4 and part of 6: the alias table.
func (r *resolver) byProduct(name, rule string, ev []string) (Match, bool) {
	p, ok := r.prods.LookupID(name)
	how, byID := "bundle id", true
	if !ok {
		p, ok = r.prods.LookupName(name)
		how, byID = "name", false
	}
	if !ok {
		return Match{}, false
	}
	// An installed bundle turns the alias into a strong match and keys the
	// owner on the identifier, so a detector that knows only the id joins
	// the same owner.
	for _, id := range p.BundleIDs {
		if strings.ContainsAny(id, "*?") {
			continue
		}
		if b, found := r.inv.Installed(id); found {
			m := r.fromBundle(b, classify.Strong, rule,
				evidence(ev, how+" "+name+" is "+p.Label+", installed at "+b.Path))
			m.IDMatch = byID
			return m, true
		}
	}
	for _, n := range p.Names {
		if b, found := r.inv.InstalledByName(n); found {
			m := r.fromBundle(b, classify.Strong, rule,
				evidence(ev, how+" "+name+" is "+p.Label+", installed at "+b.Path))
			m.IDMatch = byID
			return m, true
		}
	}
	kind := p.Kind
	if kind == KindApp {
		kind = KindProduct
	}
	return Match{
		Owner:      Owner{Key: p.Key(), Kind: kind, Label: p.Label, Slug: p.Slug},
		Confidence: classify.Likely,
		Rule:       rule,
		Evidence:   evidence(ev, how+" "+name+" is "+p.Label+"; no bundle for it on this volume"),
		IDMatch:    byID,
	}, true
}

// byVendor is step 7. The rule is deliberately narrow: a vendor prefix is
// only evidence when the vendor publishes exactly one installed application,
// because "com.google" covers Chrome, Android Studio and Antigravity, and
// attributing all three to whichever came first would be worse than an
// honest unknown.
func (r *resolver) byVendor(c Candidate, ev []string) (Match, bool) {
	rdns, ok := ParseReverseDNS(c.Name)
	if !ok || rdns.IsApple() {
		return Match{}, false
	}
	if p, found := r.prods.LookupID(c.Name); found && p.Distinct {
		return Match{}, false
	}
	installed := r.inv.InstalledForVendor(rdns.Vendor)
	switch len(installed) {
	case 0:
		return Match{}, false
	case 1:
		return r.fromBundle(installed[0], classify.Likely, "apps/vendor-prefix",
			evidence(ev, "vendor prefix "+rdns.Vendor+" shared only with "+
				installed[0].Path+" ("+installed[0].ID+")")), true
	default:
		return Match{
			Owner: Owner{
				Key: "vendor:" + rdns.Vendor, Kind: KindVendor,
				Label: VendorLabel(rdns.Vendor, ""),
			},
			Confidence: classify.Corroborating,
			Rule:       "apps/vendor-prefix",
			Evidence: evidence(ev, "vendor prefix "+rdns.Vendor+" is shared by several installed applications; "+
				"only an alias can say which one owns "+c.Name),
		}, true
	}
}

// byReceipt is step 8. A receipt whose install location still exists names
// the owner; one whose location is gone is the strongest orphan evidence
// there is, and it is recorded as such rather than discarded.
func (r *resolver) byReceipt(name string, ev []string) (Match, bool) {
	rdns, hasRDNS := ParseReverseDNS(name)
	for _, rec := range r.inv.Receipts {
		id := strings.TrimSuffix(rec.PkgID, ".pkg")
		sameVendor := hasRDNS && rec.VendorPrefix() != "" && rec.VendorPrefix() == rdns.Vendor
		if id != name && !sameVendor {
			continue
		}
		// A receipt whose "location:" is empty names no install path, so
		// it cannot say whether anything is missing. Temurin's JDK
		// receipt is one: reading its blank location as "the location is
		// gone" made a Java runtime look like an orphaned application.
		if rec.Location == "" {
			continue
		}
		when := ""
		if !rec.InstallTime.IsZero() {
			when = " installed " + rec.InstallTime.Format("2006-01-02")
		}
		if rec.LocationExists {
			if b, ok := r.inv.AnyByName(BundleBaseName(rec.InstallPath())); ok {
				return r.fromBundle(b, classify.Strong, "apps/receipt",
					evidence(ev, "pkg receipt "+rec.PkgID+when+" at "+rec.InstallPath())), true
			}
		}
		label, slug, key := rdnsLabel(rec), "", ""
		if p, ok := r.prods.LookupID(id); ok {
			label, slug, key = p.Label, p.Slug, p.Key()
		} else {
			key = "product:" + label
		}
		line := "pkg receipt " + rec.PkgID + when + " at " + rec.InstallPath() + "; location missing"
		if rec.LocationExists {
			line = "pkg receipt " + rec.PkgID + when + " at " + rec.InstallPath()
		}
		if rec.FilesChecked {
			line += "; " + strconv.Itoa(rec.FilesPresent) + " of " + strconv.Itoa(rec.FilesTotal) + " files present"
		}
		return Match{
			Owner:      Owner{Key: key, Kind: KindProduct, Label: label, Slug: slug},
			Confidence: classify.Likely,
			Rule:       "apps/receipt",
			Evidence:   evidence(ev, line),
		}, true
	}
	return Match{}, false
}

// byOwnBuild is step 9. Something the user built is never an orphan however
// absent its bundle is, and the three signals are the ones that survive a
// rebuild: the identifier carries their user name, the name matches a project
// under a code root, or a bundle with the identifier lives under one.
func (r *resolver) byOwnBuild(name string, ev []string) (Match, bool) {
	if r.inv.Paths.User != "" {
		if rdns, ok := ParseReverseDNS(name); ok && len(rdns.Labels) >= 3 &&
			strings.EqualFold(rdns.Labels[1], r.inv.Paths.User) {
			project := rdns.Labels[len(rdns.Labels)-1]
			return Match{
				Owner:      Owner{Key: "project:" + strings.ToLower(project), Kind: KindOwnBuild, Label: project},
				Confidence: classify.Likely,
				Rule:       "apps/own-build",
				Evidence:   evidence(ev, "bundle id "+name+" carries the invoking user's name: a local build"),
			}, true
		}
	}
	if root, ok := r.projectNames[strings.ToLower(name)]; ok {
		return Match{
			Owner:      Owner{Key: "project:" + strings.ToLower(name), Kind: KindOwnBuild, Label: name},
			Confidence: classify.Likely,
			Rule:       "apps/own-build",
			Evidence:   evidence(ev, name+" is a project under "+root),
		}, true
	}
	return Match{}, false
}

// fromBundle builds a match for an installed or discovered bundle.
func (r *resolver) fromBundle(b *Bundle, conf classify.Confidence, rule string, ev []string) Match {
	label := b.DisplayName
	key := "app:" + b.ID
	slug := ""
	if p, ok := r.prods.LookupID(b.ID); ok {
		label, slug = p.Label, p.Slug
	} else if p, ok := r.prods.LookupName(label); ok {
		label, slug = p.Label, p.Slug
	}
	if b.ID == "" {
		key = "app:name:" + BundleBaseName(b.Path)
	}
	return Match{
		Owner:      Owner{Key: key, Kind: KindApp, Label: label, Slug: slug},
		Confidence: conf,
		Rule:       rule,
		Evidence:   ev,
	}
}

// rdnsLabel is the display label for a receipt with no alias row: the vendor
// label of its package id, which reads better than the raw id.
func rdnsLabel(rec Receipt) string {
	id := strings.TrimSuffix(rec.PkgID, ".pkg")
	rdns, ok := ParseReverseDNS(id)
	if !ok {
		return id
	}
	return rdns.Suffix()
}

// evidence returns ev with more lines appended, copying rather than
// extending in place. Several resolution steps extend the same evidence slice
// and only one of them wins, so appending in place would let a losing step's
// line overwrite a winning step's.
func evidence(ev []string, add ...string) []string {
	out := make([]string, 0, len(ev)+len(add))
	out = append(out, ev...)
	return append(out, add...)
}

// hasFold reports whether a list contains a string, ignoring case.
func hasFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
