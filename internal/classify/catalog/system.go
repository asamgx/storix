package catalog

import "github.com/asamgx/storix/internal/classify"

// macosKey is the owner key every rule uses for bytes macOS owns itself, so
// the footprint view can total them the way it totals an application's.
const macosKey = "macos"

// systemRules cover the parts of the data volume macOS owns: the per-user
// cache and temp trees, the logs and databases under /private/var, the system
// support files under /Library, and the metadata directories at the volume
// root. Most of it needs sudo to read, so these rules are also what keeps an
// unprivileged scan's Other bucket honest: the directories are named even
// when their contents were denied.
var systemRules = []classify.Rule{
	{
		ID: "sys.var.root", Match: "/private/var", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system, Priority: generic,
		Explain: "the system's own variable data; most of it needs sudo to read",
	},
	{
		ID: "sys.var.other", Match: "/private/var/{name}", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system, Priority: generic,
		Explain: "system state under /private/var/{name}",
	},
	{
		ID: "sys.var.folders", Match: "/private/var/folders", Bucket: syscaches,
		Category: "Per-user cache and temp", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system,
		Explain: "the per-user darwin cache and temp trees; the system prunes them",
	},
	{
		ID: "sys.var.folders-cache", Match: "/private/var/folders/*/*/C", Bucket: syscaches,
		Category: "Per-user cache", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "DARWIN_USER_CACHE_DIR: rebuilt on demand by whoever wrote it",
	},
	{
		ID: "sys.var.folders-temp", Match: "/private/var/folders/*/*/T", Bucket: syscaches,
		Category: "Per-user temp", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "DARWIN_USER_TEMP_DIR: temporary files the system clears on reboot",
	},
	{
		ID: "sys.var.log", Match: "/private/var/log", Bucket: syscaches,
		Category: "System logs", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "system logs, rotated by newsyslog",
	},
	{
		ID: "sys.var.db", Match: "/private/var/db", Bucket: syscaches,
		Category: "System databases", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "system databases: launch services, TCC, software update, kernel caches",
	},
	{
		ID: "sys.var.diagnostics", Match: "/private/var/db/diagnostics", Bucket: syscaches,
		Category: "Diagnostics", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "the unified log store; the system caps and rotates it",
	},
	{
		ID: "sys.var.diagnostic-pipeline", Match: "/private/var/db/DiagnosticPipeline", Bucket: syscaches,
		Category: "Diagnostics", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "queued diagnostic submissions",
	},
	{
		ID: "sys.var.dyld", Match: "/private/var/db/dyld", Bucket: syscaches,
		Category: "Shared caches", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "the dyld shared cache; the system rebuilds it after updates",
	},
	{
		ID: "sys.var.softwareupdate", Match: "/private/var/db/softwareupdate", Bucket: syscaches,
		Category: "Software update", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: tool,
		Explain: "downloaded system update payloads; Software Update clears them",
	},
	{
		ID: "sys.var.vm", Match: "/private/var/vm", Bucket: syscaches,
		Category: "Swap", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "swap files; the kernel owns their size",
	},
	{
		ID: "sys.var.tmp", Match: "/private/var/tmp", Bucket: syscaches,
		Category: "Temporary files", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "temporary files surviving reboots; safe to clear when idle",
	},
	{
		ID: "sys.tmp", Match: "/private/tmp", Bucket: syscaches,
		Category: "Temporary files", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "/tmp: cleared on reboot",
	},
	{
		ID: "sys.etc", Match: "/private/etc", Bucket: syscaches,
		Category: "System configuration", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "system configuration files",
	},
	{
		ID: "sys.var.root-home", Match: "/private/var/root", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "root's home directory",
	},

	{
		ID: "sys.library.root", Match: "/Library", Bucket: syscaches,
		Category: "System support files", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system, Priority: generic,
		Explain: "machine-wide support files installed outside any user's home",
	},
	{
		ID: "sys.library.other", Match: "/Library/{name}", Bucket: syscaches,
		Category: "System support files", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system, Priority: generic,
		Explain: "machine-wide {name} support files",
	},
	{
		ID: "sys.library.caches", Match: "/Library/Caches", Bucket: syscaches,
		Category: "System caches", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "machine-wide caches; every owner rebuilds its own",
	},
	{
		ID: "sys.library.cache-owner", Match: "/Library/Caches/{name}", Bucket: syscaches,
		Category: "System caches", Owner: "{name}", Reclaim: regen, Priority: generic,
		Explain: "machine-wide cache written by {name}",
	},
	{
		ID: "sys.library.logs", Match: "/Library/Logs", Bucket: syscaches,
		Category: "System logs", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "machine-wide logs",
	},
	{
		ID: "sys.library.updates", Match: "/Library/Updates", Bucket: syscaches,
		Category: "Software update", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: tool,
		Explain: "macOS update leftovers; Software Update removes them",
	},
	{
		ID: "sys.library.apple", Match: "/Library/Apple", Bucket: syscaches,
		Category: "System support files", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "Apple-installed system components and receipts",
	},
	{
		ID: "sys.library.fonts", Match: "/Library/Fonts", Bucket: syscaches,
		Category: "Fonts", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "machine-wide fonts",
	},
	{
		ID: "sys.library.keychains", Match: "/Library/Keychains", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "machine keychains",
	},

	{
		ID: "sys.systemlib.root", Match: "/System/Library", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system, Priority: generic,
		Explain: "the writable part of the system library that lives on the data volume",
	},
	{
		ID: "sys.systemlib.assets", Match: "/System/Library/AssetsV2", Bucket: syscaches,
		Category: "System assets", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "downloaded system assets: voices, dictionaries, models",
	},
	{
		ID: "sys.systemlib.assets-v1", Match: "/System/Library/Assets", Bucket: syscaches,
		Category: "System assets", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "downloaded system assets (older layout)",
	},
	{
		ID: "sys.systemlib.caches", Match: "/System/Library/Caches", Bucket: syscaches,
		Category: "System caches", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "system-level caches on the data volume",
	},

	{
		ID: "sys.usr.root", Match: "/usr", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system, Priority: generic,
		Explain: "the writable part of /usr on the data volume",
	},
	{
		ID: "sys.usr.other", Match: "/usr/{name}", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey},
		Reclaim: system, Priority: generic,
		Explain: "/usr/{name} on the data volume",
	},
	{
		ID: "sys.opt.root", Match: "/opt", Bucket: developer,
		Category: "Optional software", Owner: "Optional software",
		Reclaim: unsure, Priority: generic,
		Explain: "software installed outside the system's own directories",
	},
	{
		ID: "sys.opt.other", Match: "/opt/{name}", Bucket: developer,
		Category: "Optional software", Owner: "{name}", OwnerKeys: []string{"cli:{name}"},
		Reclaim: unsure, Priority: generic,
		Explain: "{name} installed under /opt",
	},

	{
		ID: "sys.meta.spotlight", Match: "/.Spotlight-V100", Bucket: syscaches,
		Category: "Spotlight index", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "the Spotlight index; mdutil rebuilds it, slowly",
	},
	{
		ID: "sys.meta.fseventsd", Match: "/.fseventsd", Bucket: syscaches,
		Category: "File system events", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "the file system event log Time Machine and Spotlight read",
	},
	{
		ID: "sys.meta.revisions", Match: "/.DocumentRevisions-V100", Bucket: syscaches,
		Category: "Document revisions", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "version history for documents; deleting it loses Revert To versions",
	},
	{
		ID: "sys.meta.temporary", Match: "/.TemporaryItems", Bucket: syscaches,
		Category: "Temporary files", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "temporary items left by applications",
	},
	{
		ID: "sys.meta.pkinstall", Match: "/.PKInstallSandboxManager*", Bucket: syscaches,
		Category: "Software update", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: tool,
		Explain: "installer sandboxes left by system updates",
	},
	{
		ID: "sys.meta.previous-system", Match: "/.PreviousSystemInformation", Bucket: syscaches,
		Category: "Software update", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "what the last macOS upgrade left behind about the system it replaced",
	},
	{
		ID: "sys.meta.mobile-update", Match: "/MobileSoftwareUpdate", Bucket: syscaches,
		Category: "Software update", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: tool,
		Explain: "staged update payloads; Software Update clears them",
	},
	{
		ID: "sys.meta.cores", Match: "/cores", Bucket: syscaches,
		Category: "Diagnostics", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "kernel and process core dumps",
	},
	{
		ID: "sys.meta.home", Match: "/home", Bucket: syscaches,
		Category: "System data", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "the autofs home map; a mount point the walk does not enter",
	},
	{
		ID: "sys.meta.vm", Match: "/vm", Bucket: syscaches,
		Category: "Swap", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "the swap mount point",
	},
}
