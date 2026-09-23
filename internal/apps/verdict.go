package apps

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/walk"
)

// State is what became of an owner's application.
type State uint8

const (
	// StateInstalled is an owner whose application is on the volume.
	StateInstalled State = iota + 1
	// StateCaskOnly is a cask that is still installed while the
	// application it installed is gone.
	StateCaskOnly
	// StateInTrash is an application that has been dragged to the Trash
	// but not yet emptied, so its data is still on disk in full.
	StateInTrash
	// StateNonApp is software that never had an application bundle.
	StateNonApp
	// StateOwnBuild is something the user built themselves.
	StateOwnBuild
	// StateVendor is a publisher's shared directory, attributed to the
	// publisher because at least one of its applications is installed.
	StateVendor
	// StateOrphanLikely is data whose application appears to be gone.
	StateOrphanLikely
	// StateUnknown is an owner nothing could be concluded about. It is not
	// a failure: it is the honest answer whenever a probe was degraded or
	// the directory name means nothing to the alias table.
	StateUnknown
)

var stateNames = [...]string{
	"", "installed", "cask-only", "in-trash", "non-app", "own-build",
	"vendor", "orphan-likely", "unknown",
}

func (s State) String() string {
	if int(s) >= len(stateNames) {
		return "invalid"
	}
	return stateNames[s]
}

// Reclaimable reports whether an owner's data could be freed. It is true only
// for the three states that mean the software is not there any more.
func (s State) Reclaimable() bool {
	return s == StateOrphanLikely || s == StateCaskOnly || s == StateInTrash
}

// Verdict is what was concluded about one owner.
type Verdict struct {
	State      State
	Confidence classify.Confidence
	// Evidence are the verbatim lines behind the verdict.
	Evidence []string
	// Keep are the signals that blocked an orphan verdict. They are shown
	// even when the state is Installed, because "why is this not an
	// orphan" is as useful a question as the other one.
	Keep []string
	// LastWrite is the most recent modification across the owner's data.
	LastWrite time.Time
}

// DefaultRecentWindow is how recently an owner's data must have been written
// for the owner to be protected from an orphan verdict.
//
// Thirty days is long enough to cover a tool a person uses monthly and short
// enough that software removed last year is not protected by it. It matters:
// on the reference machine it is the signal that keeps a background agent's
// data directory, written today, out of the orphan list.
const DefaultRecentWindow = 30 * 24 * time.Hour

// DefaultTuningThreshold is how large an unknown owner must be before it is
// worth a line in the tuning log. Below it the alias table would grow faster
// than the answers improve.
const DefaultTuningThreshold = 50 << 20

// verdicts computes a verdict per owner and records the unknown ones that are
// large enough to be worth an alias-table row.
func (a *Analysis) verdicts(opts Options) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	window := opts.RecentWindow
	if window == 0 {
		window = DefaultRecentWindow
	}
	a.Verdicts = make(map[string]*Verdict, len(a.Owners))
	for _, key := range a.OwnerKeys() {
		a.Verdicts[key] = a.verdictFor(a.Owners[key], now, window)
	}
	a.writeTuningLog(opts)
}

