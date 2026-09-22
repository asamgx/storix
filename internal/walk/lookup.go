package walk

import (
	"path/filepath"
	"sort"
	"strings"
)

// Lookup finds a node by absolute scan path. It walks the sorted child lists
// with a binary search per segment, which costs O(depth·log n) and no memory,
// rather than keeping a path map that would cost more than the tree itself.
func (t *Tree) Lookup(scanPath string) (*Node, bool) {
	if t == nil || t.Root == nil {
		return nil, false
	}
	p := filepath.Clean(scanPath)
	root := t.Root.Name
	if p == root {
		return t.Root, true
	}
	sep := root + "/"
	if root == "/" {
		sep = "/"
	}
	if !strings.HasPrefix(p, sep) {
		return nil, false
	}
	n := t.Root
	for _, seg := range strings.Split(p[len(sep):], "/") {
		if seg == "" {
			continue
		}
		kids := n.Children
		i := sort.Search(len(kids), func(i int) bool { return kids[i].Name >= seg })
		if i == len(kids) || kids[i].Name != seg {
			return nil, false
		}
		n = kids[i]
	}
	return n, true
}

// Under returns the subtree rooted at prefix in preorder, prefix first. The
// result matches the corresponding run of Tree.Nodes.
func (t *Tree) Under(prefix string) []*Node {
	n, ok := t.Lookup(prefix)
	if !ok {
		return nil
	}
	return preorder(n)
}
