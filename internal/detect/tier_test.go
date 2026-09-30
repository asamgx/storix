package detect

import (
	"testing"

	"github.com/asamgx/storix/internal/classify"
)

// TestDefaultTierIsNeverSafe is D48's rule: "no effect beyond a slower
// first use" is a claim about a specific command, so a row whose detector
// named none can never land there, whatever its reclaim tag says.
func TestDefaultTierIsNeverSafe(t *testing.T) {
	t.Parallel()
	for _, r := range []classify.Reclaim{classify.Regenerable, classify.ToolManaged, classify.Orphaned,
		classify.UserData, classify.System, classify.Unknown} {
		for _, current := range []bool{true, false} {
			if got := DefaultTier(r, current); got == TierSafe || got == TierUnset {
				t.Errorf("DefaultTier(%s, current=%v) = %s", r, current, got)
			}
		}
	}
	if got := DefaultTier(classify.ToolManaged, true); got != TierInUse {
		t.Errorf("a current tool-managed row is %s, want in-use", got)
	}
	if got := DefaultTier(classify.UserData, false); got != TierNever {
		t.Errorf("user data is %s, want never", got)
	}
}

func TestTierNamesRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tier := range Tiers {
		got, err := ParseTier(tier.String())
		if err != nil || got != tier {
			t.Errorf("ParseTier(%q) = %v, %v", tier.String(), got, err)
		}
	}
	if _, err := ParseTier("free"); err == nil {
		t.Error("an unknown tier name was accepted")
	}
}

// TestDockerRowsAreFourDecisions: the reclaimable column of `docker system
// df` added up as one figure is how a cleanup deletes a database. Each row
// gets its own command, and volumes are never suggested.
func TestDockerRowsAreFourDecisions(t *testing.T) {
	t.Parallel()
	want := map[string]struct {
		tier    Tier
		command string
	}{
		"Build Cache":   {TierSafe, "docker --context orbstack builder prune -a"},
		"Images":        {TierRedownload, "docker --context orbstack image prune -a"},
		"Containers":    {TierCheck, "docker --context orbstack container prune"},
		"Local Volumes": {TierNever, "docker --context orbstack volume prune"},
	}
	for typ, w := range want {
		l := Line{Type: typ}
		PlanDockerRow(&l, "orbstack")
		if l.Tier != w.tier || l.Command != w.command || l.Impact == "" {
			t.Errorf("%s: tier %s command %q impact %q; want %s %q", typ, l.Tier, l.Command, l.Impact, w.tier, w.command)
		}
		if l.Tier.Suggested() != (w.tier != TierNever) {
			t.Errorf("%s: Suggested() = %v", typ, l.Tier.Suggested())
		}
	}
}
