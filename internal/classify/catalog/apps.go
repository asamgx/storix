package catalog

import "github.com/asamgx/storix/internal/classify"

// appRules put application bundles in bucket 2 wherever they are installed.
//
// A bundle is a directory macOS presents as one object, so one claim on the
// bundle covers everything inside it. The owner captured here is the bundle's
// file name, not its identifier: reading Info.plist is the apps detector's
// job (Part B), and it replaces these owners with real bundle ids at a higher
// precedence. The Caskroom rules are placeholders in the same sense — the
// apps detector reads each cask's install receipt and knows which application
// the token actually installed.
var appRules = []classify.Rule{
	{
		ID: "apps.system.root", Match: "/Applications", Bucket: apps,
		Category: "Applications", Owner: "Applications", Reclaim: user,
		Explain: "applications installed for everyone on this machine",
	},
	{
		ID: "apps.system.bundle", Match: "/Applications/{name}.app", Bucket: apps,
		Category: "Application", Owner: "{name}", OwnerKeys: []string{"product:{name}"}, Reclaim: user,
		Explain: "the application {name}; deleting the bundle leaves its data behind",
	},
	{
		ID: "apps.system.vendor", Match: "/Applications/{name}", Bucket: apps,
		Category: "Vendor folder", Owner: "{name}", OwnerKeys: []string{"product:{name}"},
		Reclaim: user, Priority: generic,
		Explain: "a vendor folder under /Applications holding {name}'s applications",
	},
	{
		ID: "apps.system.installer", Match: "/Applications/Install macOS {name}.app", Bucket: apps,
		Category: "macOS installer", Owner: "macOS installer", OwnerKeys: []string{macosKey},
		Reclaim: tool, Priority: 1,
		Explain: "a macOS installer, usually 12 GB and rarely needed after the upgrade",
	},
	{
		ID: "apps.system.utilities", Match: "/Applications/Utilities", Bucket: apps,
		Category: "Applications", Owner: "Utilities", OwnerKeys: []string{macosKey},
		Reclaim: user, Priority: 1,
		Explain: "the Utilities folder",
	},
	{
		ID: "apps.system.utility", Match: "/Applications/Utilities/{name}.app", Bucket: apps,
		Category: "Application", Owner: "{name}", OwnerKeys: []string{"product:{name}"}, Reclaim: user,
		Explain: "the utility {name}",
	},
	{
		ID: "apps.setapp.root", Match: "/Applications/Setapp", Bucket: apps,
		Category: "Setapp", Owner: "Setapp", OwnerKeys: []string{"app:com.setapp.DesktopClient"},
		Reclaim: user, Priority: 1,
		Explain: "applications Setapp installed; Setapp manages them",
	},
	{
		ID: "apps.setapp.bundle", Match: "/Applications/Setapp/{name}.app", Bucket: apps,
		Category: "Setapp", Owner: "{name}", OwnerKeys: []string{"product:{name}"}, Reclaim: user,
		Explain: "{name}, installed through Setapp",
	},
	{
		ID: "apps.user.root", Match: "~/Applications", Bucket: apps,
		Category: "Applications", Owner: "Applications", Reclaim: user, Priority: 1,
		Explain: "applications installed for this user only",
	},
	{
		ID: "apps.user.bundle", Match: "~/Applications/{name}.app", Bucket: apps,
		Category: "Application", Owner: "{name}", OwnerKeys: []string{"product:{name}"}, Reclaim: user,
		Explain: "the application {name}, installed for this user only",
	},
	{
		ID: "apps.cask.root", Match: "/opt/homebrew/Caskroom", Bucket: apps,
		Category: "Homebrew cask", Owner: "Homebrew casks", OwnerKeys: []string{"cli:brew"}, Reclaim: user,
		Explain: "applications Homebrew installed as casks",
	},
	{
		ID: "apps.cask.token", Match: "/opt/homebrew/Caskroom/{cask}", Bucket: apps,
		Category: "Homebrew cask", Owner: "{cask}", OwnerKeys: []string{"cask:{cask}"},
		Reclaim: user, Priority: generic,
		Explain: "the cask {cask}; `brew uninstall --cask {cask}` removes it",
	},
	{
		ID: "apps.cask.intel-root", Match: "/usr/local/Caskroom", Bucket: apps,
		Category: "Homebrew cask", Owner: "Homebrew casks", OwnerKeys: []string{"cli:brew"}, Reclaim: user,
		Explain: "casks installed by the Intel Homebrew",
	},
	{
		ID: "apps.cask.intel-token", Match: "/usr/local/Caskroom/{cask}", Bucket: apps,
		Category: "Homebrew cask", Owner: "{cask}", OwnerKeys: []string{"cask:{cask}"},
		Reclaim: user, Priority: generic,
		Explain: "the Intel cask {cask}",
	},
}
