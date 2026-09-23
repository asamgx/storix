package apps

import (
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/walk"
)

// Analysis is the finished attribution: the inventory it was built from, the
// candidates it considered and what each one resolved to.
type Analysis struct {
	// Tree is the walked tree the analysis was built against, kept so the
	// claims can find the node behind a path the facts named.
	Tree       *walk.Tree
	Inventory  *Inventory
	Candidates []Candidate
	Matches    []Match
	// Owners groups the matches by owner key, which is what verdicts and
	// footprints are computed over.
	Owners map[string]*OwnerResult
	// Verdicts is the state of each owner, keyed the same way.
	Verdicts map[string]*Verdict
}

// OwnerResult is everything known about one owner.
type OwnerResult struct {
	Owner Owner
	// Members are indices into Analysis.Candidates and Matches.
	Members []int
	// Bytes is the total of the members' subtrees. It is a convenience for
	// the report and is never added to the ledger: the ledger counts each
	// byte once, and a footprint deliberately counts across buckets.
	Bytes int64
	// Confidence is the strongest confidence any member reached.
	Confidence classify.Confidence
	// Bundles are the installed bundles attributed to this owner.
	Bundles []*Bundle
	// IDs are the bundle identifiers this owner answers to: the alias
	// table's, its bundles', and any candidate name that was itself an
	// identifier. They are what the orphan precondition searches on.
	IDs []string
	// Names are the display names this owner answers to.
	Names []string
	// LastWrite is the newest modification anywhere in the owner's data,
	// which is what the report's "last write" column shows.
	LastWrite time.Time
	// LastTouch is the newest modification of the owner's own directories.
	// It is what the recency keep signal reads; see lastWrite for why the
	// two are not the same question.
	LastTouch time.Time
}

// LastWriteZero reports whether nothing is known about when the owner's data
// was last written, which is the case for data outside the scanned subtree.
func (o *OwnerResult) LastWriteZero() bool { return o.LastWrite.IsZero() }

// Options tune the analysis.
type Options struct {
	// CodeRoots are display paths holding the user's projects, for the
	// own-build rule. Nil means the ones classify.Context supplies.
	CodeRoots []string
	// CodesignAvailable is false when the team id probe was degraded.
	CodesignAvailable bool
	// Root anchors the absolute paths in the location table. It is empty
	// for a real scan and a fixture directory in the corpus test.
	Root string
	// RecentWindow is how recently an owner's data must have been written
	// for the owner to be protected from an orphan verdict. Zero selects
	// DefaultRecentWindow.
	RecentWindow time.Duration
	// Now is the clock the keep signals compare against. The zero value
	// means time.Now, and a test sets it so a verdict is reproducible.
	Now time.Time
	// TuningLog receives one line per unknown owner over TuningThreshold:
	// the owner key, its bytes and its first path. It is nil by default,
	// because a package that wrote to the user's home as a side effect of
	// being tested would be untestable.
	TuningLog io.Writer
	// TuningThreshold is how large an unknown owner must be to be logged.
	// Zero selects DefaultTuningThreshold.
	TuningThreshold int64
}