// verdictFor decides one owner's state.
//
// The order is not arbitrary. Each step answers a question that makes the
// later ones unnecessary, and the expensive question — "is this an orphan" —
// is asked last, because it is the one where a wrong answer costs the most:
// telling someone that data belongs to software they still use is a way to
// lose their trust in every other line of the report.
func (a *Analysis) verdictFor(o *OwnerResult, now time.Time, window time.Duration) *Verdict {
	v := &Verdict{LastWrite: a.lastWrite(o)}

	switch o.Owner.Kind {
	case KindOwnBuild:
		v.State, v.Confidence = StateOwnBuild, o.Confidence
		v.Evidence = append(v.Evidence, o.Owner.Label+" is built on this machine, not installed")
		return v
	case KindNonApp:
		v.State, v.Confidence = StateNonApp, o.Confidence
		v.Evidence = append(v.Evidence, o.Owner.Label+" is command-line software and never had an application bundle")
		return v
	}

	live, stale, trashed := a.bundlesFor(o)
	if len(live) > 0 {
		v.State, v.Confidence = StateInstalled, classify.Strong
		v.Evidence = append(v.Evidence, "installed at "+live[0].Path)
		v.Keep = a.keepSignals(o, now, window)
		return v
	}

	if cask, ok := a.caskFor(o); ok {
		v.State, v.Confidence = StateCaskOnly, classify.Likely
		when := ""
		if !cask.InstalledAt.IsZero() {
			when = ", installed " + cask.InstalledAt.Format("2006-01-02")
		}
		expected := "its application"
		if len(cask.Apps) > 0 {
			expected = cask.Apps[0]
		}
		v.Evidence = append(v.Evidence,
			"cask "+cask.Token+" is still installed"+when,
			expected+" is not on the volume")
		return v
	}

	if len(trashed) > 0 {
		v.State, v.Confidence = StateInTrash, classify.Likely
		v.Evidence = append(v.Evidence, "the only copy is in the Trash: "+trashed[0].Path)
		return v
	}

	if o.Owner.Kind == KindVendor {
		if b, ok := a.vendorInstalled(o); ok {
			v.State, v.Confidence = StateVendor, classify.Corroborating
			v.Evidence = append(v.Evidence,
				o.Owner.Label+" is a publisher folder shared by several products, "+
					"so it is not attributable to one",
				"at least one of them is installed: "+b.Path)
			v.Keep = a.keepSignals(o, now, window)
			return v
		}
		// Nothing inside the folder belongs to anything that is installed,
		// so the folder is residue like any other and takes the ordinary
		// path below. A publisher folder is protected by the publisher's
		// products, not by being a publisher folder.
		v.Evidence = append(v.Evidence,
			"nothing inside "+o.Owner.Label+" belongs to an installed application")
	}

	// Keep signals (D24). Each one is a reason to believe the software is
	// still here despite the missing bundle, and each is recorded whether
	// or not it changes the verdict.
	v.Keep = a.keepSignals(o, now, window)
	if len(v.Keep) > 0 {
		switch {
		case a.hasLiveLaunchItem(o):
			v.State, v.Confidence = StateInstalled, classify.Likely
			v.Evidence = append(v.Evidence, "a launch item for "+o.Owner.Label+" is still configured to run")
		case a.hasLiveReceipt(o):
			v.State, v.Confidence = StateInstalled, classify.Likely
			v.Evidence = append(v.Evidence, "an installer receipt for "+o.Owner.Label+" still points at an existing location")
		default:
			v.State, v.Confidence = StateUnknown, classify.UnknownOwner
			v.Evidence = append(v.Evidence,
				"no application bundle, but the directory was written "+humanAge(o.LastTouch, now)+" ago")
		}
		return v
	}

	// The orphan precondition (D36). Nothing on the volume carries the
	// owner's identifier or name, except copies that are themselves
	// evidence of absence: a staged update inside the owner's own cache is
	// not an installation, and treating one as such would hide every
	// self-updating application's orphaned data.
	switch {
	case len(stale) > 0:
		v.State = StateOrphanLikely
		v.Evidence = append(v.Evidence, a.staleCopyNote(stale[0]))
	case o.Owner.Kind == KindUnknown:
		// Nothing identified the directory in the first place, so there
		// is no application to say is missing. An unknown owner is not
		// an orphan: it is a gap in the alias table, and the tuning log
		// is where that gets recorded.
		v.State, v.Confidence = StateUnknown, classify.UnknownOwner
		v.Evidence = append(v.Evidence, "nothing on this machine names "+o.Owner.Label)
		return v
	default:
		v.State = StateOrphanLikely
		v.Evidence = append(v.Evidence, "no application bundle for "+o.Owner.Label+" anywhere on the volume")
	}

	v.Confidence, v.Evidence = a.orphanConfidence(o, v.Evidence)
	// An orphan verdict is a claim about what is *not* on the machine, so it
	// is only as good as the search behind it. When the probes that would
	// have found the software could not run, or when a path that would have
	// been a keep signal could not be stat'd, the honest answer is that
	// nothing is known — not that the software is gone.
	if gaps := a.incompleteEvidence(o); len(gaps) > 0 {
		v.State, v.Confidence = StateUnknown, classify.UnknownOwner
		v.Evidence = append(v.Evidence, gaps...)
	}
	return v
}

