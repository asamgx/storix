package volume

import (
	"context"
	"errors"
	"time"

	"github.com/asamgx/storix/internal/mac"
)

// purgeableTimeout bounds the Foundation call.
const purgeableTimeout = 5 * time.Second

// Purgeable is a purgeable-space reading, or the reason there is none.
// A failed reading is reported as unknown and folded into the ledger's
// residual line; it is never silently treated as zero.
type Purgeable struct {
	Bytes  int64
	Known  bool
	Source string // "foundation" when known, "" otherwise
	Err    string

	// Raw inputs, kept so doctor and the debug output can show the
	// subtraction that produced Bytes.
	ImportantUsage int64
	StatfsAvail    int64
}

// ReadPurgeable reads the purgeable bytes of the volume containing
// volumePath. It never returns an error: a build without cgo, a timeout or a
// Foundation failure all produce Known=false with the reason in Err.
func ReadPurgeable(ctx context.Context, volumePath string) Purgeable {
	ctx, cancel := context.WithTimeout(ctx, purgeableTimeout)
	defer cancel()

	info, err := mac.PurgeableDetail(ctx, volumePath)
	switch {
	case errors.Is(err, mac.ErrUnavailable):
		return Purgeable{Err: "built without cgo; Foundation is the only source of purgeable space"}
	case err != nil:
		return Purgeable{Err: err.Error()}
	}
	return Purgeable{
		Bytes:          info.Bytes,
		Known:          true,
		Source:         "foundation",
		ImportantUsage: info.ImportantUsage,
		StatfsAvail:    info.StatfsAvail,
	}
}
