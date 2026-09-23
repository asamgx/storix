package apps

import (
	"sort"
	"time"

	"github.com/asamgx/storix/internal/classify"
)

// ReportSchema is the version of the document below. It is the same number
// the rest of storix uses, and it changes only when a field is removed or
// changes meaning: adding one is backward compatible by the rule docs/03 sets
// out for the JSON report.
const ReportSchema = 1

// Report is what a scan concluded about the machine's applications.
//
// One document serves three readers, which is why it is a plain data shape
// with no pointers into the tree: the scan cache stores it so that a cached
// scan can render the Apps view without re-probing, `storix apps --json`
// prints it, and the text renderer walks it. A shape that only one of the
// three could use would have meant three shapes drifting apart.
type Report struct {
	Schema int    `json:"schema"`
	Counts Counts `json:"counts"`

	// Apps are the installed applications, largest footprint first. The
	// field is named for what a reader wants rather than for the state,
	// because it is the list they came for.
	Apps []Entry `json:"apps,omitempty"`
	// CaskOnly are owners whose cask is installed and whose application is
	// not.
	CaskOnly []Entry `json:"caskOnly,omitempty"`
	// Orphans are owners whose application appears to be gone.
	Orphans []Entry `json:"orphans,omitempty"`
	// InTrash are applications in the Trash, whose data is still in full.
	InTrash []Entry `json:"inTrash,omitempty"`
	// Unknown are directories nothing could be concluded about.
	Unknown []Entry `json:"unknown,omitempty"`
	// NonApp is command-line software, listed so a reader can see it was
	// considered and deliberately not called an orphan.
	NonApp []Entry `json:"nonApp,omitempty"`
	// OwnBuild is what the user built themselves.
	OwnBuild []Entry `json:"ownBuild,omitempty"`
	// Vendor are publisher directories shared by several applications.
	Vendor []Entry `json:"vendor,omitempty"`

	// CasksMissingApp are the casks whose application artifact is not on
	// the volume. It is a different question from CaskOnly, which is about
	// whose data is orphaned: a cask can be missing its application while
	// the product is installed by another bundle.
	CasksMissingApp []CaskRef `json:"casksMissingApp,omitempty"`
	// Degraded names the probes that did not run, so a short list is
	// visibly short for a reason.
	Degraded []Degradation `json:"degraded,omitempty"`
}

// Counts are the headline numbers of the Applications table.
type Counts struct {
	Bundles  int `json:"bundles"`
	Casks    int `json:"casks"`
	AppStore int `json:"appStore"`
	Owners   int `json:"owners"`
	// Candidates is how many directories were attributed, which is the
	// denominator behind "unknown owner: 12".
	Candidates int `json:"candidates"`
}

// Entry is one owner as the report prints it.
type Entry struct {
	Owner string `json:"owner"`
	Label string `json:"label"`
	State string `json:"state"`
	// Confidence is the weakest attribution among the components: a
	// footprint is only as trustworthy as its least certain part.
	Confidence string `json:"confidence"`
	Footprint  Sizes  `json:"footprint"`
	// Reclaimable is the share of the footprint that could be freed.
	Reclaimable int64 `json:"reclaimable,omitempty"`
	// LastWrite is the newest modification anywhere in the owner's data.
	LastWrite time.Time `json:"lastWrite,omitempty"`
	// Sources are where the owner's bundles were found.
	Sources []string `json:"sources,omitempty"`
	// Bundles are the application bundles attributed to this owner.
	Bundles []BundleRef `json:"bundles,omitempty"`
	// Components are the directories the footprint is made of.
	Components []ComponentRef `json:"components,omitempty"`
	Evidence   []string       `json:"evidence,omitempty"`
	// Keep are the signals that blocked an orphan verdict, shown even when
	// the owner is installed: "why is this not an orphan" is as useful a
	// question as the other one.
	Keep []string `json:"keep,omitempty"`
}

// Sizes is a footprint split by bucket.
type Sizes struct {
	Bundle     int64 `json:"bundle"`
	Data       int64 `json:"data"`
	Caches     int64 `json:"caches"`
	Dev        int64 `json:"dev"`
	Containers int64 `json:"containers,omitempty"`
	Total      int64 `json:"total"`
}

