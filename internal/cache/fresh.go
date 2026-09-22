package cache

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MaxAge is how long a cached scan stays usable without an explicit
// --from-cache (D25).
const MaxAge = time.Hour

// Fresh reports whether a cached scan may be shown instead of rescanning.
//
// A cache is fresh when it was written by this storix version, less than
// MaxAge ago, over the same set of roots, and the scan that produced it ran to
// completion. When it is not, the reason says which rule failed, ready to
// print under the header.
func Fresh(meta Meta, now time.Time, version string, roots []string) (bool, string) {
	switch {
	case meta.Schema != SchemaVersion:
		return false, fmt.Sprintf("written with cache schema %d, this build reads %d", meta.Schema, SchemaVersion)
	case meta.Incomplete:
		return false, "the cached scan was interrupted"
	case version != "" && meta.Storix != version:
		return false, fmt.Sprintf("written by storix %s, this is %s", orUnknown(meta.Storix), version)
	case meta.Written.IsZero():
		return false, "the cache has no timestamp"
	}
	age := now.Sub(meta.Written)
	switch {
	case age < 0:
		return false, "the cache is dated in the future"
	case age >= MaxAge:
		return false, fmt.Sprintf("the cache is %s old", Age(age))
	}
	if len(roots) > 0 && !sameRoots(meta.Roots, roots) {
		return false, fmt.Sprintf("the cache covers %s, not %s",
			strings.Join(sortedCopy(meta.Roots), ", "), strings.Join(sortedCopy(roots), ", "))
	}
	return true, ""
}

// sameRoots compares two root sets ignoring order and duplicates.
func sameRoots(a, b []string) bool {
	as, bs := sortedCopy(a), sortedCopy(b)
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func sortedCopy(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown version"
	}
	return s
}

// Age renders a duration the way the cache header and `cache list` show it.
func Age(d time.Duration) string {
	switch {
	case d < 0:
		return "in the future"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
