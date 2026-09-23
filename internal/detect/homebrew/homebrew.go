// Package homebrew detects Homebrew, which on this kind of machine owns the
// largest single directory outside the user's own data.
//
// Homebrew is asked rather than guessed at. The prefix moved from
// /usr/local to /opt/homebrew with Apple silicon, the cache moved out of the
// prefix into ~/Library/Caches, and a user may have both installations side
// by side; four one-word questions settle all of it in less time than a
// directory listing takes.
//
// The number worth having is the one no path pattern can produce. A keg is
// superseded only relative to what else is installed, a bottle in the cache is
// stale only relative to what the formula now wants, and `brew cleanup -n`
// knows both. Its summary line is what this detector reports as reclaimable;
// the per-line sizes are kept as a cross-check and as evidence, because a
// total that disagreed with the lines under it would be worth noticing.
package homebrew

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "homebrew"

// Owner is the display label every Homebrew-wide claim carries.
const Owner = "Homebrew"

// brewTimeout is what a brew invocation is allowed. Every command here is a
// local query, but `brew list` and `brew cleanup` walk the Cellar and the
// cache, so they get three times the ordinary budget rather than five
// seconds that a cold page cache could eat.
const brewTimeout = 15 * time.Second

// ownerKeys are the identifiers the application footprint joins Homebrew's
// own bytes on. A formula's own keg carries its own key instead.
var ownerKeys = []string{"cli:brew"}

func init() { detect.Register(100, New()) }

// Detector finds Homebrew.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Formula is one installed formula and every version of it that is on disk.
// More than one version means the older ones are superseded kegs that
// `brew cleanup` would remove.
type Formula struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions,omitempty"`
}

// Removal is one line of `brew cleanup -n`: something brew would delete and
// what it costs.
type Removal struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	// Files is how many files the entry holds, when brew counted them. The
	// two printed shapes are "(214.1MB)" and "(1,705 files, 34.6MB)".
	Files int `json:"files,omitempty"`
}

// Cleanup is what `brew cleanup -n` reported.
type Cleanup struct {
	// Free is the figure from the "would free approximately" summary line,
	// which is brew's own total and the one storix reports.
	Free int64 `json:"free,omitempty"`
	// Known is false when brew printed no summary line, in which case Free
	// is the sum of the removals instead and says so.
	Known bool `json:"known,omitempty"`
	// Removals are the individual entries, kept as the cross-check.
	Removals []Removal `json:"removals,omitempty"`
	// Skipped counts the "Warning: Skipping …" lines, which are formulae
	// whose newest version is not installed and so have nothing to clean.
	// brew prints them on stderr, which the probe caps, so on a machine
	// with hundreds of formulae this is a lower bound.
	Skipped int `json:"skipped,omitempty"`
}

// LineSum is the total of the individual removals, which is what the summary
// line is checked against.
func (c Cleanup) LineSum() int64 {
	var n int64
	for _, r := range c.Removals {
		n += r.Bytes
	}
	return n
}

