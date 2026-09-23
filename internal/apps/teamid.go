package apps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/probe"
)

// TeamResolver maps application bundles to the Apple Developer team that
// signed them, which is the only way to read a group container named
// "HUAQ24HBR6.dev.orbstack".
//
// Signatures are read with codesign, which costs about 26 ms a bundle. Fifty
// bundles is over a second of a scan that should take a few, so the answers
// are cached on disk keyed by identifier and version: the first scan pays for
// the machine, every scan after it pays for whatever was updated. The cache
// file is the only thing this package writes, which is why the no-open lint
// test allows this file and no other.
type TeamResolver struct {
	// Runner runs codesign. A nil Runner resolves nothing, which is the
	// degraded path: an unresolved team id is reported as unresolved.
	Runner probe.Runner
	// Path is the cache file. Empty disables loading and saving.
	Path string
	// Parallel bounds concurrent codesign invocations; zero means four.
	Parallel int

	mu    sync.Mutex
	cache map[string]string
	// dirty is set when a lookup added an entry, so an unchanged machine
	// does not rewrite the file.
	dirty bool
	// calls counts codesign invocations, which the acceptance test reads
	// to prove the second scan ran none.
	calls int
}

// teamCacheDoc is the on-disk form: a version and the map, so a future change
// of key shape can be detected rather than misread.
type teamCacheDoc struct {
	Version int               `json:"version"`
	Teams   map[string]string `json:"teams"`
}

// teamCacheVersion is the current cache format.
const teamCacheVersion = 1

// DefaultTeamCachePath is where the team id cache lives. Under sudo it is the
// invoking user's Application Support rather than root's, so the file stays
// readable by the person who ran the scan.
func DefaultTeamCachePath() string {
	_, home, err := mac.InvokingHome()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "storix", "teamids.json")
}

// NewTeamResolver builds a resolver over a runner and a cache file.
func NewTeamResolver(runner probe.Runner, path string) *TeamResolver {
	return &TeamResolver{Runner: runner, Path: path, cache: make(map[string]string)}
}

// Load reads the cache file. A missing or unreadable file is not an error:
// the cache is an optimisation, and starting cold costs a second once.
func (r *TeamResolver) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]string)
	}
	if r.Path == "" {
		return nil
	}
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return err
	}
	var doc teamCacheDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	if doc.Version != teamCacheVersion {
		return nil
	}
	for k, v := range doc.Teams {
		r.cache[k] = v
	}
	return nil
}

// Save writes the cache file when a lookup added to it. Under sudo the file
// and its directory are chowned to the invoking user, because a cache owned
// by root is a cache the next unprivileged scan cannot write.
func (r *TeamResolver) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Path == "" || !r.dirty {
		return nil
	}
	doc := teamCacheDoc{Version: teamCacheVersion, Teams: r.cache}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := r.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.Path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	chownToInvoker(dir)
	chownToInvoker(r.Path)
	r.dirty = false
	return nil
}

// chownToInvoker hands a path back to the user who ran sudo. A failure is
// ignored: the scan has already succeeded, and a cache file with the wrong
// owner costs one re-run of codesign rather than a wrong answer.
func chownToInvoker(path string) {
	uid, gid, viaSudo := mac.InvokingUser()
	if !viaSudo {
		return
	}
	_ = os.Chown(path, uid, gid)
}

// Resolve fills in the team id of every bundle and returns the bundles
// grouped by team.
//
// Only bundles missing from the cache cost a codesign run, and the runs are
// bounded by Parallel. A bundle whose signature cannot be read gets an empty
// team id, cached as empty so an unsigned bundle is asked about once.
func (r *TeamResolver) Resolve(ctx context.Context, bundles []*Bundle) map[string][]*Bundle {
	todo := r.pending(bundles)
	if len(todo) > 0 && r.Runner != nil {
		r.run(ctx, todo)
	}

	out := make(map[string][]*Bundle)
	r.mu.Lock()
	for _, b := range bundles {
		if team, ok := r.cache[teamCacheKey(b.BundleInfo)]; ok && team != "" {
			b.TeamID = team
		}
		if b.TeamID != "" {
			out[b.TeamID] = append(out[b.TeamID], b)
		}
	}
	r.mu.Unlock()
	return out
}

// pending lists the bundles whose team id is not cached yet.
func (r *TeamResolver) pending(bundles []*Bundle) []*Bundle {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]string)
	}
	var todo []*Bundle
	seen := make(map[string]bool, len(bundles))
	for _, b := range bundles {
		key := teamCacheKey(b.BundleInfo)
		if _, ok := r.cache[key]; ok || seen[key] {
			continue
		}
		seen[key] = true
		todo = append(todo, b)
	}
	return todo
}

// run invokes codesign over the pending bundles.
func (r *TeamResolver) run(ctx context.Context, todo []*Bundle) {
	limit := r.Parallel
	if limit <= 0 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, b := range todo {
		wg.Add(1)
		go func(b *Bundle) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := r.Runner.Run(ctx, probe.Cmd{
				Name: "codesign",
				Args: []string{"-dv", "--verbose=4", b.Path},
			})
			// codesign writes its report to stderr and exits non-zero
			// for an unsigned bundle, so both streams are read and a
			// non-zero exit is not by itself a failure.
			team := ParseCodesignTeamID(res.Stderr + "\n" + res.Stdout)

			r.mu.Lock()
			r.calls++
			r.cache[teamCacheKey(b.BundleInfo)] = team
			r.dirty = true
			r.mu.Unlock()
		}(b)
	}
	wg.Wait()
}

// Calls is how many times codesign was invoked, which the acceptance test
// reads to prove a second scan invoked it zero times.
func (r *TeamResolver) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// Cache is a copy of the resolved team ids, for storing in Facts.
func (r *TeamResolver) Cache() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.cache))
	for k, v := range r.cache {
		out[k] = v
	}
	return out
}

// Seed adds known answers, which is how cached Facts skip the probe entirely.
func (r *TeamResolver) Seed(teams map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]string)
	}
	for k, v := range teams {
		if _, ok := r.cache[k]; !ok {
			r.cache[k] = v
		}
	}
}

// ParseCodesignTeamID reads the TeamIdentifier line of "codesign -dv
// --verbose=4". Apple's own binaries print "not set", which is an answer and
// is returned as the empty string.
func ParseCodesignTeamID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier=")
		if !ok {
			continue
		}
		team := strings.TrimSpace(rest)
		if team == "" || team == "not set" {
			return ""
		}
		return team
	}
	return ""
}

// NeedsTeamIDs reports whether any group container is namespaced by a team id
// that is worth resolving.
//
// This is the gate that keeps codesign from running at all on a machine with
// no such container (D26). Apple's own team-id containers are excluded, so a
// stock install with nothing but "243LU875E5.groups.com.apple.podcasts" never
// pays for a signature read.
func NeedsTeamIDs(groupContainerNames []string) bool {
	for _, name := range groupContainerNames {
		if _, _, ok := TeamIDPrefix(name); ok {
			return true
		}
	}
	return false
}
