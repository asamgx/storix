//go:build darwin && cgo

package mac

/*
#include <sys/resource.h>
#include <errno.h>

// Values from <sys/resource.h>; spelled out so a mismatch is visible here.
static const int storix_iopol_type_dataless = IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES; // 3
static const int storix_iopol_scope_process = IOPOL_SCOPE_PROCESS;                       // 0
static const int storix_iopol_dataless_off  = IOPOL_MATERIALIZE_DATALESS_FILES_OFF;      // 1
*/
import "C"

import (
	"fmt"
	"syscall"
)

// CgoEnabled reports whether this binary was built with cgo, i.e. whether the
// Foundation and setiopolicy_np paths are compiled in.
const CgoEnabled = true

// SetDatalessMaterializationOff sets the process IO policy so that neither a
// read nor a directory listing downloads an evicted cloud file. It is defence
// in depth: the walker never opens a file and never lists a SF_DATALESS
// directory, so a build without this call is still correct.
func SetDatalessMaterializationOff() error {
	rc, err := C.setiopolicy_np(C.storix_iopol_type_dataless, C.storix_iopol_scope_process, C.storix_iopol_dataless_off)
	if rc != 0 {
		if err == nil {
			err = syscall.EINVAL
		}
		return fmt.Errorf("setiopolicy_np(dataless, process, off): %w", err)
	}
	return nil
}

// GetDatalessPolicy reads the current process dataless-materialization policy.
func GetDatalessPolicy() (DatalessPolicy, error) {
	rc, err := C.getiopolicy_np(C.storix_iopol_type_dataless, C.storix_iopol_scope_process)
	if rc < 0 {
		if err == nil {
			err = syscall.EINVAL
		}
		return PolicyUnknown, fmt.Errorf("getiopolicy_np(dataless, process): %w", err)
	}
	return DatalessPolicy(rc), nil
}
