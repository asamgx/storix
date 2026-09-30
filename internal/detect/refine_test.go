package detect

import (
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/walk"
)

type refineFunc func([]classify.Claim) []classify.Claim

func (f refineFunc) Refine(_ *walk.Tree, _ Facts, _ classify.Context, c []classify.Claim) []classify.Claim {
	return f(c)
}

// TestRefineOneKeepsTheClaimsWhenTheContractBreaks: a Refiner may retag
// claims, and nothing else. One that panics, or that adds or drops a claim,
// has its answer discarded rather than corrupting the classification.
func TestRefineOneKeepsTheClaimsWhenTheContractBreaks(t *testing.T) {
	t.Parallel()
	orig := []classify.Claim{{Reclaim: classify.ToolManaged}, {Reclaim: classify.Unknown}}
	cases := map[string]refineFunc{
		"panics": func(c []classify.Claim) []classify.Claim {
			c[0].Reclaim = classify.Orphaned
			panic("boom")
		},
		"drops": func(c []classify.Claim) []classify.Claim {
			c[0].Reclaim = classify.Orphaned
			return c[:1]
		},
	}
	for name, r := range cases {
		claims := append([]classify.Claim(nil), orig...)
		var st Status
		got := refineOne(r, nil, Outcome{}, classify.Context{}, claims, &st)
		if len(got) != 2 || got[0].Reclaim != classify.ToolManaged {
			t.Errorf("%s: claims = %+v, want them as they were", name, got)
		}
		if name == "panics" && st.State != Panic {
			t.Errorf("a panicking Refine left the status %v", st.State)
		}
	}
	ok := refineFunc(func(c []classify.Claim) []classify.Claim {
		c[1].Reclaim = classify.Orphaned
		return c
	})
	var st Status
	got := refineOne(ok, nil, Outcome{}, classify.Context{}, append([]classify.Claim(nil), orig...), &st)
	if got[1].Reclaim != classify.Orphaned {
		t.Errorf("a well-behaved Refine was discarded: %+v", got)
	}
}
