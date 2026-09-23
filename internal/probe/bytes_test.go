package probe

import "testing"

func TestParseHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{
		// The four sizes `docker system df --format json` printed on the
		// planning machine, verbatim.
		{"9.853GB", 9_853_000_000, true},
		{"5.379GB (54%)", 5_379_000_000, true},
		{"135.5MB", 135_500_000, true},
		{"135.7MB", 135_700_000, true},
		{"4.755GB", 4_755_000_000, true},
		{"938.7MB (19%)", 938_700_000, true},
		{"5.085GB", 5_085_000_000, true},
		{"4.638GB", 4_638_000_000, true},

		{"0B", 0, true},
		{"0B (0%)", 0, true},
		{"1kB", 1_000, true},
		{"1.5 GiB", 1_610_612_736, true},
		{"2MiB", 2 << 20, true},
		{"1024", 1024, true},
		{"12.5 GB", 12_500_000_000, true},

		{"", 0, false},
		{"N/A", 0, false},
		{"-", 0, false},
		{"lots", 0, false},
		{"GB", 0, false},
		{"twoGB", 0, false},
	} {
		got, ok := ParseHumanBytes(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseHumanBytes(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParsePercent(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"5.379GB (54%)", 54, true},
		{"938.7MB (19%)", 19, true},
		{"135.5MB (99%)", 99, true},
		{"0B (0%)", 0, true},
		{"4.638GB", 0, false},
		{"5.379GB (54", 0, false},
		{"5.379GB (lots%)", 0, false},
	} {
		got, ok := ParsePercent(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParsePercent(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseCount(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"36", 36}, {" 11 ", 11}, {"0", 0}, {"", 0}, {"many", 0},
	} {
		if got := ParseCount(tc.in); got != tc.want {
			t.Errorf("ParseCount(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
