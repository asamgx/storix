//go:build darwin && cgo

package mac

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
int storix_important_usage(const char *path, long long *out, char *errbuf, int errlen);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// PurgeableDetail reads the purgeable space of the volume containing
// volumePath. The Foundation call is synchronous, so it runs on its own
// goroutine and ctx cancellation abandons the result rather than interrupting
// the call.
func PurgeableDetail(ctx context.Context, volumePath string) (PurgeableInfo, error) {
	type outcome struct {
		info PurgeableInfo
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		info, err := purgeableDetail(volumePath)
		done <- outcome{info, err}
	}()
	select {
	case <-ctx.Done():
		return PurgeableInfo{}, ctx.Err()
	case o := <-done:
		return o.info, o.err
	}
}

func purgeableDetail(volumePath string) (PurgeableInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(volumePath, &st); err != nil {
		return PurgeableInfo{}, fmt.Errorf("statfs %s: %w", volumePath, err)
	}
	avail := int64(st.Bavail) * int64(st.Bsize)

	cpath := C.CString(volumePath)
	defer C.free(unsafe.Pointer(cpath))
	errbuf := make([]byte, 512)
	var important C.longlong
	rc := C.storix_important_usage(
		cpath,
		&important,
		(*C.char)(unsafe.Pointer(&errbuf[0])),
		C.int(len(errbuf)),
	)
	if rc != 0 {
		return PurgeableInfo{}, errors.New("important-usage capacity for " + volumePath + ": " + cstring(errbuf))
	}

	info := PurgeableInfo{ImportantUsage: int64(important), StatfsAvail: avail}
	if d := info.ImportantUsage - info.StatfsAvail; d > 0 {
		info.Bytes = d
	}
	return info, nil
}

// cstring returns the NUL-terminated prefix of b as a Go string.
func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
