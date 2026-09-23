//go:build race

package tui

// raceDetector says whether this test binary is instrumented. A frame budget
// stated for a released binary cannot be measured through the race detector,
// which costs an order of magnitude on exactly the row building and string
// assembly the render does.
const raceDetector = true
