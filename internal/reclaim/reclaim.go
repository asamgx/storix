// Package reclaim builds the reclaim plan: what could be freed on this
// machine, with which command, and at what cost.
//
// The plan is a report and nothing else. It never runs a command, never
// moves a file, and holds no state between scans: [Build] is a pure function
// of a finished scan, so the TUI, `storix reclaim` and the JSON document all
// show the same plan for the same scan.
//
// Three rules shape it (D48, D49):
//
//   - A tier is what freeing costs, and Safe is asserted by the detector that
//     names the command, never derived from a reclaim tag.
//   - Each byte is counted once. An item nested inside another takes its
//     bytes out of the enclosing one, the way the ledger gives each node's
//     own bytes to exactly one claim.
//   - A tool's own figure — `brew cleanup -n`, `docker system df` — is
//     reported apart from the walked bytes and never added to them: it
//     overlaps the directories the walk already counted.
package reclaim

import (
	"path"
	"sort"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/walk"
)

// Tier is detect.Tier; see there for what each one means.
type Tier = detect.Tier

// The tiers, re-exported so a caller of this package needs no other import.
const (
	Safe       = detect.TierSafe
	Redownload = detect.TierRedownload
	Reinstall  = detect.TierReinstall
	Check      = detect.TierCheck
	InUse      = detect.TierInUse
	Never      = detect.TierNever
)

// DefaultStaleAfter is how long a project must have gone untouched before
// its build output is offered for reinstalling rather than listed as in use.
const DefaultStaleAfter = 30 * 24 * time.Hour

// moveToTrash is the action of an item whose tool has no command of its own.
const moveToTrash = "move to the Trash"

// Options tune the plan.
type Options struct {
	// StaleAfter is DefaultStaleAfter when zero.
	StaleAfter time.Duration
	// Now is the scan's own finish time when zero, so a plan rebuilt from
	// a cache file says what it said when the scan was taken.
	Now time.Time
}

// Item is one thing the plan could free, or explains it will not.
type Item struct {
	// ID is stable across runs: the source and the path, or the source
	// and the tool's row for a reported figure.
	ID    string `json:"id"`
	Tier  Tier   `json:"tier"`
	Title string `json:"title"`
	// Path is the display path, empty for a figure no directory holds.
	Path string `json:"path,omitempty"`
	// Node is the tree node id, -1 when there is none.
	Node int32 `json:"node"`
	// Bytes is what the item frees, each byte counted once across the
	// plan for walked items.
	Bytes int64 `json:"bytes"`
	// Reported marks bytes that are the tool's own figure rather than the
	// walk's; they are totalled apart.
	Reported bool `json:"reported,omitempty"`
	// Command is the tool's own way of freeing it; Action says what to do
	// when there is none.
	Command string `json:"command,omitempty"`
	Action  string `json:"action,omitempty"`
	Impact  string `json:"impact,omitempty"`
	// AfterTrash is set when the space comes back only once the Trash is
	// emptied.
	AfterTrash bool `json:"after_trash,omitempty"`
	// Source is what found it: a detector name, "apps", "projects",
	// "ledger".
	Source   string   `json:"source"`
	Evidence []string `json:"evidence,omitempty"`

	node *walk.Node
}

// Edit is a line of the user's own files that names software already gone:
// a Brewfile entry `brew bundle` would reinstall, or a shell line.
type Edit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
	Why  string `json:"why"`
}

// Totals are one tier's sums.
type Totals struct {
	// Walked is the bytes the walk counted, each once.
	Walked int64 `json:"walked"`
	// Reported is the tools' own figures, which overlap Walked.
	Reported int64 `json:"reported"`
	// AfterTrash is the part of Walked that is freed only once the Trash
	// is emptied.
	AfterTrash int64 `json:"after_trash"`
	Items      int   `json:"items"`
}

// TrashState is what the plan knows about the Trash itself.
type TrashState struct {
	Bytes int64  `json:"bytes"`
	Known bool   `json:"known"`
	Note  string `json:"note,omitempty"`
}

// Plan is the whole report.
type Plan struct {
	Items      []Item           `json:"items"`
	Edits      []Edit           `json:"edits,omitempty"`
	Totals     map[Tier]*Totals `json:"totals"`
	Trash      TrashState       `json:"trash"`
	StaleAfter time.Duration    `json:"stale_after_ns"`
	Notes      []string         `json:"notes,omitempty"`
}