// Facts are what the probe learned.
type Facts struct {
	// Home is the home the probe ran against, so that the absolute paths
	// brew reported can be moved into the home Classify works with.
	Home     string    `json:"home,omitempty"`
	Prefix   string    `json:"prefix,omitempty"`
	Cellar   string    `json:"cellar,omitempty"`
	Caskroom string    `json:"caskroom,omitempty"`
	Cache    string    `json:"cache,omitempty"`
	Formulae []Formula `json:"formulae,omitempty"`
	Cleanup  Cleanup   `json:"cleanup,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// MultiVersion are the formulae with more than one version on disk, sorted by
// name. They are what `brew cleanup` has to remove and what the report flags.
func (f *Facts) MultiVersion() []Formula {
	if f == nil {
		return nil
	}
	var out []Formula
	for _, fm := range f.Formulae {
		if len(fm.Versions) > 1 {
			out = append(out, fm)
		}
	}
	return out
}

// Probe asks brew where it keeps things and what it would throw away.
//
// The four path questions are four invocations because they have to be:
// `brew --prefix --cellar` prints the prefix and ignores the rest, so a
// combined call would silently answer the wrong question. Each one is
// instant.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	if !env.Has("brew") {
		return nil, detect.Missingf("`brew` is not on the probe path")
	}

	f := &Facts{Home: env.Home}
	var degraded []string

	for _, q := range []struct {
		flag string
		dst  *string
	}{
		{"--prefix", &f.Prefix},
		{"--cellar", &f.Cellar},
		{"--caskroom", &f.Caskroom},
		{"--cache", &f.Cache},
	} {
		res := run(ctx, env.Runner, q.flag)
		if !res.OK() {
			degraded = append(degraded, "brew "+q.flag+": "+res.Reason())
			continue
		}
		*q.dst = strings.TrimSpace(res.Stdout)
	}

	if res := run(ctx, env.Runner, "list", "--formula", "--versions"); res.OK() {
		f.Formulae = parseFormulae(res.Stdout)
	} else {
		degraded = append(degraded, "brew list --formula --versions: "+res.Reason())
	}

	if res := run(ctx, env.Runner, "cleanup", "-n"); res.OK() {
		// brew prints its removals on stdout and its warnings on stderr,
		// and the summary line is on stdout. Both are parsed because the
		// skipped count is part of the picture: 179 formulae with nothing
		// to clean is why a 247-formula Cellar frees so little.
		f.Cleanup = parseCleanup(res.Stdout + "\n" + res.Stderr)
	} else {
		degraded = append(degraded, "brew cleanup -n: "+res.Reason())
	}

	if f.Prefix == "" && len(f.Formulae) == 0 {
		return f, detect.Degradedf("brew answered nothing usable: %s", strings.Join(degraded, "; "))
	}
	if len(degraded) > 0 {
		return f, detect.Degradedf("%s", strings.Join(degraded, "; "))
	}
	return f, nil
}

// run issues one brew command with the package's timeout.
func run(ctx context.Context, r probe.Runner, args ...string) probe.Result {
	return r.Run(ctx, probe.Cmd{Name: "brew", Args: args, Timeout: brewTimeout})
}

// parseFormulae reads `brew list --formula --versions`, one formula per line
// with its versions after it: "sqlite 3.50.4 3.51.1".
func parseFormulae(out string) []Formula {
	var list []Formula
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		list = append(list, Formula{Name: fields[0], Versions: fields[1:]})
	}
	return list
}

// summaryMark is the start of the line `brew cleanup` ends with.
const summaryMark = "This operation would free approximately "

// parseCleanup reads `brew cleanup -n`.
//
// The output is three kinds of line and the interesting one is the last:
//
//	Warning: Skipping aom: most recent version 3.15.0 not installed
//	Would remove: /opt/homebrew/…/portable-ruby/4.0.3 (1,704 files, 34.6MB)
//	Would remove: /Users/…/Caches/Homebrew/foo--1.2.tar.gz (214.1MB)
//	==> This operation would free approximately 148.7MB of disk space.
//
// The summary is brew's own total and is what storix reports. Summing the
// removal lines instead would drift: brew counts a directory it is about to
// remove once, while the lines it prints for it can nest.
func parseCleanup(out string) Cleanup {
	var c Cleanup
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Warning:") && strings.Contains(line, "Skipping"):
			c.Skipped++
		case strings.HasPrefix(line, "Would remove:"):
			if r, ok := parseRemoval(strings.TrimSpace(strings.TrimPrefix(line, "Would remove:"))); ok {
				c.Removals = append(c.Removals, r)
			}
		default:
			if i := strings.Index(line, summaryMark); i >= 0 {
				rest := line[i+len(summaryMark):]
				rest = strings.TrimSuffix(strings.TrimSpace(rest), ".")
				rest = strings.TrimSuffix(rest, " of disk space")
				if n, ok := probe.ParseHumanBytes(rest); ok {
					c.Free, c.Known = n, true
				}
			}
		}
	}
	if !c.Known {
		c.Free = c.LineSum()
	}
	return c
}

// parseRemoval reads one "Would remove" entry: a path and a parenthesised
// size that may or may not be preceded by a file count.
func parseRemoval(rest string) (Removal, bool) {
	open := strings.LastIndexByte(rest, '(')
	if open < 0 || !strings.HasSuffix(rest, ")") {
		return Removal{}, false
	}
	inner := rest[open+1 : len(rest)-1]
	r := Removal{Path: strings.TrimSpace(rest[:open])}
	if i := strings.LastIndexByte(inner, ','); i >= 0 {
		r.Files = parseFileCount(inner[:i])
		inner = inner[i+1:]
	}
	size, ok := probe.ParseHumanBytes(strings.TrimSpace(inner))
	if !ok || r.Path == "" {
		return Removal{}, false
	}
	r.Bytes = size
	return r, true
}

// parseFileCount reads the "1,705 files" half of a removal line.
func parseFileCount(s string) int {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "files"))
	n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	if err != nil {
		return 0
	}
	return n
}

// defaults are the Homebrew locations to fall back on when the probe could
// not run. They are the same paths catalog's dev.homebrew.* rules name, so a
// degraded detector buckets the same bytes the same way and loses only the
// evidence.
type defaults struct{ prefix, cellar, cache string }

// resolve fills in whatever the probe did not learn, moving the paths brew
// reported into the home the tree is displayed under.
func resolve(t *walk.Tree, f *Facts, home string) defaults {
	d := defaults{prefix: "/opt/homebrew", cache: path.Join(home, "Library/Caches/Homebrew")}
	if f == nil {
		d.cellar = path.Join(d.prefix, "Cellar")
		return d
	}
	at := func(measured string) string { return detect.Rebase(measured, f.Home, home) }
	d.prefix = detect.Prefer(t, at(f.Prefix), d.prefix)
	d.cellar = detect.Prefer(t, at(f.Cellar), path.Join(d.prefix, "Cellar"))
	d.cache = detect.Prefer(t, at(f.Cache), d.cache)
	return d
}

// Classify turns the facts and the tree into claims and the tool rows the
// Developer view shows.
//
// The Caskroom is deliberately absent: a cask is an application, and the
// application inventory owns those bytes so that a cask and its bundle are
// one footprint rather than two.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}
	d := resolve(t, facts, home)
	ev := evidence(facts, d)

	targets := []detect.Target{
		{
			Path: d.prefix, Category: "Homebrew", Owner: Owner, Reclaim: classify.ToolManaged,
			Explain: "the Homebrew prefix; everything under it is brew's to remove",
		},
		{
			Path: d.cellar, Category: "Homebrew formulae", Owner: Owner, Reclaim: classify.ToolManaged,
			Kind: "toolchain", Name: "Cellar", Note: cellarNote(facts),
			Explain: "installed formulae; `brew cleanup` removes superseded versions",
		},
		{
			Path: d.cache, Category: "Package cache", Owner: Owner, Reclaim: classify.Regenerable,
			Kind: "cache", Name: "download cache",
			Explain: "bottles and casks brew downloaded; `brew cleanup` clears them",
		},
		{
			Path: path.Join(home, "Library/Logs/Homebrew"), Category: "Homebrew metadata", Owner: Owner,
			Reclaim: classify.Regenerable, Kind: "cache", Name: "build logs",
			Explain: "logs from formulae brew built from source",
		},
		{
			Path: path.Join(d.prefix, "Library/Taps"), Category: "Homebrew metadata", Owner: Owner,
			Reclaim: classify.Regenerable, Kind: "data", Name: "taps",
			Explain: "tap clones; brew refetches them",
		},
		{
			Path: path.Join(d.prefix, ".git"), Category: "Homebrew metadata", Owner: Owner,
			Reclaim: classify.ToolManaged, Kind: "data", Name: "brew repository",
			Explain: "Homebrew's own git history",
		},
	}
	for i := range targets {
		targets[i].Bucket = classify.BucketDeveloper
		targets[i].OwnerKeys = ownerKeys
		targets[i].Evidence = ev
	}

	claims, tools := detect.Claims(t, Name, targets)
	kegClaims, kegTools := kegs(t, facts, d, ev)
	claims = append(claims, kegClaims...)
	tools = append(tools, kegTools...)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}

	sum := detect.Summary{Tools: tools}
	if facts != nil && facts.Cleanup.Free > 0 {
		sum.Reclaimable = facts.Cleanup.Free
		sum.ReclaimNote = cleanupNote(facts.Cleanup)
	}
	return claims, sum
}

// kegs claims every formula's directory, and lists a row for each formula
// that has more than one version installed.
//
// A per-formula claim is what makes "whose bytes are these" answerable inside
// the Cellar: without it the owner totals would say Homebrew owns 3.8 GB and
// nothing would say that llvm is most of it. The rows are only the multi-
// version formulae, because a table of 247 identical lines is not a report.
func kegs(t *walk.Tree, f *Facts, d defaults, ev []string) ([]classify.Claim, []detect.Tool) {
	formulae := f.MultiVersion()
	multi := make(map[string]Formula, len(formulae))
	for _, fm := range formulae {
		multi[fm.Name] = fm
	}

	var targets []detect.Target
	for _, name := range formulaNames(t, f, d) {
		tg := detect.Target{
			Path:     path.Join(d.cellar, name),
			Bucket:   classify.BucketDeveloper,
			Category: "Homebrew formulae",
			Owner:    name,
			// A formula is joined on by the command it installs, which
			// is the name in every case brew itself reports.
			OwnerKeys: []string{"cli:" + name},
			Reclaim:   classify.ToolManaged,
			Explain:   "the formula " + name + "; `brew uninstall " + name + "` removes it",
			Evidence:  ev,
		}
		if fm, ok := multi[name]; ok {
			tg.Kind = "versions"
			tg.Name = name
			tg.Version = strings.Join(fm.Versions, ", ")
			tg.Note = fmt.Sprintf("%d versions installed; `brew cleanup %s` removes the superseded one",
				len(fm.Versions), name)
		}
		targets = append(targets, tg)
	}
	claims, tools := detect.Claims(t, Name, targets)

	// The version directories under a multi-version formula are worth a row
	// each: the point of flagging the formula is that one of them is dead
	// weight, and a reader wants to see which and how much.
	var versionTargets []detect.Target
	for _, fm := range formulae {
		for i, v := range sortedVersions(fm.Versions) {
			current := i == len(fm.Versions)-1
			note := "superseded keg; `brew cleanup` removes it"
			reclaim := classify.Regenerable
			if current {
				note = "the version in use"
				reclaim = classify.ToolManaged
			}
			versionTargets = append(versionTargets, detect.Target{
				Path: path.Join(d.cellar, fm.Name, v), Bucket: classify.BucketDeveloper,
				Category: "Homebrew formulae", Owner: fm.Name, OwnerKeys: []string{"cli:" + fm.Name},
				Reclaim: reclaim, Kind: "versions", Name: fm.Name, Version: v, Current: current,
				Note: note, Evidence: ev,
				Explain: "version " + v + " of the formula " + fm.Name,
			})
		}
	}
	vClaims, vTools := detect.Claims(t, Name, versionTargets)
	return append(claims, vClaims...), append(tools, vTools...)
}

// formulaNames is every formula to claim: the ones brew listed, or, when the
// listing failed, the directory names the walk found in the Cellar.
func formulaNames(t *walk.Tree, f *Facts, d defaults) []string {
	if f != nil && len(f.Formulae) > 0 {
		names := make([]string, 0, len(f.Formulae))
		for _, fm := range f.Formulae {
			names = append(names, fm.Name)
		}
		return names
	}
	cellar, ok := detect.Lookup(t, d.cellar)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(cellar.Children))
	for _, c := range cellar.Children {
		names = append(names, c.Name)
	}
	return names
}

// sortedVersions orders a formula's versions so the last one is the one brew
// would keep. Homebrew prints them oldest first in most cases but not all, so
// they are sorted rather than trusted: "3.50.4 3.51.1" and "1.6.53 1.6.50"
// both appear in one listing on this machine.
func sortedVersions(versions []string) []string {
	out := append([]string(nil), versions...)
	sort.SliceStable(out, func(i, j int) bool { return lessVersion(out[i], out[j]) })
	return out
}

// lessVersion compares two version strings the way a person reads them:
// numeric runs numerically, everything else lexically. It is a heuristic, and
// it only ever decides which of several installed kegs carries the "current"
// marker in a table.
func lessVersion(a, b string) bool {
	as, bs := versionFields(a), versionFields(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aok := strconv.Atoi(as[i])
		bn, bok := strconv.Atoi(bs[i])
		switch {
		case aok == nil && bok == nil:
			if an != bn {
				return an < bn
			}
		case as[i] != bs[i]:
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

// versionFields splits a version into its dot- and underscore-separated
// parts.
func versionFields(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
}

// cellarNote says how much is in the Cellar and how much of it is duplicate.
func cellarNote(f *Facts) string {
	if f == nil || len(f.Formulae) == 0 {
		return "brew did not list its formulae; the directory is claimed from the static paths"
	}
	multi := len(f.MultiVersion())
	if multi == 0 {
		return fmt.Sprintf("%d formulae, one version each", len(f.Formulae))
	}
	return fmt.Sprintf("%d formulae, %d of them with more than one version installed", len(f.Formulae), multi)
}

// cleanupNote attributes the reclaimable figure and records the cross-check.
func cleanupNote(c Cleanup) string {
	u := units.Decimal
	if !c.Known {
		return fmt.Sprintf("summed from %d `brew cleanup -n` entries; brew printed no total", len(c.Removals))
	}
	note := fmt.Sprintf("`brew cleanup -n` would free %s", u.Bytes(c.Free))
	if sum := c.LineSum(); sum > 0 && sum != c.Free {
		note += fmt.Sprintf(" (%s across %d entries)", u.Bytes(sum), len(c.Removals))
	}
	return note
}

// evidence are the why-panel lines every Homebrew claim carries.
func evidence(f *Facts, d defaults) []string {
	if f == nil {
		return []string{"homebrew did not answer; the paths come from the static catalog"}
	}
	out := []string{"`brew --prefix` → " + d.prefix, "`brew --cellar` → " + d.cellar}
	if f.Cache != "" {
		out = append(out, "`brew --cache` → "+f.Cache)
	}
	if n := len(f.Formulae); n > 0 {
		out = append(out, fmt.Sprintf("`brew list --formula --versions` → %d formulae, %d with several versions",
			n, len(f.MultiVersion())))
	}
	if c := f.Cleanup; c.Free > 0 {
		out = append(out, cleanupNote(c))
	}
	if f.Cleanup.Skipped > 0 {
		out = append(out, fmt.Sprintf("`brew cleanup -n` skipped %d formulae whose newest version is not installed",
			f.Cleanup.Skipped))
	}
	return out
}
