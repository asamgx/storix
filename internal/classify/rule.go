package classify

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
	// reference a capture in Match, as "{name}". See Claim for the one
	// ownership vocabulary Owner and OwnerKeys share.
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
