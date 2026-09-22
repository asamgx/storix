package walk

import (
	"fmt"
	"strings"
)

// dumpTree renders a tree as text that captures everything a scan is supposed
// to determine and nothing that depends on timing. Two walks of the same tree
// must produce the same dump whatever the parallelism.
func dumpTree(t *Tree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "incomplete=%v linkGroups=%d linkBytesSaved=%d vanished=%d\n",
		t.Incomplete, t.LinkGroups, t.LinkBytesSaved, t.Vanished)
	for _, n := range t.Nodes {
		fmt.Fprintf(&b, "%s kind=%s bytes=%d apparent=%d files=%d dirs=%d flags=%04x errno=%d small=%d/%d/%d/%d\n",
			n.Path(), n.Kind, n.Bytes, n.Apparent, n.Files, n.Dirs, n.Flags, n.Errno,
			n.Small.Files, n.Small.Dataless, n.Small.Bytes, n.Small.Apparent)
	}
	for _, e := range t.Errors {
		fmt.Fprintf(&b, "error %s %s %d %s\n", e.Op, e.Path, int(e.Errno), e.Class)
	}
	for _, m := range t.SkippedMounts {
		fmt.Fprintf(&b, "mount %s %s %s\n", m.Path, m.FSType, m.From)
	}
	for _, p := range t.SkipListed {
		fmt.Fprintf(&b, "skiplisted %s\n", p)
	}
	return b.String()
}