// Analyze resolves every candidate under the application data locations.
//
// It is pure: it reads the tree and the facts and touches nothing else, which
// is what lets a cached scan re-run it without going near the machine.
func Analyze(t *walk.Tree, f *Facts, cx classify.Context, opts Options) *Analysis {
	paths := pathsFor(cx, opts)
	inv := BuildInventory(t, f, paths)
	a := &Analysis{Tree: t, Inventory: inv, Owners: make(map[string]*OwnerResult)}

	r := &resolver{
		inv:               inv,
		prods:             defaultIndex,
		codesignAvailable: opts.CodesignAvailable,
		codeRoots:         codeRoots(cx, opts, paths.Home),
		projectNames:      make(map[string]string),
	}
	for _, root := range r.codeRoots {
		for _, n := range childrenOf(t, root) {
			if n.IsDir() {
				r.projectNames[strings.ToLower(n.Name)] = root
			}
		}
	}

	// A publisher folder is resolved before its children, so a child that
	// nothing recognises can fall back to it.
	vendorOwners := make(map[string]Match)
	for _, cand := range collectCandidates(t, paths) {
		m := r.Resolve(cand)
		switch {
		case cand.Vendor == "" && IsVendorDir(cand.Name) && m.Owner.Kind != KindUnknown:
			vendorOwners[path.Join(path.Dir(cand.Path), cand.Name)] = m
		case cand.Vendor != "" && m.Owner.Kind == KindUnknown:
			// Autodesk keeps a dozen internal directories under its
			// own folder — AdODIS, AdskCER, ADLM — and not one of them
			// names a product. They are still Autodesk's, and leaving
			// 1.4 GB of them as "unknown owner" would be a worse
			// answer than the folder they sit in already gives.
			if parent, ok := vendorOwners[path.Dir(cand.Path)]; ok {
				m = parent
				m.Confidence = classify.Corroborating
				m.Rule = "apps/vendor-dir"
				m.Evidence = evidence(parent.Evidence,
					cand.Name+" is inside "+cand.Vendor+"'s own folder")
			}
		}
		a.Candidates = append(a.Candidates, cand)
		a.Matches = append(a.Matches, m)
		a.note(len(a.Candidates)-1, cand, m)
	}
	for _, b := range inv.Bundles {
		if !b.Source.Installed() {
			continue
		}
		key := "app:" + b.ID
		if b.ID == "" {
			key = "app:name:" + BundleBaseName(b.Path)
		}
		label := b.DisplayName
		slug := ""
		if p, ok := defaultIndex.LookupID(b.ID); ok {
			label, slug = p.Label, p.Slug
		} else if p, ok := defaultIndex.LookupName(label); ok {
			label, slug = p.Label, p.Slug
		}
		res := a.owner(Owner{Key: key, Kind: KindApp, Label: label, Slug: slug})
		res.Bundles = append(res.Bundles, b)
		res.addIdentity(b.ID, b.DisplayName)
		if res.Confidence == classify.None || res.Confidence > classify.Strong {
			res.Confidence = classify.Strong
		}
	}
	for _, res := range a.Owners {
		res.addProductIdentity()
	}
	a.verdicts(opts)
	return a
}

// note folds one resolved candidate into its owner.
func (a *Analysis) note(i int, c Candidate, m Match) {
	res := a.owner(m.Owner)
	res.Members = append(res.Members, i)
	res.Bytes += c.Bytes()
	if id, ok := strings.CutPrefix(m.Owner.Key, "app:"); ok && !strings.HasPrefix(id, "name:") {
		res.addIdentity(id, "")
	}
	// A candidate whose own name is an identifier is one more name the
	// orphan precondition has to search the volume for.
	if _, isID := ParseReverseDNS(c.Name); isID {
		res.addIdentity(c.Name, "")
	} else {
		res.addIdentity("", c.Name)
	}
	if res.Confidence == classify.None || m.Confidence < res.Confidence {
		res.Confidence = m.Confidence
	}
}

// addIdentity records an identifier and a name the owner answers to.
func (o *OwnerResult) addIdentity(id, name string) {
	if id != "" {
		o.IDs = mergeKeys(o.IDs, id)
	}
	if name != "" {
		o.Names = mergeKeys(o.Names, name)
	}
}

// addProductIdentity folds in the alias table's identifiers and names, so the
// orphan precondition searches for every form of the product and not only the
// one the directory happened to be named after.
func (o *OwnerResult) addProductIdentity() {
	if o.Owner.Slug == "" {
		return
	}
	for _, p := range defaultIndex.all {
		if p.Slug != o.Owner.Slug {
			continue
		}
		for _, id := range p.BundleIDs {
			if !strings.ContainsAny(id, "*?") {
				o.addIdentity(id, "")
			}
		}
		for _, n := range p.Names {
			o.addIdentity("", n)
		}
		return
	}
}

// mergeKeys adds a value if it is not already there. The lists are a handful
// of entries long, so a linear scan beats a map per owner.
func mergeKeys(dst []string, add string) []string {
	for _, have := range dst {
		if have == add {
			return dst
		}
	}
	return append(dst, add)
}

