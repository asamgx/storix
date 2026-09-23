package scan

// The application inventory registers itself the way the container and
// package-manager detectors do, and is imported here for that effect.
//
// It lives in internal/apps rather than under internal/detect because it is
// more than a detector: the Apps view, `storix apps` and `storix explain` all
// read its report, and internal/report and internal/cli import it directly.
// The blank import is kept even though this package also names it elsewhere,
// so that the list of what a scan knows about stays complete in one place.
import _ "github.com/asamgx/storix/internal/apps"
