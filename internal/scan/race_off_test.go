//go:build !race

package scan

// raceDetector says whether this test binary is instrumented; see race_on_test.go.
const raceDetector = false
