package apps

import (
	"sort"
	"strings"

	"github.com/asamgx/storix/internal/classify"
)

// Component is one directory that belongs to an owner, wherever the ledger
// counted it.
type Component struct {
	Path     string           `json:"path"`
	Bytes    int64            `json:"bytes"`
	Bucket   classify.Bucket  `json:"bucket"`
	Category string           `json:"category,omitempty"`
	Source   string           `json:"source,omitempty"`
	Reclaim  classify.Reclaim `json:"reclaim"`
}

// Footprint is everything one application costs, across every bucket.
//
// This is the number a person actually wants and the ledger cannot give them.
// The ledger partitions the disk, so VS Code's bundle is in Applications, its
// Application Support is in App data and its extension cache is in Developer,
// and none of those three rows mentions the other two. A footprint crosses
// the partition on purpose.
//
// Because it crosses it, a footprint must never be added back into the
// ledger: the same bytes appear in a bucket total and in a footprint, and
// summing the two would count them twice. footprint_test.go asserts it.
type Footprint struct {
	Owner   Owner   `json:"owner"`
	State   State   `json:"state"`
	Verdict Verdict `json:"verdict"`
	// Bundle is bucket 2: the application itself.
	Bundle int64 `json:"bundle"`
	// Data is bucket 3 less the cache-like categories: what would be lost.
	Data int64 `json:"data"`
	// Caches is bucket 3's cache-like categories: what would come back.
	Caches int64 `json:"caches"`
	// Dev is bucket 4, the share a developer-tool detector claimed.
	Dev int64 `json:"dev"`
	// Containers is bucket 5, the disk images a runtime was given.
	Containers int64 `json:"containers"`
	Total      int64 `json:"total"`
	// Sources are where the owner's bundles were found.
	Sources []Source `json:"-"`
	// Confidence is the weakest attribution among the components, because
	// a footprint is only as trustworthy as its least certain part.
	Confidence classify.Confidence `json:"confidence"`
	Components []Component         `json:"components,omitempty"`
}

// Footprints groups the winning claims by owner.
//
// The winners are the claims that survived the engine's resolution, so a
// directory that both this package and the ide detector claimed appears once,
// under whichever of them won, and carries that winner's bucket. That is why
// the dev detectors are asked to set the same owner keys: it is what lets VS
// Code's footprint include a directory the ide detector owns.
//
// This takes the analysis rather than the inventory alone, because a
// footprint carries its owner's verdict and the verdicts live here.
func Footprints(a *Analysis, winners []classify.Claim) []Footprint {
	if a == nil {
		return nil
	}
	byKey := make(map[string]*Footprint, len(a.Owners))
	for _, key := range a.OwnerKeys() {
		o := a.Owners[key]
		fp := &Footprint{Owner: o.Owner, Confidence: o.Confidence}
		if v := a.Verdicts[key]; v != nil {
			fp.State, fp.Verdict = v.State, *v
		}
		for _, b := range o.Bundles {
			fp.Sources = append(fp.Sources, b.Source)
		}
		byKey[key] = fp
	}

	// A claim can carry several keys for one owner, so each claim is
	// attributed once: to the first key that names a known owner.
	for _, cl := range winners {
		fp := firstOwner(byKey, cl.OwnerKeys)
		if fp == nil || cl.Node == nil {
			continue
		}
		fp.Components = append(fp.Components, Component{
			Path:     cl.Node.Display(),
			Bytes:    cl.Node.Bytes,
			Bucket:   cl.Bucket,
			Category: cl.Category,
			Source:   cl.Source.String(),
			Reclaim:  cl.Reclaim,
		})
		if cl.Confidence > fp.Confidence {
			fp.Confidence = cl.Confidence
		}
	}

	ordered := make([]*Footprint, 0, len(byKey))
	for _, key := range a.OwnerKeys() {
		fp := byKey[key]
		fp.Components = dropNested(fp.Components)
		ordered = append(ordered, fp)
	}
	dropOverlap(ordered)

	out := make([]Footprint, 0, len(ordered))
	for _, fp := range ordered {
		fp.total()
		if fp.Total == 0 && len(fp.Components) == 0 {
			continue
		}
		out = append(out, *fp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Owner.Key < out[j].Owner.Key
	})
	return out
}

// firstOwner finds the footprint a claim's keys name.
func firstOwner(byKey map[string]*Footprint, keys []string) *Footprint {
	for _, k := range keys {
		if fp, ok := byKey[k]; ok {
			return fp
		}
	}
	return nil
}

