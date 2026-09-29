package scan

// The package-manager and toolchain detectors register themselves from their
// own init functions, the way the container detectors in detect.go do. They
// are imported for that effect alone.
//
// They are listed here rather than beside the container imports so that the
// two sets can be added, removed and reviewed independently: internal/detect
// must not import its own detector packages, so this file and its neighbour
// are the only places that name them, and one file per milestone keeps that
// list readable.
import (
	_ "github.com/asamgx/storix/internal/detect/clitools"
	_ "github.com/asamgx/storix/internal/detect/golang"
	_ "github.com/asamgx/storix/internal/detect/homebrew"
	_ "github.com/asamgx/storix/internal/detect/node"
	_ "github.com/asamgx/storix/internal/detect/python"
	_ "github.com/asamgx/storix/internal/detect/rust"
)
