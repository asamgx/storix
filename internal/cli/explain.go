package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/asamgx/storix/internal/apps"
	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/report"
	"github.com/asamgx/storix/internal/scan"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

func init() { register(newExplainCmd()) }

// explainOptions holds the flags of `storix explain`.
type explainOptions struct {
	roots      []string
	json       bool
	binary     bool
	fromCache  bool
	scan       bool
	debug      bool
	disableDet []string
}

func newExplainCmd() *cobra.Command {
	o := &explainOptions{}
	cmd := &cobra.Command{
		Use:   "explain PATH|BUNDLEID",
		Short: "Say why one directory, or one application's data, is counted the way it is",
		Long: `explain answers the question the ledger raises: why is this directory in
that bucket, and who does it belong to.

Given a path it prints the directory's size, the bucket and category it
landed in, its owner and reclaimability, the rule, detector or application
inventory that claimed it, the evidence behind that claim, the claims that
lost, and, when the owner is an application, what that application costs in
total across every bucket.

Given a bundle id, an owner key (app:…, cask:…, cli:…, project:…) or a
product name it prints that owner's footprint instead: every directory
attributed to it, wherever the ledger counted it.

Unlike the other commands this one does not insist on a recent scan. An
explanation is about a stored answer, so the latest stored scan is used
whatever its age and its age is printed; --scan walks the disk first and
--from-cache refuses to walk at all.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplain(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, args[0])
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&o.roots, "roots", []string{mac.DataRoot}, "roots to scan")
	f.BoolVar(&o.json, "json", false, "print the explanation as JSON instead")
	f.BoolVar(&o.binary, "binary", false, "format sizes in KiB/MiB/GiB instead of Finder's KB/MB/GB")
	f.BoolVar(&o.fromCache, "from-cache", false, "use the stored scan and never walk the disk")
	f.BoolVar(&o.scan, "scan", false, "walk the disk instead of explaining the stored scan")
	f.BoolVar(&o.debug, "debug", false, "print timings")
	f.StringArrayVar(&o.disableDet, "disable-detector", nil,
		"switch off one tool detector by name; repeatable")
	return cmd
}

// runExplain answers for one argument: a path, or an owner.
func runExplain(ctx context.Context, out, errOut io.Writer, o *explainOptions, arg string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	u, cfg, err := o.resolve()
	if err != nil {
		return err
	}
	res, err := explainScan(ctx, errOut, cfg, o.scan, o.fromCache)
	if err != nil {
		return err
	}

	doc, err := explainOf(res, arg)
	if err != nil {
		return err
	}

	if o.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	return doc.write(out, u)
}

// explainOf answers one argument against one scan. It is the whole of the
// command apart from the input and the output, so a test can ask the question
// without walking a disk.
func explainOf(res *scan.Result, arg string) (explainDoc, error) {
	doc := explainDoc{Schema: report.SchemaVersion, Query: arg, Scan: explainScanDoc{
		FromCache: res.FromCache, AgeNS: res.CacheAge.Nanoseconds(),
	}}
	var err error
	if looksLikePath(arg) {
		doc.Mode = "path"
		doc.Path, err = explainPath(res, arg)
	} else {
		doc.Mode = "owner"
		doc.Owner, err = explainOwner(res, arg)
	}
	return doc, err
}

// resolve turns the flags into the units and the scan configuration.
func (o *explainOptions) resolve() (units.Format, scan.Config, error) {
	var cfg scan.Config

	if o.fromCache && o.scan {
		return units.Decimal, cfg, &ConfigError{Err: fmt.Errorf("choose either --from-cache or --scan, not both")}
	}

	u := units.Decimal
	if o.binary {
		u = units.Binary
	}
	cfg = scan.Config{
		Roots:             o.roots,
		Units:             u,
		Debug:             o.debug,
		FromCache:         !o.scan,
		DisabledDetectors: o.disableDet,
		Version:           BuildInfo(),
	}
	return u, cfg, nil
}

// explainScan produces the scan to explain.
//
// The freshness rule the other commands follow is deliberately not applied
// here: an explanation is about an answer that was already given, so the
// stored scan is used whatever its age and the age is printed beside it. The
// two flags are the exceptions: --scan walks first, and --from-cache makes an
// empty store an error rather than a reason to walk.
func explainScan(ctx context.Context, errOut io.Writer, cfg scan.Config, forceScan, cacheOnly bool) (*scan.Result, error) {
	if !forceScan {
		res, err := cachedScan(cfg, errOut)
		switch {
		case err == nil && res != nil:
			return res, nil
		case err == nil:
			// cachedScan only returns (nil, nil) when caching is off.
		case cacheOnly || !errors.Is(err, scan.ErrNoCache):
			return nil, err
		default:
			_, _ = fmt.Fprintln(errOut, "storix: there is no stored scan yet; scanning")
		}
	}
	fresh := cfg
	fresh.FromCache = false
	return runOne(ctx, errOut, fresh)
}

// looksLikePath reports whether the argument names a directory rather than an
// owner. A path is anything holding a separator or a tilde, and anything that
// exists on disk; a bundle id, an owner key and a product name never do.
func looksLikePath(arg string) bool {
	if strings.ContainsAny(arg, "/~") {
		return true
	}
	_, err := os.Stat(arg)
	return err == nil
}

// explainDoc is one explanation, and the --json document.
type explainDoc struct {
	Schema int    `json:"schema"`
	Query  string `json:"query"`
	// Mode is "path" or "owner": which of the two questions was answered.
	Mode  string           `json:"mode"`
	Scan  explainScanDoc   `json:"scan"`
	Path  *explainPathDoc  `json:"path,omitempty"`
	Owner *explainOwnerDoc `json:"owner,omitempty"`
}

// explainScanDoc says where the answer came from.
type explainScanDoc struct {
	FromCache bool  `json:"fromCache"`
	AgeNS     int64 `json:"age_ns,omitempty"`
}

// explainPathDoc is the answer about one directory.
type explainPathDoc struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Files    uint32 `json:"files"`
	Modified string `json:"modified,omitempty"`

	Bucket      int    `json:"bucket"`
	BucketID    string `json:"bucketId"`
	BucketLabel string `json:"bucketLabel"`
	Category    string `json:"category,omitempty"`

	Owner      string   `json:"owner,omitempty"`
	OwnerKeys  []string `json:"ownerKeys,omitempty"`
	State      string   `json:"state,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	Reclaim    string   `json:"reclaim,omitempty"`

	Source        string `json:"source,omitempty"`
	Inherited     bool   `json:"inherited,omitempty"`
	InheritedFrom string `json:"inheritedFrom,omitempty"`

	Evidence      []string             `json:"evidence,omitempty"`
	AlsoClaimedBy []explainConflictDoc `json:"alsoClaimedBy,omitempty"`
	Footprint     *explainOwnerDoc     `json:"footprint,omitempty"`
	Subtree       []explainBucketDoc   `json:"subtree,omitempty"`
}

// explainConflictDoc is one claim that lost this node.
type explainConflictDoc struct {
	Loser  string `json:"loser"`
	Winner string `json:"winner"`
}

// explainBucketDoc is one bucket's share of a subtree.
type explainBucketDoc struct {
	Bucket int    `json:"bucket"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Bytes  int64  `json:"bytes"`
}

// explainOwnerDoc is the answer about one owner: the footprint that crosses
// the ledger's buckets, with the directories it is made of.
type explainOwnerDoc struct {
	Key        string              `json:"key"`
	Label      string              `json:"label"`
	State      string              `json:"state,omitempty"`
	Confidence string              `json:"confidence,omitempty"`
	Footprint  apps.Sizes          `json:"footprint"`
	Components []apps.ComponentRef `json:"components,omitempty"`
	Evidence   []string            `json:"evidence,omitempty"`
}

// explainPath answers about one directory of the walked tree.
func explainPath(res *scan.Result, arg string) (*explainPathDoc, error) {
	node, ok := res.Tree.Lookup(mac.ScanPath(arg))
	if !ok {
		return nil, &ConfigError{Err: fmt.Errorf(
			"there is no %q in the stored scan (%s); files under 64 KB are folded into their parent, so try the directory that holds it",
			mac.DisplayPath(mac.ScanPath(arg)), explainProvenance(res))}
	}

	d := &explainPathDoc{
		Path: node.Display(), Bytes: node.Bytes, Files: node.Files,
		Bucket: int(classify.BucketOther), BucketID: classify.BucketOther.ID(),
		BucketLabel: classify.BucketOther.Label(),
	}
	if node.Mtime > 0 {
		d.Modified = time.Unix(node.Mtime, 0).Format(time.DateOnly)
	}

	class := res.Class
	cl, claimed := class.OfNode(node)
	if claimed {
		d.Bucket, d.BucketID, d.BucketLabel = int(cl.Bucket), cl.Bucket.ID(), cl.Bucket.Label()
		d.Category = cl.Category
		d.Owner, d.OwnerKeys = cl.Owner, cl.OwnerKeys
		if cl.Confidence != 0 {
			d.Confidence = cl.Confidence.String()
		}
		d.Reclaim = cl.Reclaim.String()
		d.Source = sourceText(cl.Source)
		d.Evidence = cl.Evidence
		if from := class.InheritedFromNode(node); from != nil {
			d.Inherited, d.InheritedFrom = true, from.Display()
		}
	}
	for _, c := range class.Conflicts {
		if c.Node == node.ID {
			d.AlsoClaimedBy = append(d.AlsoClaimedBy,
				explainConflictDoc{Loser: sourceText(c.Loser), Winner: sourceText(c.Winner)})
		}
	}
	d.Footprint = pathFootprint(res, cl)
	if d.Footprint != nil {
		d.State = d.Footprint.State
	}
	d.Subtree = explainSubtree(class, node)
	return d, nil
}

// explainSubtree splits a subtree by bucket, largest first, and returns
// nothing when the whole subtree sits in one bucket.
//
// The split is over own bytes — a directory's total less its children's — so
// the shares partition the subtree exactly as the ledger partitions the disk,
// and a directory whose children were claimed by three different detectors
// says so instead of reporting its own claim for all of them.
func explainSubtree(class *classify.Classification, root *walk.Node) []explainBucketDoc {
	if class == nil || root == nil {
		return nil
	}
	byBucket := make(map[classify.Bucket]int64, len(classify.Buckets()))
	stack := []*walk.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		own := n.Bytes
		for _, kid := range n.Children {
			own -= kid.Bytes
			stack = append(stack, kid)
		}
		if own <= 0 {
			continue
		}
		b := classify.BucketOther
		if cl, ok := class.Of(n.ID); ok {
			b = cl.Bucket
		}
		byBucket[b] += own
	}

	var out []explainBucketDoc
	for _, b := range classify.Buckets() {
		if byBucket[b] > 0 {
			out = append(out, explainBucketDoc{
				Bucket: int(b), ID: b.ID(), Label: b.Label(), Bytes: byBucket[b],
			})
		}
	}
	if len(out) < 2 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// explainOwner answers about one owner: a bundle id, an owner key or a label.
func explainOwner(res *scan.Result, arg string) (*explainOwnerDoc, error) {
	if rep, ok := scan.Apps(res); ok {
		if e, ok := matchEntry(rep, arg); ok {
			return ownerDocOf(e), nil
		}
	}
	if d, ok := ownerFromClassification(res.Class, arg); ok {
		return d, nil
	}
	return nil, &ConfigError{Err: ownerNotFound(res, arg)}
}

// ownerDocOf turns an application inventory entry into the answer.
func ownerDocOf(e apps.Entry) *explainOwnerDoc {
	d := &explainOwnerDoc{
		Key: e.Owner, Label: e.Label, State: e.State, Confidence: e.Confidence,
		Footprint: e.Footprint, Evidence: e.Evidence,
	}
	d.Components = append(d.Components, e.Components...)
	for i := range d.Components {
		d.Components[i].Source = collapseAppsPrefix(d.Components[i].Source)
	}
	sortComponents(d.Components)
	return d
}

// pathFootprint is the footprint line of the path block: what the directory's
// owner costs in total, across every bucket.
//
// The application inventory answers first, and only on an owner key: a claim
// carrying "cli:pnpm" must not pick up an inventory row that merely shares a
// label with it. For an owner the inventory does not know — a toolchain, a
// project — the classification's own join stands in, and only when it reaches
// past this one directory, since a footprint equal to the line above it tells
// a reader nothing.
func pathFootprint(res *scan.Result, cl classify.Claim) *explainOwnerDoc {
	if len(cl.OwnerKeys) == 0 {
		return nil
	}
	if rep, ok := scan.Apps(res); ok {
		if e, ok := appsEntryFor(rep, cl.OwnerKeys); ok {
			return ownerDocOf(e)
		}
	}
	d, ok := footprintFor(res.Class, cl.OwnerKeys, cl.Owner)
	if !ok || len(d.Components) < 2 {
		return nil
	}
	return d
}

// ownerFromClassification is the answer for an owner the application
// inventory does not know: a toolchain, a project, anything a detector or a
// catalog rule owns. The footprint is then the join on the owner key, which
// is the same join the inventory itself uses.
func ownerFromClassification(class *classify.Classification, arg string) (*explainOwnerDoc, bool) {
	if class == nil {
		return nil, false
	}
	keys := ownerKeyCandidates(arg)
	label := arg
	if l, total, ok := ownerByLabel(class, arg); ok {
		keys = append(keys, total.Keys...)
		label = l
	}
	return footprintFor(class, keys, label)
}

// footprintFor joins the classification on a set of owner keys and builds the
// footprint they share.
func footprintFor(class *classify.Classification, keys []string, label string) (*explainOwnerDoc, bool) {
	if class == nil {
		return nil, false
	}
	d := &explainOwnerDoc{Key: label}
	seen := make(map[int32]bool)
	for _, key := range keys {
		for _, id := range class.ByOwnerKey(key) {
			cl, ok := class.Of(id)
			if !ok || cl.Node == nil || seen[id] {
				continue
			}
			seen[id] = true
			if d.Label == "" {
				d.Label, d.Key = cl.Owner, key
			}
			d.Components = append(d.Components, apps.ComponentRef{
				Path: cl.Node.Display(), Bytes: cl.Node.Bytes, Bucket: cl.Bucket.ID(),
				Category: cl.Category, Source: sourceText(cl.Source), Reclaim: cl.Reclaim.String(),
			})
			addToSizes(&d.Footprint, cl.Bucket, cl.Category, cl.Node.Bytes)
		}
	}
	if len(d.Components) == 0 {
		return nil, false
	}
	if d.Label == "" {
		d.Label = label
	}
	sortComponents(d.Components)
	return d, true
}

// ownerByLabel finds an owner total by its display label, case-insensitively.
func ownerByLabel(class *classify.Classification, arg string) (string, *classify.OwnerTotal, bool) {
	for label, total := range class.Owners {
		if strings.EqualFold(label, arg) {
			return label, total, true
		}
	}
	return "", nil, false
}

// ownerKeyCandidates expands a bare argument into the prefixed owner keys it
// could mean. An already-prefixed argument is taken as it stands.
func ownerKeyCandidates(arg string) []string {
	if strings.Contains(arg, ":") {
		return []string{arg}
	}
	lower := strings.ToLower(arg)
	out := []string{arg}
	for _, p := range []string{"app:", "cask:", "cli:", "project:", "product:", "vendor:", "team:", "unknown:"} {
		out = append(out, p+arg)
		if lower != arg {
			out = append(out, p+lower)
		}
	}
	return out
}

// addToSizes adds one component's bytes to the right part of a footprint,
// splitting App data into what would be lost and what would come back the way
// internal/apps does.
func addToSizes(s *apps.Sizes, b classify.Bucket, category string, n int64) {
	switch b {
	case classify.BucketApps:
		s.Bundle += n
	case classify.BucketAppData:
		if strings.Contains(strings.ToLower(category), "cache") {
			s.Caches += n
		} else {
			s.Data += n
		}
	case classify.BucketDeveloper:
		s.Dev += n
	case classify.BucketContainers:
		s.Containers += n
	default:
		s.Data += n
	}
	s.Total += n
}

// sortComponents orders a footprint's directories by bucket and then by size,
// so the buckets a footprint crosses read as groups.
func sortComponents(cs []apps.ComponentRef) {
	sort.SliceStable(cs, func(i, j int) bool {
		bi, bj := bucketByID(cs[i].Bucket), bucketByID(cs[j].Bucket)
		if bi != bj {
			return bi < bj
		}
		return cs[i].Bytes > cs[j].Bytes
	})
}

// bucketByID is the bucket with this id, or Other.
func bucketByID(id string) classify.Bucket {
	for _, b := range classify.Buckets() {
		if b.ID() == id {
			return b
		}
	}
	return classify.BucketOther
}

// matchEntry finds the inventory entry an argument names: its owner key, the
// key without its prefix, its label, or one of its bundle ids.
func matchEntry(rep *apps.Report, arg string) (apps.Entry, bool) {
	for _, e := range allEntries(rep) {
		if entryMatches(e, arg) {
			return e, true
		}
	}
	return apps.Entry{}, false
}

// entryMatches reports whether an argument names this entry.
func entryMatches(e apps.Entry, arg string) bool {
	if strings.EqualFold(e.Owner, arg) || strings.EqualFold(e.Label, arg) {
		return true
	}
	if _, rest, ok := strings.Cut(e.Owner, ":"); ok && strings.EqualFold(rest, arg) {
		return true
	}
	for _, b := range e.Bundles {
		if b.ID != "" && strings.EqualFold(b.ID, arg) {
			return true
		}
	}
	return false
}

// appsEntryFor finds the inventory entry behind a claim's owner. It joins on
// the owner key alone, because that is what the key is for: two owners can
// share a label, and none can share a prefixed key.
func appsEntryFor(rep *apps.Report, keys []string) (apps.Entry, bool) {
	for _, e := range allEntries(rep) {
		for _, k := range keys {
			if strings.EqualFold(e.Owner, k) {
				return e, true
			}
		}
	}
	return apps.Entry{}, false
}

// sourceText renders a claim's source the way the block prints it. It is
// Source.String() with one repetition removed: the application inventory's
// own claim ids already begin with "apps/", so the kind prefix would make it
// "apps:apps/cask-zap".
func sourceText(s classify.Source) string { return collapseAppsPrefix(s.String()) }

// collapseAppsPrefix is sourceText for a source that has already been
// rendered, which is how the inventory's own components carry theirs.
func collapseAppsPrefix(s string) string {
	if rest, ok := strings.CutPrefix(s, "apps:apps/"); ok {
		return "apps/" + rest
	}
	return s
}

// allEntries is every owner the inventory reported, in one list.
func allEntries(rep *apps.Report) []apps.Entry {
	if rep == nil {
		return nil
	}
	out := make([]apps.Entry, 0, len(rep.Apps)+len(rep.CaskOnly)+len(rep.Orphans))
	for _, list := range [][]apps.Entry{
		rep.Apps, rep.CaskOnly, rep.Orphans, rep.InTrash,
		rep.NonApp, rep.OwnBuild, rep.Vendor, rep.Unknown,
	} {
		out = append(out, list...)
	}
	return out
}

// ownerNotFound is the error for an argument that named no owner, with the
// owners whose key or label starts the same way.
func ownerNotFound(res *scan.Result, arg string) error {
	names := ownerSuggestions(res, arg)
	if len(names) == 0 {
		return fmt.Errorf("no owner is called %q in the stored scan (%s); "+
			"pass a path, a bundle id, or an owner key such as app:com.apple.Safari or cask:cursor",
			arg, explainProvenance(res))
	}
	return fmt.Errorf("no owner is called %q in the stored scan (%s); did you mean %s?",
		arg, explainProvenance(res), strings.Join(names, ", "))
}

// ownerSuggestions are up to five owner labels and keys that begin with the
// argument, from the inventory and from the classification alike.
func ownerSuggestions(res *scan.Result, arg string) []string {
	prefix := strings.ToLower(arg)
	if _, rest, ok := strings.Cut(prefix, ":"); ok {
		prefix = rest
	}
	seen := make(map[string]bool)
	var out []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		cmp := strings.ToLower(name)
		if _, rest, ok := strings.Cut(cmp, ":"); ok {
			cmp = rest
		}
		if !strings.HasPrefix(cmp, prefix) {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	if rep, ok := scan.Apps(res); ok {
		for _, e := range allEntries(rep) {
			add(e.Owner)
			add(e.Label)
		}
	}
	if res.Class != nil {
		for label, total := range res.Class.Owners {
			add(label)
			for _, k := range total.Keys {
				add(k)
			}
		}
	}
	sort.Strings(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// explainProvenance says where the answer came from: the stored scan and its
// age, or this run's own walk.
func explainProvenance(res *scan.Result) string {
	if res == nil || !res.FromCache {
		return "walked just now"
	}
	return "from cache, " + explainAge(res.CacheAge) + " old"
}

// explainAge formats a cache age in the coarsest unit that still says
// something: a scan from last week does not need its minutes.
func explainAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// explainLabel is the width of the label column of the text block.
const explainLabel = 10

// write prints the text form of an explanation.
func (d explainDoc) write(w io.Writer, u units.Format) error {
	if d.Path != nil {
		d.Path.write(w, u)
	}
	if d.Owner != nil {
		d.Owner.write(w, u)
	}
	row(w, "scan", explainProvenanceOf(d.Scan))
	return nil
}

// explainProvenanceOf is explainProvenance for the document's own record of
// where the scan came from, which is what --json carries.
func explainProvenanceOf(s explainScanDoc) string {
	if !s.FromCache {
		return "walked just now"
	}
	return "from cache, " + explainAge(time.Duration(s.AgeNS)) + " old"
}

// write prints the block about one directory.
func (d *explainPathDoc) write(w io.Writer, u units.Format) {
	head := fmt.Sprintf("%s, %s files", u.Bytes(d.Bytes), fmtCount(uint64(d.Files)))
	if d.Modified != "" {
		head += ", modified " + d.Modified
	}
	_, _ = fmt.Fprintf(w, "%s   %s\n", d.Path, head)

	bucket := fmt.Sprintf("%d %s", d.Bucket, d.BucketLabel)
	if d.Category != "" {
		bucket += " › " + d.Category
	}
	row(w, "bucket", bucket)

	if d.Owner != "" || len(d.OwnerKeys) > 0 {
		owner := d.Owner
		if len(d.OwnerKeys) > 0 {
			owner += " (" + strings.Join(d.OwnerKeys, ", ") + ")"
		}
		if d.State != "" && !strings.Contains(strings.ToLower(d.Owner), d.State) {
			owner += " — " + d.State
		}
		row(w, "owner", strings.TrimSpace(owner))
	}
	if d.Reclaim != "" {
		row(w, "reclaim", d.Reclaim)
	}
	if d.Source != "" {
		by := d.Source
		if d.Inherited {
			by += " (inherited from " + d.InheritedFrom + ")"
		}
		row(w, "by", by)
	}
	if d.Source == "" {
		row(w, "by", "nothing claimed this directory; it is counted as Other")
	}
	row(w, "evidence", d.Evidence...)

	if len(d.AlsoClaimedBy) > 0 {
		lines := make([]string, 0, len(d.AlsoClaimedBy))
		for _, c := range d.AlsoClaimedBy {
			lines = append(lines, fmt.Sprintf("%s — lost to %s", c.Loser, c.Winner))
		}
		rowWidth(w, "also claimed by", len("also claimed by"), lines...)
	}
	if d.Footprint != nil {
		row(w, "footprint", fmt.Sprintf("%s: %s", d.Footprint.Label, sizesLine(d.Footprint.Footprint, u)))
	}
	if len(d.Subtree) > 0 {
		lines := make([]string, 0, len(d.Subtree))
		for _, b := range d.Subtree {
			lines = append(lines, fmt.Sprintf("%9s  %d %s", u.Bytes(b.Bytes), b.Bucket, b.Label))
		}
		row(w, "below it", lines...)
	}
}

// write prints the block about one owner.
func (d *explainOwnerDoc) write(w io.Writer, u units.Format) {
	head := d.Label
	if d.Key != "" {
		head += " (" + d.Key + ")"
	}
	if d.State != "" {
		head += " — " + d.State
	}
	if d.Confidence != "" {
		head += ", " + d.Confidence
	}
	_, _ = fmt.Fprintln(w, head)
	row(w, "footprint", sizesLine(d.Footprint, u))
	row(w, "evidence", d.Evidence...)

	if len(d.Components) == 0 {
		row(w, "components", "none: nothing on this volume is attributed to this owner")
		return
	}
	_, _ = fmt.Fprintln(w, "components")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", "SIZE", "BUCKET", "PATH", "BY")
	for _, c := range d.Components {
		b := bucketByID(c.Bucket)
		_, _ = fmt.Fprintf(tw, "  %s\t%d %s\t%s\t%s\n", u.Bytes(c.Bytes), int(b), b.Label(), c.Path, c.Source)
	}
	_ = tw.Flush()
}

// sizesLine is a footprint on one line: the total and the buckets it crosses.
func sizesLine(s apps.Sizes, u units.Format) string {
	parts := make([]string, 0, 5)
	for _, p := range []struct {
		n     int64
		label string
	}{
		{s.Bundle, "Applications"},
		{s.Data, "App data"},
		{s.Caches, "caches"},
		{s.Dev, "Developer"},
		{s.Containers, "Containers"},
	} {
		if p.n > 0 {
			parts = append(parts, u.Bytes(p.n)+" "+p.label)
		}
	}
	if len(parts) == 0 {
		return u.Bytes(s.Total) + " total"
	}
	return fmt.Sprintf("%s total — %s", u.Bytes(s.Total), strings.Join(parts, ", "))
}

// row prints one labelled row of the block, continuing under the label for
// the lines after the first. Nothing is printed for a row with no lines.
func row(w io.Writer, label string, lines ...string) {
	rowWidth(w, label, explainLabel, lines...)
}

// rowWidth is row with its own label column, for the one label too long for
// the common one.
func rowWidth(w io.Writer, label string, width int, lines ...string) {
	for i, line := range lines {
		name := ""
		if i == 0 {
			name = label
		}
		_, _ = fmt.Fprintf(w, "%-*s %s\n", width, name, line)
	}
}
