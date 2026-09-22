// Package units formats and parses byte counts.
//
// Decimal formatting matches Finder / System Settings (NSByteCountFormatter):
// bytes and KB have no decimals, MB one, GB and TB two, with trailing zeros
// trimmed ("1 GB", "1.2 GB", "1.23 GB"). Binary uses the same digit rules with
// KiB/MiB/GiB/TiB.
package units

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Format selects decimal (1000) or binary (1024) units.
type Format uint8

const (
	Decimal Format = iota
	Binary
)

func (f Format) base() float64 {
	if f == Binary {
		return 1024
	}
	return 1000
}

func (f Format) suffixes() []string {
	if f == Binary {
		return []string{"bytes", "KiB", "MiB", "GiB", "TiB", "PiB"}
	}
	return []string{"bytes", "KB", "MB", "GB", "TB", "PB"}
}

// decimals returns the number of fraction digits Finder uses at each magnitude.
func decimals(exp int) int {
	switch exp {
	case 0, 1:
		return 0
	case 2:
		return 1
	default:
		return 2
	}
}

// split returns the scaled value and magnitude index for n.
func (f Format) split(n int64) (float64, int) {
	if n == 0 {
		return 0, 0
	}
	v := math.Abs(float64(n))
	exp := 0
	base := f.base()
	sfx := f.suffixes()
	for v >= base && exp < len(sfx)-1 {
		v /= base
		exp++
	}
	if n < 0 {
		v = -v
	}
	return v, exp
}

// Bytes formats n the way Finder does: "999 bytes", "12 KB", "12.3 MB", "1.23 GB".
func (f Format) Bytes(n int64) string {
	v, exp := f.split(n)
	sfx := f.suffixes()
	if exp == 0 {
		if n == 1 || n == -1 {
			return strconv.FormatInt(n, 10) + " byte"
		}
		return strconv.FormatInt(n, 10) + " " + sfx[0]
	}
	d := decimals(exp)
	s := strconv.FormatFloat(v, 'f', d, 64)
	// Rounding can produce "1000 KB"; promote to the next unit.
	if abs, _ := strconv.ParseFloat(s, 64); math.Abs(abs) >= f.base() && exp < len(sfx)-1 {
		exp++
		v /= f.base()
		d = decimals(exp)
		s = strconv.FormatFloat(v, 'f', d, 64)
	}
	if d > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s + " " + sfx[exp]
}

// Fixed formats n for right-aligned table columns: always one decimal from MB
// upward, padded to 9 characters ("  12.3 GB", " 999 bytes" is wider and left as is).
func (f Format) Fixed(n int64) string {
	v, exp := f.split(n)
	sfx := f.suffixes()
	var s string
	switch exp {
	case 0:
		s = strconv.FormatInt(n, 10) + " B"
	case 1:
		s = strconv.FormatFloat(v, 'f', 0, 64) + " " + sfx[1]
	default:
		s = strconv.FormatFloat(v, 'f', 1, 64) + " " + sfx[exp]
	}
	if len(s) < 9 {
		s = strings.Repeat(" ", 9-len(s)) + s
	}
	return s
}

// Percent formats part/whole as "12.3%"; "0.0%" when whole is zero.
func Percent(part, whole int64) string {
	if whole == 0 {
		return "0.0%"
	}
	return strconv.FormatFloat(100*float64(part)/float64(whole), 'f', 1, 64) + "%"
}

var multipliers = map[string]int64{
	"":      1,
	"b":     1,
	"byte":  1,
	"bytes": 1,
	"k":     1000, "kb": 1000,
	"m": 1000 * 1000, "mb": 1000 * 1000,
	"g": 1000 * 1000 * 1000, "gb": 1000 * 1000 * 1000,
	"t": 1000 * 1000 * 1000 * 1000, "tb": 1000 * 1000 * 1000 * 1000,
	"ki": 1024, "kib": 1024,
	"mi": 1024 * 1024, "mib": 1024 * 1024,
	"gi": 1024 * 1024 * 1024, "gib": 1024 * 1024 * 1024,
	"ti": 1024 * 1024 * 1024 * 1024, "tib": 1024 * 1024 * 1024 * 1024,
}

// Parse accepts "512", "64KB", "1 MB", "1MiB", "1.5 GB" and returns bytes.
func Parse(s string) (int64, error) {
	t := strings.TrimSpace(strings.ToLower(s))
	if t == "" {
		return 0, fmt.Errorf("units: empty size")
	}
	i := 0
	for i < len(t) && (t[i] >= '0' && t[i] <= '9' || t[i] == '.') {
		i++
	}
	num, unit := t[:i], strings.TrimSpace(t[i:])
	if num == "" {
		return 0, fmt.Errorf("units: %q has no number", s)
	}
	mult, ok := multipliers[unit]
	if !ok {
		return 0, fmt.Errorf("units: unknown unit %q in %q", unit, s)
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("units: %q: %w", s, err)
	}
	return int64(math.Round(v * float64(mult))), nil
}
