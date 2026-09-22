package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version is set at build time via
// -ldflags "-X github.com/asamgx/storix/internal/cli.Version=v0.1.0".
var Version = "dev"

// Commit returns the short VCS revision embedded by the Go toolchain, with a
// "-dirty" suffix when the tree had uncommitted changes. Empty when unknown.
func Commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	rev, dirty := "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 8 {
				rev = s.Value[:8]
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return ""
	}
	return rev + dirty
}

// BuildInfo returns "VERSION (COMMIT)" or just VERSION when the commit is unknown.
func BuildInfo() string {
	if c := Commit(); c != "" {
		return Version + " (" + c + ")"
	}
	return Version
}

func init() { register(newVersionCmd()) }

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the storix version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "storix "+BuildInfo())
			return err
		},
	}
}