// BundleRef is one application bundle.
type BundleRef struct {
	Path    string `json:"path"`
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	Cask    string `json:"cask,omitempty"`
	MAS     bool   `json:"mas,omitempty"`
	Source  string `json:"source,omitempty"`
}

// ComponentRef is one directory of a footprint.
type ComponentRef struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Bucket   string `json:"bucket"`
	Category string `json:"category,omitempty"`
	Source   string `json:"source,omitempty"`
	Reclaim  string `json:"reclaim,omitempty"`
}

// CaskRef is a cask whose application artifact is missing.
type CaskRef struct {
	Token       string    `json:"token"`
	Version     string    `json:"version,omitempty"`
	ExpectedApp string    `json:"expectedApp,omitempty"`
	InstalledAt time.Time `json:"installedAt,omitempty"`
	// Evidence is why the cask is listed, in words.
	Evidence []string `json:"evidence,omitempty"`
}

// BuildReport turns an analysis and the claims that won their nodes into the
// report document.
//
// The winners matter rather than this package's own claims: a directory the
// ide detector claimed carries the ide detector's bucket and category, and
// including it under the application's owner is exactly what makes a
// footprint cross the ledger's partition.
func BuildReport(a *Analysis, winners []classify.Claim) *Report {
	if a == nil {
		return nil
	}
	r := &Report{Schema: ReportSchema, Degraded: a.Inventory.Degraded}
	r.Counts = counts(a)

	rendered := make(map[string]bool)
	for _, fp := range Footprints(a, winners) {
		e := entryOf(a, fp)
		rendered[fp.Owner.Key] = true
		switch fp.State {
		case StateInstalled:
			r.Apps = append(r.Apps, e)
		case StateCaskOnly:
			r.CaskOnly = append(r.CaskOnly, e)
		case StateOrphanLikely:
			r.Orphans = append(r.Orphans, e)
		case StateInTrash:
			r.InTrash = append(r.InTrash, e)
		case StateNonApp:
			r.NonApp = append(r.NonApp, e)
		case StateOwnBuild:
			r.OwnBuild = append(r.OwnBuild, e)
		case StateVendor:
			r.Vendor = append(r.Vendor, e)
		case StateUnknown:
			// An owner can be unknown and still have claimed its
			// bytes: one whose data was written yesterday, or one
			// whose orphan verdict was withdrawn because a probe
			// could not run. Those keep the footprint they earned,
			// and losing it is how 2.6 GB of an owner's data
			// stopped being shown at all.
			r.Unknown = append(r.Unknown, e)
		}
	}
	// The owners that claimed nothing are added from the analysis instead.
	// A directory nothing could be attributed to deliberately emits no
	// claim — one that said "I could not attribute this" would outrank the
	// catalog rule for the same path — so it has no footprint to render.
	// It still belongs in the report, which is where a reader and the next
	// round of alias-table rows come from.
	r.Unknown = append(r.Unknown, unknownEntries(a, rendered)...)
	sortEntries(r.Unknown, false)

	for _, c := range a.CaskOnlyCasks() {
		ref := CaskRef{Token: c.Token, Version: c.Version, InstalledAt: c.InstalledAt}
		if len(c.Apps) > 0 {
			ref.ExpectedApp = c.Apps[0]
		}
		if c.ReceiptErr != "" {
			ref.Evidence = append(ref.Evidence, "its install receipt could not be read: "+c.ReceiptErr)
		}
		ref.Evidence = append(ref.Evidence, ref.ExpectedApp+" is not on the volume")
		r.CasksMissingApp = append(r.CasksMissingApp, ref)
	}
	return r
}

// unknownEntries lists the directories nothing could be attributed to,
// largest first. Owners already rendered from their footprint are skipped, so
// that an owner which is unknown but did claim its bytes appears once.
func unknownEntries(a *Analysis, rendered map[string]bool) []Entry {
	var out []Entry
	for _, key := range a.OwnerKeys() {
		o := a.Owners[key]
		v := a.Verdicts[key]
		if v == nil || v.State != StateUnknown || rendered[key] {
			continue
		}
		e := Entry{
			Owner: key, Label: o.Owner.Label, State: v.State.String(),
			Confidence: v.Confidence.String(),
			Footprint:  Sizes{Data: o.Bytes, Total: o.Bytes},
			LastWrite:  v.LastWrite,
			Evidence:   v.Evidence,
			Keep:       v.Keep,
		}
		for _, i := range o.Members {
			c := a.Candidates[i]
			e.Components = append(e.Components, ComponentRef{
				Path: c.Path, Bytes: c.Bytes(), Bucket: classify.BucketAppData.ID(),
				Category: c.Loc.Category,
			})
		}
		out = append(out, e)
	}
	sortEntries(out, false)
	return out
}

