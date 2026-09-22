package cli

import (
	"context"
	"errors"
)

// Exit codes: 0 completed scan, 2 configuration error, 130 interrupted.
const (
	ExitOK          = 0
	ExitConfigError = 2
	ExitInterrupted = 130
)

// ConfigError marks user/configuration errors (exit 2).
type ConfigError struct{ Err error }

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

// ExitCode maps an error returned from the root command to a process exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, context.Canceled):
		return ExitInterrupted
	}
	var ce *ConfigError
	if errors.As(err, &ce) {
		return ExitConfigError
	}
	return 1
}
