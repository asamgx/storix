package classify

import "github.com/asamgx/storix/internal/walk"

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

// Claim is one assertion about one node. Detectors build claims directly; the
// engine builds them from rules and resolves the two against each other.
//
// There is one ownership vocabulary and it lives in two fields, so every lane
// that produces claims — the catalog, the tool detectors, the application
// inventory — says the same thing the same way:
//
//   - Owner is the display label a person reads: "Homebrew", "VS Code",
//     "com.spotify.client". It is never parsed and never joined on.
//   - OwnerKeys are the machine identifiers a footprint joins on, each one
//     prefixed by what it is: "app:<bundleid>", "team:<TEAMID>",
//     "vendor:<reverse-dns>", "cask:<token>", "cli:<name>",
//     "project:<name>", "product:<slug>", "unknown:<dirname>", and the bare
//     "macos" for what the system owns.
//
// There is deliberately no second label field and no owner struct: an
// application's footprint is Classification.ByOwnerKey over its identifiers,
// and the prefix is what stops a cask token from matching a bundle id that
// happens to spell the same word.
type Claim struct {
	// Node is the node the claim is about. Detectors hold nodes rather
	// than indices because they find their paths with Tree.Lookup; the
	// node carries its own ID, so the engine needs no reverse map.
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
	// Depth and Shape are the specificity of the pattern that matched,
	// carried on the claim so the resolver needs nothing else: Depth is the
	// number of segments and Shape packs their classes, deepest first.
	Depth    uint16
	Shape    uint64
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
//
// Specificity is depth first and then shape, which compares the two patterns
// segment by segment from the deepest one back towards the root. Depth alone
// is not enough: "~/Library/Caches/*.ShipIt" and "~/Library/Caches/{bundleid}"
// are the same depth over the same directory, and the first is plainly the
// more specific answer. Two segments of the same class tie, and Priority is
// what an author reaches for then.
func better(a, b *Claim) bool {
	switch {
	case a.Source.Kind != b.Source.Kind:
		return a.Source.Kind > b.Source.Kind
	case a.Depth != b.Depth:
		return a.Depth > b.Depth
	case a.Shape != b.Shape:
		return a.Shape > b.Shape
	case a.Priority != b.Priority:
		return a.Priority > b.Priority
	default:
		return a.Source.ID < b.Source.ID
	}
}
