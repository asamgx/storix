package mac

import "errors"

// ErrUnavailable is returned by the calls that need cgo when the binary was
// built without it (CGO_ENABLED=0) or for a non-darwin target.
var ErrUnavailable = errors.New("mac: not available in this build (built without cgo)")

// DatalessPolicy is the process-wide IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES
// setting. The numeric values match <sys/resource.h>:
// IOPOL_DEFAULT = 0, IOPOL_MATERIALIZE_DATALESS_FILES_OFF = 1,
// IOPOL_MATERIALIZE_DATALESS_FILES_ON = 2.
type DatalessPolicy int

const (
	// PolicyUnknown means the policy could not be read.
	PolicyUnknown DatalessPolicy = -1
	// PolicyDefault is the inherited system default.
	PolicyDefault DatalessPolicy = 0
	// PolicyOff means reads and directory listings never materialize
	// dataless (evicted cloud) items; they fail with EDEADLK instead.
	PolicyOff DatalessPolicy = 1
	// PolicyOn means access materializes dataless items, i.e. downloads them.
	PolicyOn DatalessPolicy = 2
)

func (p DatalessPolicy) String() string {
	switch p {
	case PolicyDefault:
		return "default"
	case PolicyOff:
		return "off"
	case PolicyOn:
		return "on"
	default:
		return "unknown"
	}
}
