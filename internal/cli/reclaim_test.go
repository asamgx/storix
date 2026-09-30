package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/asamgx/storix/internal/reclaim"
	"github.com/asamgx/storix/internal/testutil"
)

func TestParseStaleAfter(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30d": 30 * 24 * time.Hour, "7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour,
	} {
		got, err := parseStaleAfter(in)
		if err != nil || got != want {
			t.Errorf("parseStaleAfter(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0d", "-3d", "thirty", "d"} {
		if _, err := parseStaleAfter(bad); err == nil {
			t.Errorf("parseStaleAfter(%q) was accepted", bad)
		}
	}
}

func TestParseTiers(t *testing.T) {
	got, err := parseTiers([]string{"safe", "redownload,check"})
	if err != nil || len(got) != 3 || got[0] != reclaim.Safe || got[2] != reclaim.Check {
		t.Errorf("parseTiers = %v, %v", got, err)
	}
	if _, err := parseTiers([]string{"free"}); err == nil {
		t.Error("an unknown tier was accepted")
	}
}

func TestRunReclaimRejectsABadThreshold(t *testing.T) {
	err := runReclaim(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		&reclaimOptions{staleAfter: "soon"})
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("a bad --stale-after was not a configuration error: %v", err)
	}
}

// TestRunReclaimIsReadOnlyAndSaysSo: the plan prints, the header says
// nothing was changed, and without --plan the command adds that running a
// plan is not available.
func TestRunReclaimIsReadOnlyAndSaysSo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var out, errOut bytes.Buffer
	o := &reclaimOptions{staleAfter: "30d", dev: devOptions{roots: []string{f.Root}, scan: true}}
	if err := runReclaim(context.Background(), &out, &errOut, o); err != nil {
		t.Fatalf("runReclaim: %v (stderr %s)", err, errOut.String())
	}
	if !strings.Contains(out.String(), "nothing has been changed") {
		t.Errorf("the plan does not say it is read-only:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "not available yet") {
		t.Errorf("without --plan the command does not say running is unavailable: %q", errOut.String())
	}
}

func TestRunReclaimJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testutil.New(t)
	f.File("big.bin", 200_000)

	var out, errOut bytes.Buffer
	o := &reclaimOptions{plan: true, staleAfter: "7d", dev: devOptions{roots: []string{f.Root}, scan: true, json: true}}
	if err := runReclaim(context.Background(), &out, &errOut, o); err != nil {
		t.Fatalf("runReclaim: %v (stderr %s)", err, errOut.String())
	}
	var doc struct {
		Schema int `json:"schema"`
		Plan   struct {
			StaleAfter time.Duration              `json:"stale_after_ns"`
			Totals     map[string]json.RawMessage `json:"totals"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if doc.Schema == 0 || doc.Plan.StaleAfter != 7*24*time.Hour {
		t.Errorf("doc = %+v", doc)
	}
	if _, ok := doc.Plan.Totals["safe"]; !ok {
		t.Errorf("totals are not keyed by tier name: %v", doc.Plan.Totals)
	}
}
