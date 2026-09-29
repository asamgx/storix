package catalog

import "github.com/asamgx/storix/internal/classify"

// backupRules cover backups of things that are not on this disk: phones,
// and builds that were archived rather than kept in a project. They are all
// user-data: a backup is worth exactly as much as the thing it backs up.
var backupRules = []classify.Rule{
	{
		ID: "bak.mobilesync.root", Match: "~/Library/Application Support/MobileSync", Bucket: backups,
		Category: "Device backups", Owner: "MobileSync", OwnerKeys: []string{macosKey}, Reclaim: user,
		Explain: "backups of iOS and iPadOS devices; reading them needs Full Disk Access",
	},
	{
		ID: "bak.mobilesync.backup", Match: "~/Library/Application Support/MobileSync/Backup", Bucket: backups,
		Category: "Device backups", Owner: "MobileSync", OwnerKeys: []string{macosKey}, Reclaim: user,
		Explain: "one directory per backed-up device",
	},
	{
		ID: "bak.mobilesync.device", Match: "~/Library/Application Support/MobileSync/Backup/{udid}", Bucket: backups,
		Category: "Device backup", Owner: "{udid}", Reclaim: user, Priority: generic,
		Explain: "the backup of device {udid}; Finder can delete it",
	},
	{
		ID: "bak.xcode.archives", Match: "~/Library/Developer/Xcode/Archives", Bucket: backups,
		Category: "Build archives", Owner: "Xcode", OwnerKeys: []string{"app:com.apple.dt.Xcode"}, Reclaim: user,
		Explain: "archived builds with their debug symbols; needed to symbolicate old crashes",
	},
	{
		ID: "bak.timemachine.local", Match: "/Volumes/com.apple.TimeMachine.localsnapshots", Bucket: backups,
		Category: "Time Machine", Owner: "Time Machine", OwnerKeys: []string{macosKey}, Reclaim: tool,
		Explain: "local Time Machine snapshots; `tmutil thinlocalsnapshots` reclaims them",
	},
}
