package catalog

import "github.com/asamgx/storix/internal/classify"

// appDataRules cover the Library trees where data belongs to an application.
//
// Almost every rule here captures the directory name as the owner and tags the
// bytes Unknown rather than guessing. That is deliberate: a name is not an
// owner until something has confirmed the application exists, and confirming
// it is the apps detector's job (Part B). Until then the bytes are correctly
// bucketed and honestly labelled, which is the whole point of having a floor
// under the detectors.
var appDataRules = []classify.Rule{
	{
		ID: "appdata.library.root", Match: "~/Library", Bucket: appData,
		Category: "Library", Owner: "Library", Reclaim: unsure, Priority: generic,
		Explain: "the user's Library: application data that is not a document",
	},
	{
		ID: "appdata.library.other", Match: "~/Library/{name}", Bucket: appData,
		Category: "Library", Owner: "{name}", Reclaim: unsure, Priority: generic,
		Explain: "{name} under the user's Library",
	},
	{
		ID: "appdata.library.apple", Match: "~/Library/com.apple.{name}", Bucket: appData,
		Category: "Apple app data", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "data macOS keeps for com.apple.{name}",
	},

	{
		ID: "appdata.containers.root", Match: "~/Library/Containers", Bucket: appData,
		Category: "Containers", Owner: "Containers", Reclaim: unsure,
		Explain: "sandboxed applications' private containers",
	},
	{
		ID: "appdata.containers.bundle", Match: "~/Library/Containers/{bundleid}", Bucket: appData,
		Category: "Container", Owner: "{bundleid}", OwnerKeys: []string{"app:{bundleid}"},
		Reclaim: unsure, Priority: generic,
		Explain: "the sandbox container of {bundleid}",
	},
	{
		ID: "appdata.daemon-containers", Match: "~/Library/Daemon Containers", Bucket: appData,
		Category: "Containers", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "containers for system daemons running as the user",
	},
	{
		ID: "appdata.groups.root", Match: "~/Library/Group Containers", Bucket: appData,
		Category: "Group containers", Owner: "Group containers", Reclaim: unsure,
		Explain: "containers shared between an application and its helpers",
	},
	{
		ID: "appdata.groups.name", Match: "~/Library/Group Containers/{name}", Bucket: appData,
		Category: "Group container", Owner: "{name}", OwnerKeys: []string{"app:{name}"},
		Reclaim: unsure, Priority: generic,
		Explain: "the group container {name}, shared by one vendor's applications",
	},
	{
		ID: "appdata.groups.apple", Match: "~/Library/Group Containers/group.com.apple.{name}", Bucket: appData,
		Category: "Apple app data", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "a group container macOS owns",
	},

	{
		ID: "appdata.appsupport.root", Match: "~/Library/Application Support", Bucket: appData,
		Category: "Application support", Owner: "Application support", Reclaim: unsure,
		Explain: "where unsandboxed applications keep their data",
	},
	{
		ID: "appdata.appsupport.name", Match: "~/Library/Application Support/{name}", Bucket: appData,
		Category: "Application support", Owner: "{name}", OwnerKeys: []string{"app:{name}"},
		Reclaim: unsure, Priority: generic,
		Explain: "application data kept by {name}",
	},
	{
		ID: "appdata.appsupport.apple", Match: "~/Library/Application Support/com.apple.{name}", Bucket: appData,
		Category: "Apple app data", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "application data macOS keeps for com.apple.{name}",
	},
	{
		ID: "appdata.appscripts", Match: "~/Library/Application Scripts/{bundleid}", Bucket: appData,
		Category: "Application scripts", Owner: "{bundleid}", OwnerKeys: []string{"app:{bundleid}"},
		Reclaim: unsure, Priority: generic,
		Explain: "sandbox extension scripts for {bundleid}",
	},
	{
		ID: "appdata.preferences", Match: "~/Library/Preferences", Bucket: appData,
		Category: "Preferences", Owner: "Preferences", Reclaim: user,
		Explain: "per-application settings; tiny, and deleting them resets applications",
	},
	{
		ID: "appdata.cookies", Match: "~/Library/Cookies", Bucket: appData,
		Category: "Cookies", Owner: "Cookies", Reclaim: user,
		Explain: "cookie stores; deleting them signs you out",
	},
	{
		ID: "appdata.keychains", Match: "~/Library/Keychains", Bucket: appData,
		Category: "Keychains", Owner: "Keychains", Reclaim: user,
		Explain: "the login keychain; never delete it",
	},
	{
		ID: "appdata.launchagents", Match: "~/Library/LaunchAgents", Bucket: appData,
		Category: "Launch agents", Owner: "Launch agents", Reclaim: unsure,
		Explain: "per-user launchd jobs; the evidence that an application is still installed",
	},
	{
		ID: "appdata.autosave", Match: "~/Library/Autosave Information", Bucket: appData,
		Category: "Autosave", Owner: "Autosave", Reclaim: user,
		Explain: "unsaved document state",
	},
	{
		ID: "appdata.services", Match: "~/Library/Services", Bucket: appData,
		Category: "Services", Owner: "Services", Reclaim: unsure,
		Explain: "per-user Services menu extensions",
	},

	{
		ID: "appdata.shared.appsupport", Match: "/Library/Application Support", Bucket: appData,
		Category: "Application support", Owner: "Application support", Reclaim: unsure,
		Explain: "machine-wide application data",
	},
	{
		ID: "appdata.shared.appsupport-name", Match: "/Library/Application Support/{name}", Bucket: appData,
		Category: "Application support", Owner: "{name}", OwnerKeys: []string{"app:{name}"},
		Reclaim: unsure, Priority: generic,
		Explain: "machine-wide application data kept by {name}",
	},
	{
		ID: "appdata.shared.helpers", Match: "/Library/PrivilegedHelperTools", Bucket: appData,
		Category: "Privileged helpers", Owner: "Privileged helpers", Reclaim: unsure,
		Explain: "helper binaries installed with root privileges",
	},
	{
		ID: "appdata.shared.launchagents", Match: "/Library/LaunchAgents", Bucket: appData,
		Category: "Launch agents", Owner: "Launch agents", Reclaim: unsure,
		Explain: "machine-wide launchd agents",
	},
	{
		ID: "appdata.shared.launchdaemons", Match: "/Library/LaunchDaemons", Bucket: appData,
		Category: "Launch daemons", Owner: "Launch daemons", Reclaim: unsure,
		Explain: "machine-wide launchd daemons",
	},
	{
		ID: "appdata.shared.preferences", Match: "/Library/Preferences", Bucket: appData,
		Category: "Preferences", Owner: "Preferences", Reclaim: user,
		Explain: "machine-wide settings",
	},
	{
		ID: "appdata.receipts", Match: "/private/var/db/receipts", Bucket: appData,
		Category: "Package receipts", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "installer receipts; tiny, and the evidence orphan detection reads",
	},
}
