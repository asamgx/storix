package catalog

import "github.com/asamgx/storix/internal/classify"

// personalRules cover the files that are the reason the machine exists. They
// are all user-data and none of them is ever reclaimable, whatever their size:
// a disk tool that offers to delete someone's Photos library is worse than no
// disk tool at all.
var personalRules = []classify.Rule{
	{
		ID: "personal.users.root", Match: "/Users", Bucket: personal,
		Category: "Home folders", Owner: "Home folders", Reclaim: user,
		Explain: "the home folders of everyone with an account on this machine",
	},
	{
		ID: "personal.home.root", Match: "~", Bucket: personal,
		Category: "Home", Owner: "Home", Reclaim: user,
		Explain: "the user's home folder",
	},
	{
		ID: "personal.home.other", Match: "~/{name}", Bucket: personal,
		Category: "Home folder", Owner: "{name}", Reclaim: user, Priority: generic,
		Explain: "{name} in the home folder",
	},
	{
		// /Users/Shared is not an account's home, so the engine keeps the
		// "~" rules off it (see notHomes) and this rule answers for the
		// whole folder. Whatever an application dropped there is one of
		// several accounts' data and nobody's cache to clear.
		ID: "personal.shared", Match: "/Users/Shared", Bucket: personal,
		Category: "Shared", Owner: "Shared folder", Reclaim: user,
		Explain: "files shared between the accounts on this machine",
	},

	{
		ID: "personal.documents", Match: "~/Documents", Bucket: personal,
		Category: "Documents", Owner: "Documents", Reclaim: user,
		Explain: "your documents",
	},
	{
		ID: "personal.desktop", Match: "~/Desktop", Bucket: personal,
		Category: "Desktop", Owner: "Desktop", Reclaim: user,
		Explain: "your desktop",
	},
	{
		ID: "personal.downloads", Match: "~/Downloads", Bucket: personal,
		Category: "Downloads", Owner: "Downloads", Reclaim: user,
		Explain: "downloads: yours to keep, and often the quickest win on a full disk",
	},
	{
		ID: "personal.pictures", Match: "~/Pictures", Bucket: personal,
		Category: "Pictures", Owner: "Pictures", Reclaim: user,
		Explain: "photos and the Photos library",
	},
	{
		ID: "personal.movies", Match: "~/Movies", Bucket: personal,
		Category: "Movies", Owner: "Movies", Reclaim: user,
		Explain: "videos and video libraries",
	},
	{
		ID: "personal.music", Match: "~/Music", Bucket: personal,
		Category: "Music", Owner: "Music", Reclaim: user,
		Explain: "music and the Music library",
	},
	{
		ID: "personal.public", Match: "~/Public", Bucket: personal,
		Category: "Public", Owner: "Public", Reclaim: user,
		Explain: "the folder other accounts can read",
	},
	{
		ID: "personal.sites", Match: "~/Sites", Bucket: personal,
		Category: "Sites", Owner: "Sites", Reclaim: user,
		Explain: "the personal web folder",
	},

	{
		ID: "personal.mail", Match: "~/Library/Mail", Bucket: personal,
		Category: "Mail", Owner: "Mail", Reclaim: user,
		Explain: "the local mail store; reading it needs Full Disk Access",
	},
	{
		ID: "personal.messages", Match: "~/Library/Messages", Bucket: personal,
		Category: "Messages", Owner: "Messages", Reclaim: user,
		Explain: "the Messages database and its attachments",
	},
	{
		ID: "personal.icloud", Match: "~/Library/Mobile Documents", Bucket: personal,
		Category: "iCloud Drive", Owner: "iCloud Drive", Reclaim: user,
		Explain: "iCloud Drive; evicted files are in the cloud and take no local space",
	},
	{
		ID: "personal.cloudstorage", Match: "~/Library/CloudStorage", Bucket: personal,
		Category: "Cloud storage", Owner: "Cloud storage", Reclaim: user,
		Explain: "Dropbox, Google Drive and OneDrive through the File Provider",
	},
	{
		ID: "personal.cloudstorage-provider", Match: "~/Library/CloudStorage/{name}", Bucket: personal,
		Category: "Cloud storage", Owner: "{name}", Reclaim: user, Priority: generic,
		Explain: "synced files from {name}",
	},
	{
		ID: "personal.fonts", Match: "~/Library/Fonts", Bucket: personal,
		Category: "Fonts", Owner: "Fonts", Reclaim: user,
		Explain: "fonts you installed",
	},
	{
		ID: "personal.safari", Match: "~/Library/Safari", Bucket: personal,
		Category: "Safari", Owner: "Safari", OwnerKeys: []string{macosKey}, Reclaim: user,
		Explain: "Safari history, bookmarks and reading list",
	},
	{
		ID: "personal.photos-cache", Match: "~/Library/Containers/com.apple.{name}", Bucket: personal,
		Category: "Apple app data", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: user,
		Explain: "data an Apple application keeps for you: Books, Podcasts, TV downloads",
	},
}
