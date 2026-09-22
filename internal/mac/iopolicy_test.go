package mac

import (
	"context"
	"errors"
	"testing"
)

func TestDatalessPolicyRoundTrip(t *testing.T) {
	err := SetDatalessMaterializationOff()
	if !CgoEnabled {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("SetDatalessMaterializationOff() = %v, want ErrUnavailable without cgo", err)
		}
		if p, err := GetDatalessPolicy(); p != PolicyUnknown || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("GetDatalessPolicy() = %v, %v; want unknown, ErrUnavailable", p, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("SetDatalessMaterializationOff() = %v", err)
	}
	p, err := GetDatalessPolicy()
	if err != nil {
		t.Fatalf("GetDatalessPolicy() = %v", err)
	}
	if p != PolicyOff {
		t.Errorf("policy = %v (%d), want off", p, p)
	}
}

func TestDatalessPolicyString(t *testing.T) {
	cases := map[DatalessPolicy]string{
		PolicyDefault: "default", PolicyOff: "off", PolicyOn: "on",
		PolicyUnknown: "unknown", DatalessPolicy(7): "unknown",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("DatalessPolicy(%d).String() = %q, want %q", p, got, want)
		}
	}
}

func TestPurgeableDetail(t *testing.T) {
	info, err := PurgeableDetail(context.Background(), DataRoot)
	if !CgoEnabled {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("PurgeableDetail() = %v, want ErrUnavailable without cgo", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("PurgeableDetail() = %v", err)
	}
	if info.ImportantUsage <= 0 {
		t.Errorf("ImportantUsage = %d, want a positive capacity", info.ImportantUsage)
	}
	if info.StatfsAvail <= 0 {
		t.Errorf("StatfsAvail = %d, want a positive number", info.StatfsAvail)
	}
	if info.Bytes < 0 {
		t.Errorf("Bytes = %d, want a clamped non-negative number", info.Bytes)
	}
	if want := info.ImportantUsage - info.StatfsAvail; want > 0 && info.Bytes != want {
		t.Errorf("Bytes = %d, want %d", info.Bytes, want)
	}
	// Important-usage capacity includes purgeable space, so it is never
	// below what statfs reports as free.
	if info.ImportantUsage < info.StatfsAvail {
		t.Errorf("ImportantUsage %d below statfs available %d", info.ImportantUsage, info.StatfsAvail)
	}
	bytes, err := PurgeableBytes(context.Background(), DataRoot)
	if err != nil {
		t.Fatalf("PurgeableBytes() = %v", err)
	}
	if bytes < 0 {
		t.Errorf("PurgeableBytes() = %d", bytes)
	}
	t.Logf("purgeable: %+v", info)
}

func TestPurgeableDetailCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PurgeableDetail(ctx, DataRoot)
	if err == nil {
		t.Error("PurgeableDetail with a cancelled context returned no error")
	}
}

func TestPurgeableDetailBadPath(t *testing.T) {
	if !CgoEnabled {
		t.Skip("needs cgo")
	}
	if _, err := PurgeableDetail(context.Background(), "/no/such/volume"); err == nil {
		t.Error("PurgeableDetail on a missing path returned no error")
	}
}
