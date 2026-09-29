package catalog

import "github.com/asamgx/storix/internal/classify"

// trashRules cover what the user has already decided to delete. The tag is
// tool-managed rather than regenerable because emptying the Trash is the
// owning tool's job — the Finder's — and because the bytes are not rebuilt
// afterwards, they are gone.
//
// Both rules carry a priority above the generic dot-directory rule, which is
// exactly as specific: "~/.Trash" and "~/.{name}" are both three segments
// with literal text in each.
var trashRules = []classify.Rule{
	{
		ID: "trash.user.home", Match: "~/.Trash", Bucket: trash, Priority: 1,
		Category: "Trash", Owner: "Trash", Reclaim: tool,
		Explain: "files you deleted; Finder's Empty Trash frees them",
	},
	{
		ID: "trash.volume.root", Match: "/.Trashes", Bucket: trash, Priority: 1,
		Category: "Trash", Owner: "Trash", Reclaim: tool,
		Explain: "the volume's per-user trash directories",
	},
	{
		ID: "trash.volume.user", Match: "/.Trashes/{uid}", Bucket: trash,
		Category: "Trash", Owner: "Trash", Reclaim: tool, Priority: generic,
		Explain: "the trash of the account with uid {uid}",
	},
}
