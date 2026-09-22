package units

import "testing"

func TestDecimalBytesMatchesFinder(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 bytes"},
		{1, "1 byte"},
		{999, "999 bytes"},
		{1000, "1 KB"},
		{1500, "2 KB"},
		{12_345, "12 KB"},
		{999_500, "1 MB"},
		{1_000_000, "1 MB"},
		{1_200_000, "1.2 MB"},
		{12_345_678, "12.3 MB"},
		{123_456_789, "123.5 MB"},
		{1_000_000_000, "1 GB"},
		{1_200_000_000, "1.2 GB"},
		{1_234_567_890, "1.23 GB"},
		{18_800_000_000, "18.8 GB"},
		{177_565_782_016, "177.57 GB"},
		{245_100_000_000, "245.1 GB"},
		{1_000_000_000_000, "1 TB"},
		{-1_234_567_890, "-1.23 GB"},
	}
	for _, c := range cases {
		if got := Decimal.Bytes(c.n); got != c.want {
			t.Errorf("Decimal.Bytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestBinaryBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{1024, "1 KiB"},
		{1_234_567_890, "1.15 GiB"},
		{1 << 30, "1 GiB"},
	}
	for _, c := range cases {
		if got := Binary.Bytes(c.n); got != c.want {
			t.Errorf("Binary.Bytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFixedWidth(t *testing.T) {
	for _, n := range []int64{0, 5, 12_345, 12_345_678, 1_234_567_890, 177_565_782_016} {
		s := Decimal.Fixed(n)
		if len(s) < 9 {
			t.Errorf("Fixed(%d) = %q shorter than 9", n, s)
		}
	}
	if got := Decimal.Fixed(12_345_678_901); got != "  12.3 GB" {
		t.Errorf("Fixed = %q", got)
	}
}

func TestPercent(t *testing.T) {
	if got := Percent(1, 0); got != "0.0%" {
		t.Errorf("got %q", got)
	}
	if got := Percent(123, 1000); got != "12.3%" {
		t.Errorf("got %q", got)
	}
}

func TestParse(t *testing.T) {
	cases := map[string]int64{
		"512":    512,
		"64KB":   64_000,
		"64 kb":  64_000,
		"1 MB":   1_000_000,
		"1MiB":   1_048_576,
		"1.5 GB": 1_500_000_000,
		"10mb":   10_000_000,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "12 parsecs"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) expected error", bad)
		}
	}
}