// dropNested removes a component that lies inside another of the same owner.
//
// Without this an owner that claims both a directory and something under it
// would have its bytes counted twice, and the footprint would be larger than
// the disk. The components are sorted by path first so a parent is always
// seen before its children.
func dropNested(cs []Component) []Component {
	if len(cs) < 2 {
		return cs
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Path < cs[j].Path })
	out := cs[:0]
	var kept []string
	for _, c := range cs {
		nested := false
		for _, p := range kept {
			if strings.HasPrefix(c.Path, p+"/") {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		kept = append(kept, c.Path)
		out = append(out, c)
	}
	return out
}

// dropOverlap stops one owner's component from counting another owner's bytes.
//
// dropNested settles the question inside a single footprint. Across footprints
// it stays open, and the answer it leaves is wrong in a way that matters:
// ~/Library/Application Support/Google is the publisher folder, 2.68 GB of it,
// and Android Studio's own directory sits inside it. Both are claimed, by
// different owners, and both carry their whole subtree — so those bytes are in
// two footprints at once, and uninstalling Chrome would have offered Android
// Studio's live data for deletion.
//
// A node's bytes belong to exactly one owner: the deepest one that claimed
// them. So each component gives up the bytes of every component claimed
// beneath it, and only the outermost of those is subtracted, because a nested
// one is already inside it. The arithmetic is exact — the subtracted amounts
// reappear in the owners that claimed them — and a component reduced to
// nothing stays in the list, since "Google holds nothing of its own" is an
// answer a reader wants rather than a row to hide.
func dropOverlap(fps []*Footprint) {
	type ref struct {
		owner string
		comp  *Component
	}
	var all []ref
	for _, fp := range fps {
		for i := range fp.Components {
			all = append(all, ref{fp.Owner.Key, &fp.Components[i]})
		}
	}
	if len(all) < 2 {
		return
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].comp.Path < all[j].comp.Path })

	// The paths are sorted, so a component's descendants are exactly the run
	// of entries that follows it and carries its path as a prefix.
	inner := make([]int64, len(all))
	for i := range all {
		prefix := all[i].comp.Path + "/"
		covered := ""
		for j := i + 1; j < len(all) && strings.HasPrefix(all[j].comp.Path, prefix); j++ {
			if covered != "" && strings.HasPrefix(all[j].comp.Path, covered) {
				continue
			}
			if all[j].owner == all[i].owner {
				continue
			}
			covered = all[j].comp.Path + "/"
			inner[i] += all[j].comp.Bytes
		}
	}
	for i, r := range all {
		if r.comp.Bytes -= inner[i]; r.comp.Bytes < 0 {
			r.comp.Bytes = 0
		}
	}
}

// total sums the components into the per-bucket columns.
func (f *Footprint) total() {
	f.Bundle, f.Data, f.Caches, f.Dev, f.Containers, f.Total = 0, 0, 0, 0, 0, 0
	for _, c := range f.Components {
		switch c.Bucket {
		case classify.BucketApps:
			f.Bundle += c.Bytes
		case classify.BucketAppData:
			if CacheLike(c.Category) {
				f.Caches += c.Bytes
			} else {
				f.Data += c.Bytes
			}
		case classify.BucketDeveloper:
			f.Dev += c.Bytes
		case classify.BucketContainers:
			f.Containers += c.Bytes
		default:
			f.Data += c.Bytes
		}
		f.Total += c.Bytes
	}
}

// Reclaimable is the share of the footprint that could be freed.
func (f Footprint) Reclaimable() int64 {
	var n int64
	for _, c := range f.Components {
		if c.Reclaim.Reclaimable() {
			n += c.Bytes
		}
	}
	return n
}

// OwnerIndex answers "who is this identifier or name", so a developer-tool
// detector can set the same owner key this package would without duplicating
// the alias table.
type OwnerIndex struct{ inv *Inventory }

// Owners builds the lookup over an inventory.
func Owners(inv *Inventory) *OwnerIndex { return &OwnerIndex{inv: inv} }

// Lookup resolves a bundle identifier, a cask token or a display name to an
// owner. The second result is false when nothing on the machine and nothing
// in the alias table knows the name.
func (ix *OwnerIndex) Lookup(idOrName string) (Owner, bool) {
	if idOrName == "" {
		return Owner{}, false
	}
	label := idOrName
	slug := ""
	if p, ok := defaultIndex.LookupID(idOrName); ok {
		label, slug = p.Label, p.Slug
	} else if p, ok := defaultIndex.LookupName(idOrName); ok {
		label, slug = p.Label, p.Slug
	}
	if ix.inv != nil {
		if b, ok := ix.inv.Installed(idOrName); ok {
			return Owner{Key: "app:" + b.ID, Kind: KindApp, Label: label, Slug: slug}, true
		}
		if b, ok := ix.inv.InstalledByName(idOrName); ok && b.ID != "" {
			return Owner{Key: "app:" + b.ID, Kind: KindApp, Label: label, Slug: slug}, true
		}
		if c, ok := ix.inv.CaskByToken[idOrName]; ok {
			return Owner{Key: "cask:" + c.Token, Kind: KindCask, Label: label, Slug: slug}, true
		}
	}
	if slug == "" {
		return Owner{}, false
	}
	// The alias table knows the product even though nothing is installed,
	// which is how a detector attributes "~/.cursor" to Cursor.
	for _, p := range defaultIndex.all {
		if p.Slug != slug {
			continue
		}
		if len(p.BundleIDs) > 0 && !strings.ContainsAny(p.BundleIDs[0], "*?") {
			return Owner{Key: "app:" + p.BundleIDs[0], Kind: KindProduct, Label: label, Slug: slug}, true
		}
		return Owner{Key: p.Key(), Kind: KindProduct, Label: label, Slug: slug}, true
	}
	return Owner{}, false
}
