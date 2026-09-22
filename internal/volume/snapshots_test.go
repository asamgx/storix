package volume

import (
	"context"
	"reflect"
	"testing"
)

func TestParseSnapshotNames(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []string
	}{
		{
			name: "none",
			out:  "Snapshots for disk /System/Volumes/Data:\n",
		},
		{
			name: "two",
			out: "Snapshots for disk /System/Volumes/Data:\n" +
				"com.apple.TimeMachine.2026-09-21-031500.local\n" +
				"com.apple.TimeMachine.2026-09-22-081500.local\n",
			want: []string{
				"com.apple.TimeMachine.2026-09-21-031500.local",
				"com.apple.TimeMachine.2026-09-22-081500.local",
			},
		},
		{
			name: "indented and trailing noise",
			out:  "Snapshots for disk /:\n\tcom.apple.TimeMachine.2026-01-02-030405.local  \n\n",
			want: []string{"com.apple.TimeMachine.2026-01-02-030405.local"},
		},
		{
			name: "header only mentioning a date is not a snapshot",
			out:  "Snapshots for disk 2026-09-22:\n",
		},
		{
			name: "malformed timestamps ignored",
			out:  "com.apple.TimeMachine.2026-9-22-0815.local\ncom.apple.TimeMachine.local\n",
		},
		{name: "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseSnapshotNames(c.out)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseSnapshotNames() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestListLocalSnapshotsUnknownVolume(t *testing.T) {
	s := ListLocalSnapshots(context.Background(), "/no/such/volume")
	if s.Known {
		t.Errorf("Known = true for a missing volume: %+v", s)
	}
	if s.Err == "" {
		t.Error("no reason recorded for the failure")
	}
}

func TestListLocalSnapshotsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := ListLocalSnapshots(ctx, "/")
	if s.Known {
		t.Error("Known = true despite a cancelled context")
	}
}