// orphanProbes are the probes whose failure leaves an orphan verdict without
// the evidence that would have contradicted it: the casks that name an
// application, the installer receipts and the launch items that show something
// is still configured to run, the LaunchServices register, and the listing of
// the directories an application is installed in. Lose any of those and the
// search an orphan verdict claims to have done was not done.
//
// LaunchServices earns its place through the inventory rather than through the
// verdict. Its most visible use points the other way — a registration at a
// path that is gone is what promotes an orphan from possible to likely — but
// the dump is also where a bundle inside the scanned tree gets its identifier
// when no Info.plist was read for it. Without it those bundles are name-only,
// an owner keyed on a bundle id stops matching the application that is sitting
// on the disk, and the verdict is an orphan for software that never left.
// That is a keep signal lost, which is exactly what this list is for.
var orphanProbes = map[string]bool{
	probeBrew:         true,
	probePkgutil:      true,
	probeLSRegister:   true,
	probeApplications: true,
	probeLaunchd:      true,
}

// incompleteEvidence lists the reasons an orphan verdict cannot be reached for
// this owner: probes that did not run, and paths that could not be checked.
//
// The two are the same failure seen from different ends. A degraded probe is a
// class of evidence nobody gathered; an unreadable path is one particular
// piece of it. Either way the search that an orphan verdict rests on was not
// the search it claims to have been, and inventory.go's rule — a degraded
// probe must never become a verdict — applies to both.
func (a *Analysis) incompleteEvidence(o *OwnerResult) []string {
	var out []string
	for _, deg := range a.Inventory.Degraded {
		if orphanProbes[deg.Probe] {
			out = append(out, "orphan evidence incomplete: "+deg.Probe+" "+deg.Reason)
		}
	}
	return append(out, a.uncheckedFor(o)...)
}

// uncheckedFor lists the owner's own paths that could not be stat'd.
//
// Each one is a keep signal that may or may not exist. A launch item whose
// program could not be checked might be running the software right now; a
// receipt whose install location could not be checked might point at a
// directory that is still there. Reading either as absence is how a machine
// without Full Disk Access reports its installed software as orphaned.
func (a *Analysis) uncheckedFor(o *OwnerResult) []string {
	var out []string
	for _, item := range a.Inventory.LaunchItems {
		if item.CheckErr == "" {
			continue
		}
		for _, id := range o.IDs {
			if item.Owns(id) {
				out = append(out, item.CheckErr)
				break
			}
		}
	}
	for _, rec := range a.Inventory.Receipts {
		if rec.CheckErr == "" || !a.receiptOwnedBy(rec, o) {
			continue
		}
		out = append(out, rec.CheckErr)
	}
	for _, id := range o.IDs {
		for _, e := range a.Inventory.RegistryFor(id) {
			if e.CheckErr != "" {
				out = append(out, e.CheckErr)
			}
		}
	}
	dedupeInPlace(&out)
	return out
}

// orphanConfidence grades an orphan verdict.
//
// Likely means something on the machine records a past installation: a
// LaunchServices entry pointing at a path that is gone, an installer receipt
// whose location is gone, a cask that names the data, or an alias hit on a
// reverse-DNS identifier, which only appears on disk because an application
// put it there. Corroborating — the report prints it as "possible" — means
// the only evidence is a directory whose name resembles a product, which is a
// guess and is labelled as one.
func (a *Analysis) orphanConfidence(o *OwnerResult, ev []string) (classify.Confidence, []string) {
	for _, i := range o.Members {
		m := a.Matches[i]
		switch m.Rule {
		case "apps/cask-zap", "apps/cask-quit", "apps/cask-app-name":
			return classify.Likely, append(ev, "a cask receipt still lists this data")
		case "apps/receipt":
			return classify.Likely, append(ev, "an installer receipt records the installation")
		}
		if m.IDMatch {
			return classify.Likely, append(ev,
				"the directory is named after a bundle identifier, which only an installation creates")
		}
	}
	if e, id, found := a.staleRegistration(o); found {
		return classify.Likely, append(ev,
			"LaunchServices still lists "+id+" at "+e.Path+", which is gone")
	}
	return classify.Corroborating, append(ev,
		"the only evidence is the directory name, so this is possible rather than likely")
}

