//go:build !cgo || !darwin

package mac

import "context"

// PurgeableDetail is unavailable without cgo: Foundation is the only API that
// exposes the important-usage capacity, and the ledger shows purgeable as
// unknown in such a build.
func PurgeableDetail(_ context.Context, _ string) (PurgeableInfo, error) {
	return PurgeableInfo{}, ErrUnavailable
}