// owner returns the result for an owner key, creating it on first sight.
func (a *Analysis) owner(o Owner) *OwnerResult {
	res := a.Owners[o.Key]
	if res == nil {
		res = &OwnerResult{Owner: o}
		a.Owners[o.Key] = res
	}
	return res
}

// OwnerKeys lists the owner keys in a deterministic order, so a report built
// from a map still renders the same way twice.
func (a *Analysis) OwnerKeys() []string {
	out := make([]string, 0, len(a.Owners))
	for k := range a.Owners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// collectCandidates walks the location table and returns every direct child
// that is worth asking about, plus the children of publisher folders.
func collectCandidates(t *walk.Tree, p Paths) []Candidate {
	var out []Candidate
	for _, loc := range Locations {
		dir := p.Resolve(loc.Dir)
		for _, n := range childrenOf(t, dir) {
			raw := n.Name
			name := NormalizeName(loc.Key, raw)
			if name == "" || IsAppleName(name) {
				continue
			}
			display := path.Join(dir, raw)
			if isLocationDir(display, p) {
				continue
			}
			if IsVendorDir(name) {
				// The folder itself is still a candidate: it holds
				// the vendor's shared files. Its children are the
				// ones that name individual products.
				out = append(out, Candidate{
					Name: name, RawName: raw, Path: display, Loc: loc, Node: n,
				})
				for _, kid := range n.Children {
					kidName := NormalizeName(loc.Key, kid.Name)
					if kidName == "" || IsAppleName(kidName) {
						continue
					}
					out = append(out, Candidate{
						Name:    kidName,
						RawName: kid.Name,
						Path:    path.Join(display, kid.Name),
						Loc:     loc,
						Vendor:  name,
						Node:    kid,
					})
				}
				continue
			}
			out = append(out, Candidate{
				Name: name, RawName: raw, Path: display, Loc: loc, Node: n,
			})
		}
	}
	return out
}

// isLocationDir reports whether a path is itself one of the application data
// locations, such as "~/Library/Preferences/ByHost" under Preferences.
func isLocationDir(display string, p Paths) bool {
	for _, loc := range Locations {
		if p.Resolve(loc.Dir) == display {
			return true
		}
	}
	return false
}

// childrenOf returns the children of a display path, or nothing when the walk
// never saw it. Retained leaves are children too: the walker keeps every file
// under Preferences, Cookies, LaunchAgents and the receipts directory for
// exactly this reason.
func childrenOf(t *walk.Tree, display string) []*walk.Node {
	n, ok := lookupDisplay(t, display)
	if !ok {
		return nil
	}
	return n.Children
}

// lookupDisplay finds the node for a display path.
//
// A real scan is rooted at the data volume, where the display path
// "/Users/u/Library" is stored as "/System/Volumes/Data/Users/u/Library", so
// the translation is needed. A fixture is rooted at an ordinary directory,
// where the path is already what it looks like — and, because a temporary
// directory lives under /private, translating it would produce a path that is
// not in the tree at all. Trying the path as given first covers both.
func lookupDisplay(t *walk.Tree, display string) (*walk.Node, bool) {
	if t == nil || display == "" {
		return nil, false
	}
	if n, ok := t.Lookup(display); ok {
		return n, true
	}
	return t.Lookup(mac.ScanPath(display))
}

// pathsFor reads the scan root and the user's home out of the context, with
// the options' Root override for the corpus tests.
func pathsFor(cx classify.Context, opts Options) Paths {
	p := Paths{Root: opts.Root, Home: cx.Home}
	if p.Home != "" {
		p.User = path.Base(p.Home)
	}
	return p
}

// codeRoots is the list of directories the own-build rule looks in.
func codeRoots(cx classify.Context, opts Options, home string) []string {
	roots := opts.CodeRoots
	if len(roots) == 0 {
		roots = cx.CodeRoots
	}
	if len(roots) == 0 && home != "" {
		roots = []string{path.Join(home, "code")}
	}
	return roots
}
