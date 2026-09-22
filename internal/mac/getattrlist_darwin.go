//go:build darwin

package mac

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// VolAttrs holds the volume-level space attributes getattrlist reports.
// All four are byte counts.
type VolAttrs struct {
	Size       int64 // ATTR_VOL_SIZE: total container size
	SpaceFree  int64 // ATTR_VOL_SPACEFREE
	SpaceAvail int64 // ATTR_VOL_SPACEAVAIL: free to an unprivileged writer
	SpaceUsed  int64 // ATTR_VOL_SPACEUSED: this volume's own usage
}

// volAttrsRequest are the attributes asked for, in ascending bit order, which
// is also the order getattrlist packs them into the buffer.
const volAttrsRequest = unix.ATTR_VOL_INFO |
	unix.ATTR_VOL_SIZE | // 0x00000004
	unix.ATTR_VOL_SPACEFREE | // 0x00000008
	unix.ATTR_VOL_SPACEAVAIL | // 0x00000010
	unix.ATTR_VOL_SPACEUSED // 0x00800000

// volAttrsBufSize is u_int32_t length followed by four packed off_t values.
// Apple's sample buffers are declared __attribute__((aligned(4), packed)), so
// the off_t fields sit at offsets 4, 12, 20 and 28 with no padding.
const volAttrsBufSize = 4 + 4*8

// GetVolAttrs reads the volume space attributes for the volume containing
// path. x/sys/unix has no getattrlist (get) wrapper, so this is a raw
// syscall; the ABI is public in <sys/attr.h> and darwin/arm64 Syscall6 is a
// direct SVC, so no cgo is involved.
//
// It is a cross-check on statfs and the Q8 purgeable spike: SpaceAvail minus
// statfs f_bavail*f_bsize would be a pure-Go purgeable source if the two ever
// disagreed.
func GetVolAttrs(path string) (VolAttrs, error) {
	pathPtr, err := unix.BytePtrFromString(path)
	if err != nil {
		return VolAttrs{}, fmt.Errorf("getattrlist %s: %w", path, err)
	}
	list := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     volAttrsRequest,
	}
	buf := make([]byte, volAttrsBufSize)

	// x/sys/unix has no getattrlist (get) wrapper and neither does the
	// standard library, so the syscall is made by number. Its ABI is public
	// in <sys/attr.h> and darwin/arm64 Syscall6 is a direct SVC.
	//nolint:staticcheck // SA1019: no libSystem wrapper exists for this call
	_, _, errno := unix.Syscall6(
		unix.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&list)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, // options
		0,
	)
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(&list)
	if errno != 0 {
		return VolAttrs{}, fmt.Errorf("getattrlist %s: %w", path, errno)
	}

	n := binary.LittleEndian.Uint32(buf)
	if int(n) != volAttrsBufSize {
		return VolAttrs{}, fmt.Errorf(
			"getattrlist %s: returned %d bytes, want %d (volume does not report all space attributes)",
			path, n, volAttrsBufSize)
	}
	return VolAttrs{
		Size:       int64(binary.LittleEndian.Uint64(buf[4:])),
		SpaceFree:  int64(binary.LittleEndian.Uint64(buf[12:])),
		SpaceAvail: int64(binary.LittleEndian.Uint64(buf[20:])),
		SpaceUsed:  int64(binary.LittleEndian.Uint64(buf[28:])),
	}, nil
}
