//go:build !cgo || !darwin

package mac

// CgoEnabled reports whether this binary was built with cgo, i.e. whether the
// Foundation and setiopolicy_np paths are compiled in.
const CgoEnabled = false

// SetDatalessMaterializationOff is unavailable without cgo. The walker does
// not depend on it: it never opens a file and never lists a SF_DATALESS
// directory, so no scan can materialize an evicted cloud file.
func SetDatalessMaterializationOff() error { return ErrUnavailable }

// GetDatalessPolicy is unavailable without cgo.
func GetDatalessPolicy() (DatalessPolicy, error) { return PolicyUnknown, ErrUnavailable }
