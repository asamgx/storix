package walk

import "sort"

// partialTriggers are the child flags that make an ancestor's totals a lower bound.
const partialTriggers = FlagUnreadable | FlagPartial | FlagMountSkipped | FlagSkipListed

// finalize turns the raw tree the workers built into a usable one. It runs
// single-threaded after every worker has returned, which is what lets the walk
// itself skip atomics on the parent chain.
//
// Order matters: hard links are resolved first (they move bytes between
// nodes), then the preorder index is built, then totals are summed bottom-up.
func (w *walker) finalize(tree *Tree) {
	tree.LinkGroups, tree.LinkBytesSaved = w.links.resolve()
	w.links = nil

	tree.Nodes = preorder(tree.Root)

	// Preorder puts every parent before its children, so walking it backwards
	// visits every child before its parent: one pass sums the whole tree.
	for i := len(tree.Nodes) - 1; i >= 0; i-- {
		n := tree.Nodes[i]
		if !n.IsDir() {
			continue
		}
		n.Bytes += n.Small.Bytes
		n.Apparent += n.Small.Apparent
		n.Files += n.Small.Files
		for _, c := range n.Children {
			n.Bytes += c.Bytes
			n.Apparent += c.Apparent
			n.Files += c.Files
			if c.IsDir() {
				n.Dirs += 1 + c.Dirs
				if c.Flags&FlagDataless != 0 {
					n.Flags |= FlagPartial
				}
			}
			if c.Flags&partialTriggers != 0 {
				n.Flags |= FlagPartial
			}
		}
	}

	tree.Errors = w.errs
	sort.Slice(tree.Errors, func(i, j int) bool {
		if tree.Errors[i].Path != tree.Errors[j].Path {
			return tree.Errors[i].Path < tree.Errors[j].Path
		}
		return tree.Errors[i].Op < tree.Errors[j].Op
	})
	tree.SkippedMounts = w.skippedMounts
	sort.Slice(tree.SkippedMounts, func(i, j int) bool {
		return tree.SkippedMounts[i].Path < tree.SkippedMounts[j].Path
	})
	tree.SkipListed = w.skipListed
	sort.Strings(tree.SkipListed)
	tree.Vanished = w.vanished.Load()
	tree.Incomplete = w.incomplete.Load()
}

// preorder indexes the tree depth-first, left to right, sorting any child list
// that is not already in name order. os.ReadDir sorts, so this normally only
// verifies; a custom DirReader may not.
//
// Every node is stamped with its index as it is appended, so Node.ID and the
// position in the returned slice can never disagree. internal/cache/reader.go
// repeats this walk over a rebuilt tree and must stamp the same way.
func preorder(root *Node) []*Node {
	nodes := make([]*Node, 0, 64)
	stack := []*Node{root}
	for len(stack) > 0 {
		i := len(stack) - 1
		n := stack[i]
		stack = stack[:i]
		n.ID = int32(len(nodes))
		nodes = append(nodes, n)
		if len(n.Children) == 0 {
			continue
		}
		if !sortedByName(n.Children) {
			sort.Slice(n.Children, func(a, b int) bool { return n.Children[a].Name < n.Children[b].Name })
		}
		for j := len(n.Children) - 1; j >= 0; j-- {
			stack = append(stack, n.Children[j])
		}
	}
	return nodes
}

func sortedByName(ns []*Node) bool {
	for i := 1; i < len(ns); i++ {
		if ns[i-1].Name > ns[i].Name {
			return false
		}
	}
	return true
}
