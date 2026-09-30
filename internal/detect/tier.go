package detect

import (
	"fmt"
	"strings"

	"github.com/asamgx/storix/internal/classify"
)

// Tier is how much it costs to free a directory: what a reader loses, and
// what they have to do to get it back. It is the reclaim plan's grouping, and
// it lives here because the detector is the only part of storix that knows
// what a command actually does (D48).
//
// The zero value is unset. An unset tier is derived from the reclaim tag by
// DefaultTier, and that derivation never answers Safe: "no effect beyond a
// slower first use" is a claim about a specific command, and only the
// detector that names the command can make it.
type Tier uint8

const (
	// TierUnset means the detector said nothing; see DefaultTier.
	TierUnset Tier = iota
	// TierSafe frees bytes whose loss costs nothing but a slower first use.
	TierSafe
	// TierRedownload frees bytes the tool or application fetches again.
	TierRedownload
	// TierReinstall frees build output a project rebuilds or reinstalls.
	TierReinstall
	// TierCheck is for bytes storix cannot vouch for either way.
	TierCheck
	// TierInUse is listed to account for it and never suggested.
	TierInUse
	// TierNever is the user's own data or configuration.
	TierNever
	numTiers
)

// Tiers are the tiers in the order the plan prints them.
var Tiers = []Tier{TierSafe, TierRedownload, TierReinstall, TierCheck, TierInUse, TierNever}

var tierNames = [numTiers]string{"", "safe", "redownload", "reinstall", "check", "in-use", "never"}

var tierLabels = [numTiers]string{"", "Safe", "Re-download", "Reinstall", "Check first", "In use", "Never"}

// String is the tier's stable name, used in JSON and on the command line.
func (t Tier) String() string {
	if t < numTiers {
		return tierNames[t]
	}
	return fmt.Sprintf("tier(%d)", uint8(t))
}

// Label is the tier's heading.
func (t Tier) Label() string {
	if t < numTiers {
		return tierLabels[t]
	}
	return t.String()
}

// Suggested reports whether the plan offers the tier for freeing at all.
func (t Tier) Suggested() bool {
	return t == TierSafe || t == TierRedownload || t == TierReinstall || t == TierCheck
}

// MarshalText writes the tier by name.
func (t Tier) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText reads a tier by name; the empty string is unset.
func (t *Tier) UnmarshalText(b []byte) error {
	got, err := ParseTier(string(b))
	if err != nil {
		return err
	}
	*t = got
	return nil
}

// ParseTier reads a tier by name.
func ParseTier(s string) (Tier, error) {
	for i, n := range tierNames {
		if n == s {
			return Tier(i), nil
		}
	}
	return TierUnset, fmt.Errorf("unknown tier %q (want one of safe, redownload, reinstall, check, in-use, never)", s)
}

// DefaultTier is the tier of a row whose detector named none. It is the
// conservative reading of the reclaim tag and never Safe.
func DefaultTier(r classify.Reclaim, current bool) Tier {
	switch r {
	case classify.Regenerable:
		return TierRedownload
	case classify.ToolManaged:
		if current {
			return TierInUse
		}
		return TierCheck
	case classify.Orphaned, classify.Unknown:
		return TierCheck
	default:
		return TierNever
	}
}

// EffectiveTier is the row's tier: its own when a detector set one, the
// default otherwise.
func (t Tool) EffectiveTier() Tier {
	if t.Tier != TierUnset {
		return t.Tier
	}
	return DefaultTier(t.Reclaim, t.Current)
}

// PlanDockerRow fills in the plan's view of one `docker system df` row: the
// command that frees the row's reclaimable share and what running it costs.
//
// The four rows are four different decisions, which is why the containers
// section may not add them up as one "reclaimable" figure. Build cache is
// rebuilt by the next build. An unused image — one no container, running or
// stopped, was created from — is pulled again when needed; `image prune -a`
// leaves every image a container still uses. A stopped container is state
// someone may come back to. A volume is a database.
func PlanDockerRow(l *Line, context string) {
	docker := "docker"
	if context != "" {
		docker += " --context " + context
	}
	t := strings.ToLower(l.Type)
	switch {
	case strings.Contains(t, "build"):
		l.Tier, l.Command = TierSafe, docker+" builder prune -a"
		l.Impact = "the next image build runs every step again"
	case strings.Contains(t, "image"):
		l.Tier, l.Command = TierRedownload, docker+" image prune -a"
		l.Impact = "removes only images no container uses, stopped ones included; they are pulled again when needed"
	case strings.Contains(t, "container"):
		l.Tier, l.Command = TierCheck, docker+" container prune"
		l.Impact = "removes stopped containers and whatever they wrote outside a volume"
	case strings.Contains(t, "volume"):
		l.Tier, l.Command = TierNever, docker+" volume prune"
		l.Impact = "volumes hold databases and other state; storix never suggests removing them"
	default:
		l.Tier = TierCheck
	}
}