// Build makes the plan for a finished scan. A nil or unclassified scan gives
// an empty plan, never an error: there is simply nothing to suggest.
func Build(res *scan.Result, o Options) *Plan {
	if o.StaleAfter <= 0 {
		o.StaleAfter = DefaultStaleAfter
	}
	p := &Plan{Totals: map[Tier]*Totals{}, StaleAfter: o.StaleAfter}
	if res == nil || res.Tree == nil {
		return p
	}
	if o.Now.IsZero() {
		o.Now = res.Tree.Finished
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	b := &builder{res: res, o: o, plan: p, seen: map[*walk.Node]bool{}}

	// Order matters only for two items on one node, where the first wins:
	// an orphan's own label says more than a detector row about the same
	// directory, and a detector row more than an application's cache.
	b.orphans()
	b.tools()
	b.runtimes()
	b.projects()
	b.appCaches()
	b.trash()
	b.notes()

	b.dedupe()
	b.sortAndTotal()
	return p
}

type builder struct {
	res  *scan.Result
	o    Options
	plan *Plan
	seen map[*walk.Node]bool
}

// add records an item, dropping a second item on a node already planned.
func (b *builder) add(it Item) {
	if it.node != nil {
		if b.seen[it.node] {
			return
		}
		b.seen[it.node] = true
		it.Node = it.node.ID
		if it.Path == "" {
			it.Path = it.node.Display()
		}
	} else {
		it.Node = -1
	}
	if it.Command == "" && it.Action == "" && it.Tier.Suggested() && it.node != nil {
		it.Action = moveToTrash
	}
	if it.Action == moveToTrash {
		it.AfterTrash = true
	}
	b.plan.Items = append(b.plan.Items, it)
}

// nodeAt finds the node behind a display path.
func (b *builder) nodeAt(display string) *walk.Node {
	if display == "" {
		return nil
	}
	n, ok := b.res.Tree.Lookup(mac.ScanPath(display))
	if !ok {
		return nil
	}
	return n
}

// nodeByID finds a node by its id.
func (b *builder) nodeByID(id int32) *walk.Node {
	if id < 0 || int(id) >= len(b.res.Tree.Nodes) {
		return nil
	}
	return b.res.Tree.Nodes[id]
}

// claimAt is the claim that decides a node's bytes.
func (b *builder) claimAt(n *walk.Node) (classify.Claim, bool) {
	if b.res.Class == nil || n == nil {
		return classify.Claim{}, false
	}
	return b.res.Class.OfNode(n)
}

// orphans turns the software that is gone into items: its data to check
// before removing, what must stay, and the lines that would bring it back.
func (b *builder) orphans() {
	rep, ok := scan.Apps(b.res)
	if !ok {
		return
	}
	for _, group := range [][]apps.Entry{rep.Orphans, rep.CaskOnly, rep.InTrash} {
		for _, e := range group {
			b.orphan(e)
		}
	}
}

func (b *builder) orphan(e apps.Entry) {
	why := e.Label + " is " + e.State
	for _, c := range e.Components {
		it := Item{
			ID: "apps:" + c.Path, Title: e.Label + " — " + componentName(c), node: b.nodeAt(c.Path),
			Path: c.Path, Source: "apps", Evidence: e.Evidence,
		}
		if it.node != nil {
			it.Bytes = it.node.Bytes
		}
		if c.Kept != "" {
			it.Tier, it.Impact = Never, "kept: "+c.Kept
		} else {
			it.Tier = Check
			it.Impact = why + "; check nothing else still uses it before removing it"
		}
		b.add(it)
	}
	for _, l := range e.ConfigLinks {
		b.add(Item{
			ID: "apps:" + l.Path, Tier: Never, Title: e.Label + " — your configuration",
			Path: l.Path, Source: "apps", node: b.nodeAt(l.Path),
			Impact: "a link into " + l.Target + "; the configuration lives in your repository, and removing the link frees nothing",
		})
	}
	for _, r := range e.References {
		b.plan.Edits = append(b.plan.Edits, editFor(e, r))
	}
}

// editFor describes one line of the user's files that names a removed app.
func editFor(e apps.Entry, r apps.Reference) Edit {
	switch r.Kind {
	case "cask", "brew":
		return Edit{
			File: r.File, Line: r.Line, Text: r.Kind + ` "` + r.Token + `"`,
			Why: "`brew bundle` would install " + e.Label + " again; remove the line and the description comment above it, if any",
		}
	default:
		return Edit{File: r.File, Line: r.Line, Text: r.Text, Why: "refers to " + e.Label + ", which is " + e.State}
	}
}

// componentName is the short name of an application's directory: its
// location and its name, "Caches/com.spotify.client".
func componentName(c apps.ComponentRef) string {
	return path.Join(path.Base(path.Dir(c.Path)), path.Base(c.Path))
}

// tools turns the detectors' rows into items.
//
// A row whose node another detector won is skipped, for the reason the
// developer section skips it: the classifier already decided whose it is,
// and two rows for one directory would promise its bytes twice.
func (b *builder) tools() {
	for _, st := range b.res.Detectors {
		for _, tool := range b.res.Summaries[st.Name].Tools {
			n := b.nodeByID(tool.Node)
			if n == nil {
				continue
			}
			cl, ok := b.claimAt(n)
			if ok && cl.Source.Kind == classify.SourceDetector && cl.Source.Detector != st.Name {
				continue
			}
			it := Item{
				ID: st.Name + ":" + tool.Path, Tier: tool.EffectiveTier(), Title: toolTitle(tool),
				node: n, Bytes: n.Bytes, Command: tool.Command, Impact: tool.Impact,
				Source: st.Name,
			}
			if ok {
				it.Evidence = cl.Evidence
				// The apps verdict has the last word on a directory
				// whose application is gone (D44): whatever the
				// detector thought of it, it is a leftover now.
				if cl.Reclaim == classify.Orphaned {
					it.Tier, it.Command = Check, ""
					it.Impact = "left behind by software that is no longer installed; check nothing still uses it"
				}
			}
			b.add(it)
		}
		if s := b.res.Summaries[st.Name]; s.Reclaimable > 0 {
			b.add(Item{
				ID: st.Name + ":reported", Tier: Safe, Reported: true, Bytes: s.Reclaimable,
				Title: st.Name + ": what the tool itself would free", Command: reportedCommand(st.Name),
				Impact: s.ReclaimNote, Source: st.Name,
			})
		}
	}
}

// reportedCommand names the command behind a tool's own reclaimable figure.
func reportedCommand(detector string) string {
	if detector == "homebrew" {
		return "brew cleanup"
	}
	return ""
}

// toolTitle is a row's name with its version.
func toolTitle(t detect.Tool) string {
	if t.Version != "" && !strings.Contains(t.Name, t.Version) {
		return t.Name + " " + t.Version
	}
	return t.Name
}

// runtimes turns what each container runtime's daemon reports into items.
// The host's disk image is in use; each df row is its own decision.
func (b *builder) runtimes() {
	for _, st := range b.res.Detectors {
		for _, rt := range b.res.Summaries[st.Name].Runtimes {
			for _, img := range rt.HostImage {
				b.add(Item{
					ID: st.Name + ":" + img.Path, Tier: InUse, Title: rt.Name + " — " + path.Base(img.Path),
					node: b.nodeByID(img.Node), Bytes: img.Bytes, Source: st.Name,
					Impact: "the runtime's disk image; it gives space back on its own after the prunes",
				})
			}
			for _, l := range rt.GuestReported {
				if l.Reclaimable == 0 && l.Tier != Never {
					continue
				}
				bytes := l.Reclaimable
				if l.Tier == Never {
					bytes = l.Size
				}
				b.add(Item{
					ID: st.Name + ":df:" + l.Type, Tier: l.Tier, Reported: true, Bytes: bytes,
					Title: rt.Name + " — " + strings.ToLower(l.Type), Command: l.Command, Impact: l.Impact,
					Source: st.Name,
				})
			}
		}
	}
}

// projects offers the build output of stale projects and accounts for that
// of the active ones.
func (b *builder) projects() {
	for _, st := range b.res.Detectors {
		for _, pr := range b.res.Summaries[st.Name].Projects {
			stale := !pr.LastActivity.IsZero() && b.o.Now.Sub(pr.LastActivity) >= b.o.StaleAfter
			for _, a := range pr.Artifacts {
				n := b.nodeByID(a.Node)
				if n == nil || a.Reclaim == classify.UserData || n.Bytes == 0 {
					continue
				}
				it := Item{
					ID: "projects:" + a.Path, Title: path.Base(pr.Root) + "/" + a.Name,
					node: n, Bytes: n.Bytes, Impact: a.Impact, Source: st.Name,
				}
				switch {
				case a.Tier != detect.TierUnset:
					it.Tier = a.Tier
				case pr.LastActivity.IsZero():
					it.Tier = Check
					it.Impact = "when the project was last used is unknown; " + a.Impact
				case stale:
					it.Tier = Reinstall
				default:
					it.Tier = InUse
					it.Impact = "the project was used " + pr.LastActivity.Format(time.DateOnly) + "; " + a.Impact
				}
				b.add(it)
			}
		}
	}
}

// appCaches offers the caches of installed applications.
func (b *builder) appCaches() {
	rep, ok := scan.Apps(b.res)
	if !ok {
		return
	}
	for _, e := range rep.Apps {
		for _, c := range e.Components {
			if c.Reclaim != classify.Regenerable.String() || c.Bucket != classify.BucketAppData.ID() || c.Bytes == 0 {
				continue
			}
			impact := "quit " + e.Label + " first; it rebuilds this as it is used"
			if c.Category == "Updater cache" {
				impact = "a staged update; " + e.Label + " downloads it again when it next updates"
			}
			n := b.nodeAt(c.Path)
			it := Item{
				ID: "apps:" + c.Path, Tier: Redownload, Title: e.Label + " — " + componentName(c),
				node: n, Path: c.Path, Impact: impact, Source: "apps",
			}
			if n != nil {
				it.Bytes = n.Bytes
			}
			b.add(it)
		}
	}
}

// trash reports the Trash: emptying it is what finally frees everything the
// plan moves there, and it cannot be undone. Each Trash the walk read is its
// own item, so an application in the Trash is not counted twice.
func (b *builder) trash() {
	l := b.res.Ledger
	if l == nil {
		return
	}
	for _, bk := range l.Buckets {
		if bk.ID != classify.BucketTrash.ID() {
			continue
		}
		b.plan.Trash = TrashState{Bytes: bk.Bytes, Known: bk.Known, Note: bk.Note}
		if !bk.Known {
			b.plan.Trash.Note = "the Trash could not be read; grant Full Disk Access to your terminal to see what emptying it would free"
		}
	}
	for _, n := range b.trashNodes() {
		if n.Bytes == 0 {
			continue
		}
		b.add(Item{
			ID: "ledger:" + n.Display(), Tier: Check, Title: "the Trash", node: n, Bytes: n.Bytes,
			Action: "empty the Trash in Finder", Source: "ledger",
			Impact: "cannot be undone; look through it first",
		})
	}
}

// trashNodes are every home's .Trash and the volume's .Trashes.
func (b *builder) trashNodes() []*walk.Node {
	var out []*walk.Node
	if users := b.nodeAt("/Users"); users != nil {
		for _, u := range users.Children {
			for _, c := range u.Children {
				if c.Name == ".Trash" {
					out = append(out, c)
				}
			}
		}
	}
	if n := b.nodeAt("/.Trashes"); n != nil {
		out = append(out, n)
	}
	return out
}

// notes are the plan's standing remarks.
func (b *builder) notes() {
	l := b.res.Ledger
	if l != nil && l.Purgeable.Known && l.Purgeable.Bytes > 0 {
		b.plan.Notes = append(b.plan.Notes,
			"purgeable space is not in the plan: macOS frees it on its own when space runs short")
	}
	b.plan.Notes = append(b.plan.Notes,
		"figures marked \"reported\" are the tools' own and overlap the walked bytes, so they are never added to them")
}

// dedupe gives every walked byte to exactly one item: the deepest one that
// holds it. Items are sorted by path; each item's bytes are reduced by the
// bytes of the items directly nested inside it.
func (b *builder) dedupe() {
	var idx []int
	for i, it := range b.plan.Items {
		if !it.Reported && it.node != nil {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(x, y int) bool { return b.plan.Items[idx[x]].Path < b.plan.Items[idx[y]].Path })

	items := b.plan.Items
	var stack []int
	for _, i := range idx {
		for len(stack) > 0 && !inside(items[i].node, items[stack[len(stack)-1]].node) {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			parent := &items[stack[len(stack)-1]]
			parent.Bytes = max(parent.Bytes-items[i].node.Bytes, 0)
		}
		stack = append(stack, i)
	}
}

// inside reports whether n lies within dir in the tree.
func inside(n, dir *walk.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p == dir {
			return true
		}
	}
	return false
}

// sortAndTotal orders the items tier by tier, largest first, and sums them.
func (b *builder) sortAndTotal() {
	rank := map[Tier]int{}
	for i, t := range detect.Tiers {
		rank[t] = i
	}
	items := b.plan.Items
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Tier != items[j].Tier {
			return rank[items[i].Tier] < rank[items[j].Tier]
		}
		if items[i].Bytes != items[j].Bytes {
			return items[i].Bytes > items[j].Bytes
		}
		return items[i].ID < items[j].ID
	})
	for _, t := range detect.Tiers {
		b.plan.Totals[t] = &Totals{}
	}
	for _, it := range items {
		tot := b.plan.Totals[it.Tier]
		if tot == nil {
			tot = &Totals{}
			b.plan.Totals[it.Tier] = tot
		}
		tot.Items++
		if it.Reported {
			tot.Reported += it.Bytes
			continue
		}
		tot.Walked += it.Bytes
		if it.AfterTrash {
			tot.AfterTrash += it.Bytes
		}
	}
}

// Freeable is the walked bytes of the tiers the plan suggests, each byte
// once.
func (p *Plan) Freeable() int64 {
	var n int64
	for t, tot := range p.Totals {
		if t.Suggested() {
			n += tot.Walked
		}
	}
	return n
}

// Filter keeps the items of the named tiers. An empty list keeps them all.
func (p *Plan) Filter(tiers []Tier) []Item {
	if len(tiers) == 0 {
		return p.Items
	}
	want := map[Tier]bool{}
	for _, t := range tiers {
		want[t] = true
	}
	var out []Item
	for _, it := range p.Items {
		if want[it.Tier] {
			out = append(out, it)
		}
	}
	return out
}
