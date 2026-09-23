// Package catalog is the static rule table that classifies a macOS volume.
//
// The rules are typed Go values rather than a parsed config file, so a rule
// with a broken pattern or a duplicate id fails a test instead of quietly
// leaving a bucket short. They are grouped one file per area, and every rule
// id is <area>.<tool>.<what>, which is what the why panel prints and what the
// rule tests key their positive and negative paths on.
//
// The catalog is the floor, not the ceiling: a detector that probed the
// machine outranks every rule here, so these tables are what the ledger falls
// back to when a tool is absent, a probe failed, or a detector is disabled.
package catalog

import "github.com/asamgx/storix/internal/classify"

// areas are the rule tables in the order they are concatenated. The order has
// no effect on matching, which is decided by specificity alone; it only keeps
// Rules() deterministic.
func areas() [][]classify.Rule {
	return [][]classify.Rule{
		systemRules,
		cacheRules,
		appDataRules,
		personalRules,
		developerRules,
		containerRules,
		backupRules,
		trashRules,
		appRules,
	}
}

// Rules returns the whole catalog. The slice is fresh on every call, so a
// caller that sorts or appends cannot corrupt the tables.
func Rules() []classify.Rule {
	n := 0
	for _, a := range areas() {
		n += len(a)
	}
	out := make([]classify.Rule, 0, n)
	for _, a := range areas() {
		out = append(out, a...)
	}
	return out
}

// Reclaim tags, shortened because the tables are read as tables.
const (
	regen  = classify.Regenerable
	tool   = classify.ToolManaged
	user   = classify.UserData
	system = classify.System
	unsure = classify.Unknown
)

// Buckets, shortened for the same reason.
const (
	apps       = classify.BucketApps
	appData    = classify.BucketAppData
	developer  = classify.BucketDeveloper
	containers = classify.BucketContainers
	personal   = classify.BucketPersonal
	backups    = classify.BucketBackups
	syscaches  = classify.BucketSystemCaches
	trash      = classify.BucketTrash
)

// generic is the priority of a catch-all rule. A catch-all and a named rule
// can reach the same specificity — "~/.{name}" and "~/.Trash" are both three
// segments with literal text in each — so the catch-alls are demoted by one
// rather than every named rule being promoted.
const generic = -1
