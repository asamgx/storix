package classify

import "github.com/asamgx/storix/internal/walk"

// Rule is one declarative catalog entry: a path pattern and what the bytes
// under it are. Rules live in typed Go tables under internal/classify/catalog
// rather than in a config file, so a rule that stops matching is a test
// failure rather than a silent hole in the ledger.
type Rule struct {
	// ID is the unique dotted identifier, <area>.<tool>.<what>. It is the
	// provenance the why panel prints and the key the rule tests use.
	ID string
	// Match is an anchored display-path pattern. See pattern.go for the
	// grammar; there is deliberately no "**", because anything that means
	// "anywhere under X" is a detector's job, not a rule's.
	Match string
	// Bucket is where the matched bytes are counted.
	Bucket Bucket
	// Category is the sub-heading the bucket groups the bytes under, such
	// as "Package cache" or "Simulators".
	Category string
	// Owner is the display label for whoever the bytes belong to. It may
	// reference a capture in Match, as "{name}".
	Owner string
	// OwnerKeys are the prefixed identifiers an application footprint can
	// join on ("app:com.microsoft.VSCode", "cli:pnpm", "macos"). Entries
	// may reference captures the same way Owner does.
	OwnerKeys []string
	// Reclaim is how safely the bytes can be freed.
	Reclaim Reclaim
	// Explain is the one line the why panel shows.
	Explain string
	// Priority breaks ties between rules of equal specificity; higher wins.
	Priority int8
	// Leaf allows the rule to match a file node. Rules match directories
	// only by default, because a file that happens to be named like a tool
	// directory should not carry that tool's bucket.
	Leaf bool
}

// SourceKind is where a claim came from. The order is the precedence order of
// docs/03: a detector that probed the machine outranks the apps inventory,
// which outranks a static path rule.
type SourceKind uint8

const (
	// SourceRule is a catalog rule.
	SourceRule SourceKind = iota
	// SourceApps is the application inventory.
	SourceApps
	// SourceDetector is a tool detector that probed the machine.
	SourceDetector
)

// sourceKindNames are the names, indexed by the kind.
var sourceKindNames = [...]string{"rule", "apps", "detector"}

func (k SourceKind) String() string {
	if int(k) >= len(sourceKindNames) {
		return "invalid"
	}
	return sourceKindNames[k]
}

// Source identifies what made a claim.
type Source struct {
	Kind SourceKind
	// ID is the rule id for a rule, or the detector's own claim id.
	ID string
	// Detector is the detector's name, empty for a rule.
	Detector string
}

// String renders the source the way the why panel and the JSON report do:
// "rule:cache.homebrew", "detector:orbstack".
func (s Source) String() string {
	if s.Detector != "" {
		return "detector:" + s.Detector
	}
	return s.Kind.String() + ":" + s.ID
}

// Claim is one assertion about one node. Detectors build claims directly;
// the engine builds them from rules and resolves the two against each other.
type Claim struct {
	// Node is the node the claim is about. Detectors hold nodes rather
	// than indices because they find their paths with Tree.Lookup, and the
	// engine maps pointer to index for claimed nodes only.
	Node       *walk.Node
	Bucket     Bucket
	Category   string
	Owner      string
	OwnerKeys  []string
	Reclaim    Reclaim
	Confidence Confidence
	Source     Source
	// Evidence are verbatim lines for the why panel: the command that was
	// run, the receipt that was read, the pattern that matched.
	Evidence []string
	// Depth and Literals are the specificity of the pattern that matched,
	// carried on the claim so the resolver needs nothing else.
	Depth    uint16
	Literals uint16
	Priority int8
	// NoInherit keeps the claim on its own node: the children of the node
	// inherit whatever the node itself inherited instead.
	NoInherit bool
}

// Conflict records a claim that lost to another on the same node. Conflicts
// are data rather than warnings: two sources agreeing on a node is the normal
// case, and the interesting ones are the disagreements over many bytes, which
// is why the list is sorted by size.
type Conflict struct {
	Node   int32  `json:"node"`
	Path   string `json:"path"`
	Winner Source `json:"winner"`
	Loser  Source `json:"loser"`
	Bytes  int64  `json:"bytes"`
}

// maxConflicts is how many conflicts are kept. The list is for tuning, and a
// reader never looks past the first twenty; the cap stops a pathological
// catalog from turning the classification into a log file.
const maxConflicts = 2000

// better reports whether a wins over b for the same node: source kind first,
// then specificity, then priority, then the source id so that two runs over
// the same tree always pick the same winner.
func better(a, b *Claim) bool {
	switch {
	case a.Source.Kind != b.Source.Kind:
		return a.Source.Kind > b.Source.Kind
	case a.Depth != b.Depth:
		return a.Depth > b.Depth
	case a.Literals != b.Literals:
		return a.Literals > b.Literals
	case a.Priority != b.Priority:
		return a.Priority > b.Priority
	default:
		return a.Source.ID < b.Source.ID
	}
}
