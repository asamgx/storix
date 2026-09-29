//go:build race

package scan

// raceDetector says whether this test binary is instrumented. A budget stated
// for a released binary cannot be measured through the race detector, which
// costs an order of magnitude on exactly the pointer-chasing the engine does.
const raceDetector = true
