package walk

import (
	"fmt"
	"strings"
	"syscall"
	"time"
)

// genReader synthesizes a balanced tree without touching a filesystem: every
// directory above maxDepth has subdirs subdirectories, and every directory has
// files leaves. It allocates nothing per node, so a memory measurement of a
// walk over it sees the tree and nothing else.
type genReader struct {
	root      string
	maxDepth  int
	subdirs   int
	files     int
	fileBytes int64
	delay     time.Duration
}

// nodes returns how many nodes a full walk of the generated tree produces.
func (g *genReader) nodes() int {
	dirs, level := 0, 1
	for d := 0; d <= g.maxDepth; d++ {
		dirs += level
		level *= g.subdirs
	}
	return dirs + dirs*g.files
}

func (g *genReader) depthOf(p string) int {
	rest := strings.TrimPrefix(p, g.root)
	if rest == "" {
		return 0
	}
	return strings.Count(rest, "/")
}

func (g *genReader) ReadDir(p string) ([]Entry, error) {
	if !strings.HasPrefix(p, g.root) {
		return nil, syscall.ENOENT
	}
	if g.delay > 0 {
		time.Sleep(g.delay)
	}
	depth := g.depthOf(p)
	n := g.files
	if depth < g.maxDepth {
		n += g.subdirs
	}
	out := make([]Entry, 0, n)
	if depth < g.maxDepth {
		for i := range g.subdirs {
			out = append(out, Entry{Name: fmt.Sprintf("d%d", i), Kind: KindDir, Dev: 1, Nlink: 2, Ino: uint64(i + 1)})
		}
	}
	blocks := (g.fileBytes + 511) / 512
	for i := range g.files {
		out = append(out, Entry{
			Name: fmt.Sprintf("f%02d", i), Kind: KindFile, Dev: 1, Nlink: 1,
			Ino: uint64(1_000_000 + i), Blocks: blocks, Size: g.fileBytes,
		})
	}
	return out, nil
}

func (g *genReader) Stat(p string) (Entry, error) {
	return Entry{Name: p, Kind: KindDir, Dev: 1, Nlink: 2}, nil
}
