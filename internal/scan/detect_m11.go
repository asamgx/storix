package scan

// The rest of the docs/04 detectors register themselves the way the container
// ones do, and are blank-imported here for that effect alone.
//
// They are in a file of their own rather than appended to detect.go's import
// block because the two sets arrived in different milestones and are owned by
// different lanes; a detector added later goes in the list that matches its
// milestone, and the default registry is the concatenation of all of them in
// registration order.
import (
	_ "github.com/asamgx/storix/internal/detect/aimodels"
	_ "github.com/asamgx/storix/internal/detect/backups"
	_ "github.com/asamgx/storix/internal/detect/ecosystems"
	_ "github.com/asamgx/storix/internal/detect/ide"
	_ "github.com/asamgx/storix/internal/detect/jvm"
	_ "github.com/asamgx/storix/internal/detect/nix"
	_ "github.com/asamgx/storix/internal/detect/projects"
	_ "github.com/asamgx/storix/internal/detect/ruby"
	_ "github.com/asamgx/storix/internal/detect/xcode"
)