// staleRegistration finds a LaunchServices entry for this owner whose path is
// gone, which is a record of an installation that has been removed.
//
// The entry is matched against the owner's own identifiers and, failing that,
// through the alias table: LaunchServices listing "com.tabnine.TabNine" at a
// path that no longer exists is evidence about TabNine whether or not the
// directory that named the owner happened to be an identifier.
func (a *Analysis) staleRegistration(o *OwnerResult) (RegistryEntry, string, bool) {
	for _, id := range o.IDs {
		for _, e := range a.Inventory.RegistryFor(id) {
			// An entry whose path could not be stat'd says nothing
			// about whether the bundle was removed.
			if !e.Exists && e.CheckErr == "" {
				return e, id, true
			}
		}
	}
	if o.Owner.Slug == "" {
		return RegistryEntry{}, "", false
	}
	for _, e := range a.Inventory.Registry {
		if e.Exists || e.CheckErr != "" {
			continue
		}
		if p, ok := defaultIndex.LookupID(e.ID); ok && p.Slug == o.Owner.Slug {
			return e, e.ID, true
		}
	}
	return RegistryEntry{}, "", false
}

// bundlesFor splits the bundles carrying an owner's identifiers and names
// into the three groups the verdict turns on: installations, staged copies
// that prove nothing, and copies in the Trash.
func (a *Analysis) bundlesFor(o *OwnerResult) (live, stale, trashed []*Bundle) {
	seen := make(map[*Bundle]bool)
	consider := func(b *Bundle) {
		if b == nil || seen[b] {
			return
		}
		seen[b] = true
		switch {
		case b.Source == SourceTrash:
			trashed = append(trashed, b)
		case b.Source.Installed():
			live = append(live, b)
		case a.isStaleCopy(b, o):
			stale = append(stale, b)
		case !a.Inventory.Paths.OnVolume(b.Path):
			// A copy on a Time Machine disk, a mounted image or a
			// cloned system volume is not this volume's installation.
			// Counting one as such is how an application deleted from
			// this disk keeps looking installed forever, which is the
			// answer that makes the whole report untrustworthy.
			stale = append(stale, b)
		default:
			// A bundle somewhere unconventional — a JetBrains Toolbox
			// directory, a Downloads folder — is still an installation.
			live = append(live, b)
		}
	}
	for _, b := range o.Bundles {
		consider(b)
	}
	for _, id := range o.IDs {
		for _, b := range a.Inventory.ByID[id] {
			consider(b)
		}
	}
	for _, name := range o.Names {
		for _, k := range nameKeys(name) {
			for _, b := range a.Inventory.ByName[k] {
				consider(b)
			}
		}
	}
	return live, stale, trashed
}

// staleCopyNote says what sort of copy was found in place of an installation.
func (a *Analysis) staleCopyNote(b *Bundle) string {
	if !a.Inventory.Paths.OnVolume(b.Path) {
		return "the only copy is on another volume at " + b.Path
	}
	return "the only copy is a staged update at " + b.Path
}

// vendorInstalled reports whether any of a publisher's products is installed,
// and names the bundle that proves it.
//
// This is what decides whether ~/Library/Application Support/Google is a live
// publisher folder or residue, and it is deliberately not a question about the
// folder's name: the folder is kept because Chrome or Android Studio is
// installed, and once every Google product has gone it is left behind like any
// other orphan.
//
// Two signals answer it, because neither is enough alone. An installed bundle
// under the publisher's reverse-DNS prefix is the direct answer, and it is the
// one that keeps "BraveSoftware" while com.brave.Browser is installed even
// though nothing inside the folder is named after it. A child of the folder
// that resolved to something installed is the other, and it is the one that
// keeps "Smart Code ltd" while Stremio ships under com.westbridge.
func (a *Analysis) vendorInstalled(o *OwnerResult) (*Bundle, bool) {
	if prefix, ok := strings.CutPrefix(o.Owner.Key, "vendor:"); ok && prefix != "" {
		if signed := a.Inventory.InstalledForVendor(prefix); len(signed) > 0 {
			return signed[0], true
		}
	}
	for _, i := range a.vendorKids[o.Owner.Key] {
		child, ok := a.Owners[a.Matches[i].Owner.Key]
		if !ok || child == o {
			continue
		}
		if live, _, _ := a.bundlesFor(child); len(live) > 0 {
			return live[0], true
		}
	}
	return nil, false
}

