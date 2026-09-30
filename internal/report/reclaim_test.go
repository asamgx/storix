package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/reclaim"
	"github.com/asamgx/storix/internal/units"
)

// planFixture is the report fixture with the plan built from it, dated at
// the fixture's own scan so the stale threshold reads the same every run.
func planFixture() (*reclaim.Plan, Options) {
	r := fakeScan()
	return reclaim.Build(r, reclaim.Options{Now: r.Tree.Finished}), Options{Units: units.Decimal}
}

func TestReclaimGolden(t *testing.T) {
	r := fakeScan()
	p, o := planFixture()
	var buf bytes.Buffer
	if err := Reclaim(&buf, r, p, o, PlanView{All: true}); err != nil {
		t.Fatal(err)
	}
	golden(t, "reclaim.txt", buf.String())
}

func TestReclaimJSONGolden(t *testing.T) {
	p, _ := planFixture()
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "reclaim.json", string(b)+"\n")
}

// TestReclaimTierFilter: --tier shows only the named tiers, and the headline
// still describes the whole plan.
func TestReclaimTierFilter(t *testing.T) {
	r := fakeScan()
	p, o := planFixture()
	var buf bytes.Buffer
	if err := Reclaim(&buf, r, p, o, PlanView{Tiers: []reclaim.Tier{reclaim.Reinstall}}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "Reinstall —") || strings.Contains(got, "Re-download —") {
		t.Errorf("--tier reinstall rendered other tiers:\n%s", got)
	}
	if !strings.Contains(got, "could be freed across the suggested tiers") {
		t.Errorf("the headline is missing:\n%s", got)
	}
}
