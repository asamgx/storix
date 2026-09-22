package mac

import "context"

// PurgeableInfo is the result of a purgeable-space reading.
//
// macOS exposes no direct "purgeable bytes" attribute (verified: sys/attr.h
// has no such volume attribute and diskutil prints no such field on macOS 26).
// The number is derived: Foundation reports the capacity the system would make
// available to an important write, which includes space it would reclaim by
// evicting purgeable content; statfs reports only the space free right now.
// The difference is the purgeable amount.
type PurgeableInfo struct {
	// Bytes is ImportantUsage - StatfsAvail, clamped at zero.
	Bytes int64
	// ImportantUsage is NSURLVolumeAvailableCapacityForImportantUsageKey.
	ImportantUsage int64
	// StatfsAvail is statfs f_bavail * f_bsize at the time of the reading.
	StatfsAvail int64
}

// PurgeableBytes returns the purgeable bytes on the volume containing
// volumePath, or [ErrUnavailable] in a build without cgo. Use
// [PurgeableDetail] when the raw inputs are wanted as well.
func PurgeableBytes(ctx context.Context, volumePath string) (int64, error) {
	info, err := PurgeableDetail(ctx, volumePath)
	return info.Bytes, err
}
