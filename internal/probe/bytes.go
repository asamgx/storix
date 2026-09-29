package probe

import (
	"strconv"
	"strings"
)

// humanUnits are the suffixes a tool prints sizes with, longest first so that
// "GiB" is matched before "B". Docker formats with decimal units ("9.853GB"
// is 9.853 × 10⁹), which is also what Finder means by GB, so the two agree;
// the binary suffixes are here because other tools print them.
var humanUnits = []struct {
	suffix string
	scale  float64
}{
	{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"TIB", 1 << 40}, {"PIB", 1 << 50},
	{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"PB", 1e15},
	{"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15},
	{"B", 1},
}

// ParseHumanBytes reads a size the way a command line prints one: "9.853GB",
// "135.5MB", "0B", "1.5 GiB". A trailing percentage in parentheses is
// ignored, so the "5.379GB (54%)" that `docker system df` prints in its
// Reclaimable column parses as a size without the caller stripping it first.
//
// It exists because the JSON that `--format json` produces is JSON only in
// its structure: the values inside it are the same human strings the table
// shows, so a parser is needed either way.
func ParseHumanBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" || s == "-" {
		return 0, false
	}
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	up := strings.ToUpper(s)
	for _, u := range humanUnits {
		if !strings.HasSuffix(up, u.suffix) {
			continue
		}
		num := strings.TrimSpace(up[:len(up)-len(u.suffix)])
		if num == "" {
			continue
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, false
		}
		return int64(f*u.scale + 0.5), true
	}
	// A bare number is already bytes.
	if f, err := strconv.ParseFloat(up, 64); err == nil {
		return int64(f + 0.5), true
	}
	return 0, false
}

// ParsePercent reads the "(54%)" a size may be followed by. The second result
// is false when there is none, which is how `docker system df` reports a
// build cache: it is all reclaimable and prints no percentage.
func ParsePercent(s string) (int, bool) {
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return 0, false
	}
	rest := s[open+1:]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return 0, false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest[:end]), "%"))
	n, err := strconv.Atoi(inner)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ParseCount reads an integer field such as `docker system df`'s TotalCount,
// which is also a string in the JSON. An unreadable field is zero.
func ParseCount(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
