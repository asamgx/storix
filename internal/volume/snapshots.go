package volume

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// snapshotTimeout bounds the tmutil call.
const snapshotTimeout = 5 * time.Second

// snapshotRe matches a local Time Machine snapshot name as tmutil prints it,
// e.g. com.apple.TimeMachine.2026-09-22-081500.local.
var snapshotRe = regexp.MustCompile(`com\.apple\.TimeMachine\.\d{4}-\d{2}-\d{2}-\d{6}\.local`)

// Snapshots is the list of local Time Machine snapshots on a volume. Their
// blocks count as used space but belong to no file the walk can see, so the
// ledger names them even though it cannot size them.
type Snapshots struct {
	Names []string
	Known bool
	Err   string
}

// ListLocalSnapshots runs tmutil for volumePath. Like ReadPurgeable it never
// returns an error; a failure leaves Known false with the reason in Err.
func ListLocalSnapshots(ctx context.Context, volumePath string) Snapshots {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "tmutil", "listlocalsnapshots", volumePath).Output()
	if err != nil {
		msg := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return Snapshots{Err: msg}
	}
	return Snapshots{Names: parseSnapshotNames(string(out)), Known: true}
}

// parseSnapshotNames extracts snapshot names from tmutil output. The first
// line is a header ("Snapshots for disk /System/Volumes/Data:") and a volume
// with no snapshots prints nothing else.
func parseSnapshotNames(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if m := snapshotRe.FindString(strings.TrimSpace(line)); m != "" {
			names = append(names, m)
		}
	}
	return names
}