// sortEntries orders one list the way the report shows it: by size, which is
// what the question "what is big" needs, or by name for a reader comparing two
// runs.
func sortEntries(entries []Entry, byName bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		if byName {
			return entries[i].Label < entries[j].Label
		}
		if entries[i].Footprint.Total != entries[j].Footprint.Total {
			return entries[i].Footprint.Total > entries[j].Footprint.Total
		}
		return entries[i].Label < entries[j].Label
	})
}

// counts fills in the headline numbers.
func counts(a *Analysis) Counts {
	c := Counts{
		Casks:      len(a.Inventory.Casks),
		Owners:     len(a.Owners),
		Candidates: len(a.Candidates),
	}
	for _, b := range a.Inventory.Bundles {
		if !b.Source.Installed() {
			continue
		}
		c.Bundles++
		if b.MASReceipt {
			c.AppStore++
		}
	}
	return c
}

// entryOf renders one footprint as a report entry.
func entryOf(a *Analysis, fp Footprint) Entry {
	e := Entry{
		Owner:      fp.Owner.Key,
		Label:      fp.Owner.Label,
		State:      fp.State.String(),
		Confidence: fp.Confidence.String(),
		Footprint: Sizes{
			Bundle: fp.Bundle, Data: fp.Data, Caches: fp.Caches,
			Dev: fp.Dev, Containers: fp.Containers, Total: fp.Total,
		},
		Reclaimable: fp.Reclaimable(),
		LastWrite:   fp.Verdict.LastWrite,
		Evidence:    fp.Verdict.Evidence,
		Keep:        fp.Verdict.Keep,
	}
	for _, s := range fp.Sources {
		e.Sources = appendUnique(e.Sources, s.String())
	}
	if o, ok := a.Owners[fp.Owner.Key]; ok {
		for _, b := range o.Bundles {
			ref := BundleRef{
				Path: b.Path, ID: b.ID, Version: b.Version,
				MAS: b.MASReceipt, Source: b.Source.String(),
			}
			if b.Cask != nil {
				ref.Cask = b.Cask.Token
			}
			e.Bundles = append(e.Bundles, ref)
		}
	}
	for _, c := range fp.Components {
		e.Components = append(e.Components, ComponentRef{
			Path: c.Path, Bytes: c.Bytes, Bucket: c.Bucket.ID(),
			Category: c.Category, Source: c.Source, Reclaim: c.Reclaim.String(),
		})
	}
	return e
}

// appendUnique adds a string if the list does not already hold it.
func appendUnique(dst []string, s string) []string {
	if s == "" {
		return dst
	}
	for _, have := range dst {
		if have == s {
			return dst
		}
	}
	return append(dst, s)
}

// SortBy reorders every list. Size is the default because the question the
// table answers is "what is big"; name exists for a reader comparing two runs.
func (r *Report) SortBy(byName bool) {
	if r == nil {
		return
	}
	for _, list := range [][]Entry{
		r.Apps, r.CaskOnly, r.Orphans, r.InTrash, r.Unknown, r.NonApp, r.OwnBuild, r.Vendor,
	} {
		sortEntries(list, byName)
	}
}

// NeedsAttention is the total of everything the report would have a reader
// look at: the states that mean an application is not there any more.
func (r *Report) NeedsAttention() int64 {
	if r == nil {
		return 0
	}
	var n int64
	for _, list := range [][]Entry{r.CaskOnly, r.Orphans, r.InTrash} {
		for _, e := range list {
			n += e.Footprint.Total
		}
	}
	return n
}

// Empty reports whether the document has nothing to show, which is what a
// pre-1b cache yields.
func (r *Report) Empty() bool {
	return r == nil || (len(r.Apps) == 0 && len(r.Orphans) == 0 &&
		len(r.CaskOnly) == 0 && len(r.Unknown) == 0)
}