// isStaleCopy reports whether a bundle is a copy that proves nothing: one
// inside a cache, inside a staging directory, or inside the owner's own data.
func (a *Analysis) isStaleCopy(b *Bundle, o *OwnerResult) bool {
	p := b.Path
	if strings.Contains(p, "/Library/Caches/") || strings.Contains(p, "/Updates/") {
		return true
	}
	for _, i := range o.Members {
		if dir := a.Candidates[i].Path; dir != "" && strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

// caskFor returns the cask behind a cask-only owner.
func (a *Analysis) caskFor(o *OwnerResult) (*Cask, bool) {
	if token, ok := strings.CutPrefix(o.Owner.Key, "cask:"); ok {
		if c, found := a.Inventory.CaskByToken[token]; found {
			return c, true
		}
	}
	if o.Owner.Slug == "" {
		return nil, false
	}
	// A product row names the casks that install it, which is how a
	// product with no bundle is recognised as a cask-only install rather
	// than an orphan.
	p, ok := defaultIndex.byName[strings.ToLower(o.Owner.Label)]
	if !ok {
		return nil, false
	}
	for _, token := range p.Casks {
		if c, found := a.Inventory.CaskByToken[token]; found && c.HasApp() {
			return c, true
		}
	}
	return nil, false
}

// keepSignals lists the reasons to believe the software is still here.
func (a *Analysis) keepSignals(o *OwnerResult, now time.Time, window time.Duration) []string {
	var keep []string
	if !o.LastTouch.IsZero() && now.Sub(o.LastTouch) < window {
		keep = append(keep, "written "+humanAge(o.LastTouch, now)+" ago")
	}
	for _, item := range a.liveLaunchItems(o) {
		keep = append(keep, item.Describe())
	}
	for _, rec := range a.liveReceipts(o) {
		keep = append(keep, "installer receipt "+rec.PkgID+" still points at "+rec.InstallPath())
	}
	return keep
}

// liveLaunchItems are the owner's launch items whose program still exists.
func (a *Analysis) liveLaunchItems(o *OwnerResult) []LaunchItem {
	var out []LaunchItem
	for _, item := range a.Inventory.LaunchItems {
		if !item.ProgramExists {
			continue
		}
		for _, id := range o.IDs {
			if item.Owns(id) {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

func (a *Analysis) hasLiveLaunchItem(o *OwnerResult) bool { return len(a.liveLaunchItems(o)) > 0 }

// liveReceipts are the owner's installer receipts whose location still exists.
func (a *Analysis) liveReceipts(o *OwnerResult) []Receipt {
	var out []Receipt
	for _, rec := range a.Inventory.Receipts {
		if rec.LocationExists && a.receiptOwnedBy(rec, o) {
			out = append(out, rec)
		}
	}
	return out
}

// receiptOwnedBy reports whether an installer receipt describes this owner,
// by its package id or by the vendor prefix the id shares with the owner's.
func (a *Analysis) receiptOwnedBy(rec Receipt, o *OwnerResult) bool {
	id := strings.TrimSuffix(rec.PkgID, ".pkg")
	for _, ownerID := range o.IDs {
		if id == ownerID || (rec.VendorPrefix() != "" && strings.HasPrefix(ownerID, rec.VendorPrefix()+".")) {
			return true
		}
	}
	return false
}

func (a *Analysis) hasLiveReceipt(o *OwnerResult) bool { return len(a.liveReceipts(o)) > 0 }

// lastWrite fills in the owner's two timestamps.
//
// They are deliberately different questions. LastWrite is the newest
// modification anywhere in the owner's data, which is what the report shows
// and what a person means by "when did I last touch this". LastTouch is the
// newest modification of the owner's own directories, and it is the only one
// the keep signal reads.
//
// The distinction is not academic. On the reference machine the leftover data
// of four browsers nobody has run in months all carried the same deep-file
// timestamp to the second, because something swept the volume: a backup, an
// indexer, a migration. Read as activity it protected four real orphans from
// being reported. A directory's own mtime changes when entries are added or
// removed inside it, which is what an application writing there actually
// does, and a sweep that only touches files does not move it.
func (a *Analysis) lastWrite(o *OwnerResult) time.Time {
	var newest, touched int64
	for _, i := range o.Members {
		if n := a.Candidates[i].Node; n != nil {
			newest = max(newest, subtreeMtime(n))
			touched = max(touched, n.Mtime)
		}
	}
	for _, b := range o.Bundles {
		if b.Node != nil {
			newest = max(newest, subtreeMtime(b.Node))
			touched = max(touched, b.Node.Mtime)
		}
	}
	o.LastWrite, o.LastTouch = time.Time{}, time.Time{}
	if newest > 0 {
		o.LastWrite = time.Unix(newest, 0).UTC()
	}
	if touched > 0 {
		o.LastTouch = time.Unix(touched, 0).UTC()
	}
	return o.LastWrite
}

// maxMtimeDepth bounds how far into a subtree the modification scan goes. An
// owner's freshness is decided by its own directory and the level or two
// below it; walking a seven-gigabyte cache to the leaves would cost more than
// the answer is worth.
const maxMtimeDepth = 3

// subtreeMtime is the newest modification time at or just below a node.
func subtreeMtime(n *walk.Node) int64 {
	newest := n.Mtime
	var walkDown func(*walk.Node, int)
	walkDown = func(cur *walk.Node, depth int) {
		newest = max(newest, cur.Mtime)
		if depth >= maxMtimeDepth {
			return
		}
		for _, kid := range cur.Children {
			walkDown(kid, depth+1)
		}
	}
	walkDown(n, 0)
	return newest
}

// humanAge renders how long ago a time was, in the coarsest useful unit.
func humanAge(t, now time.Time) string {
	if t.IsZero() {
		return "an unknown time"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return "less than an hour"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " hours"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " days"
	}
}

// writeTuningLog records the unknown owners large enough to be worth an
// alias-table row.
//
// The writer is injected and nil by default. A package that wrote to the
// user's Application Support as a side effect of being tested would be a
// package nobody could test twice, and the whole posture here is that storix
// writes one file and asks first.
func (a *Analysis) writeTuningLog(opts Options) {
	if opts.TuningLog == nil {
		return
	}
	threshold := opts.TuningThreshold
	if threshold == 0 {
		threshold = DefaultTuningThreshold
	}

	type row struct {
		key   string
		bytes int64
		path  string
	}
	var rows []row
	for key, o := range a.Owners {
		v := a.Verdicts[key]
		if v == nil || v.State != StateUnknown || o.Bytes < threshold {
			continue
		}
		p := ""
		if len(o.Members) > 0 {
			p = a.Candidates[o.Members[0]].Path
		}
		rows = append(rows, row{key, o.Bytes, p})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].key < rows[j].key
	})
	for _, r := range rows {
		line := r.key + "\t" + strconv.FormatInt(r.bytes, 10) + "\t" + r.path + "\n"
		if _, err := opts.TuningLog.Write([]byte(line)); err != nil {
			return
		}
	}
}

// CaskOnlyCasks lists the casks that are still installed while the
// application they installed is not on the volume.
//
// This is a property of the cask rather than of an owner, which is why it is
// computed here and not from the verdict map: Homebrew leaves a dangling
// symlink and a receipt behind, and the question "which casks think they
// installed something that is gone" has a different answer from "whose data
// is orphaned". On the reference machine the two differ by exactly one
// entry, because the stremioservice cask's application is missing while
// Stremio itself is installed and still owns its data.
func (a *Analysis) CaskOnlyCasks() []*Cask {
	var out []*Cask
	for _, c := range a.Inventory.Casks {
		if !c.HasApp() {
			continue
		}
		found := false
		for _, n := range c.AppNames() {
			if _, ok := a.Inventory.InstalledByName(n); ok {
				found = true
				break
			}
		}
		if !found {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Token < out[j].Token })
	return out
}

// Verdict is the verdict for an owner key.
func (a *Analysis) Verdict(key string) (*Verdict, bool) {
	v, ok := a.Verdicts[key]
	return v, ok
}

// OwnersInState lists the owner keys in a state, in a deterministic order.
func (a *Analysis) OwnersInState(s State) []string {
	var out []string
	for _, key := range a.OwnerKeys() {
		if v := a.Verdicts[key]; v != nil && v.State == s {
			out = append(out, key)
		}
	}
	return out
}
