package catalog

import "github.com/asamgx/storix/internal/classify"

// cacheRules cover ~/Library/Caches and ~/Library/Logs, where a directory name
// is usually a bundle id and therefore already the owner. Developer tools that
// also write here are named in developer.go, where the literal name makes the
// rule more specific than the catch-all below.
//
// Apple's own caches are tagged system rather than regenerable: they are
// rebuilt by the system on its own schedule, and telling a user that several
// gigabytes of com.apple.* caches are theirs to delete is how a disk tool
// earns a support ticket.
var cacheRules = []classify.Rule{
	{
		ID: "cache.user.root", Match: "~/Library/Caches", Bucket: appData,
		Category: "Caches", Owner: "Caches", Reclaim: regen,
		Explain: "per-user application caches; every owner rebuilds its own",
	},
	{
		ID: "cache.user.bundle", Match: "~/Library/Caches/{bundleid}", Bucket: appData,
		Category: "Cache", Owner: "{bundleid}", OwnerKeys: []string{"app:{bundleid}"},
		Reclaim: regen, Priority: generic,
		Explain: "cache written by {bundleid}; the application refills it",
	},
	{
		ID: "cache.apple.bundle", Match: "~/Library/Caches/com.apple.{name}", Bucket: appData,
		Category: "Apple caches", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: system,
		Explain: "a cache macOS keeps for com.apple.{name}; the system manages its size",
	},
	{
		ID: "cache.updater.shipit", Match: "~/Library/Caches/{bundleid}.ShipIt", Bucket: appData,
		Category: "Updater cache", Owner: "{bundleid}", OwnerKeys: []string{"app:{bundleid}"},
		Reclaim: regen,
		Explain: "Squirrel.Mac update downloads for {bundleid}; refetched at the next update",
	},
	{
		ID: "cache.updater.suffix", Match: "~/Library/Caches/{product}-updater", Bucket: appData,
		Category: "Updater cache", Owner: "{product}", OwnerKeys: []string{"product:{product}"},
		Reclaim: regen,
		Explain: "Electron updater downloads for {product}; refetched at the next update",
	},
	{
		ID: "cache.user.logs", Match: "~/Library/Logs", Bucket: appData,
		Category: "Logs", Owner: "Logs", Reclaim: regen,
		Explain: "per-user application logs",
	},
	{
		ID: "cache.user.log-owner", Match: "~/Library/Logs/{name}", Bucket: appData,
		Category: "Logs", Owner: "{name}", OwnerKeys: []string{"app:{name}"},
		Reclaim: regen, Priority: generic,
		Explain: "logs written by {name}",
	},
	{
		ID: "cache.user.diagnostics", Match: "~/Library/Logs/DiagnosticReports", Bucket: appData,
		Category: "Diagnostics", Owner: "macOS", OwnerKeys: []string{macosKey}, Reclaim: regen,
		Explain: "crash reports; safe to clear once they have been read",
	},

	{
		ID: "cache.user.httpstorages", Match: "~/Library/HTTPStorages", Bucket: appData,
		Category: "Network cache", Owner: "HTTP storages", Reclaim: regen,
		Explain: "per-application URL caches and cookies storage",
	},
	{
		ID: "cache.user.httpstorage-owner", Match: "~/Library/HTTPStorages/{bundleid}", Bucket: appData,
		Category: "Network cache", Owner: "{bundleid}", OwnerKeys: []string{"app:{bundleid}"},
		Reclaim: regen, Priority: generic,
		Explain: "the URL cache of {bundleid}",
	},
	{
		ID: "cache.user.webkit", Match: "~/Library/WebKit", Bucket: appData,
		Category: "Web content", Owner: "WebKit", Reclaim: regen,
		Explain: "WebKit's per-application storage",
	},
	{
		ID: "cache.user.webkit-owner", Match: "~/Library/WebKit/{bundleid}", Bucket: appData,
		Category: "Web content", Owner: "{bundleid}", OwnerKeys: []string{"app:{bundleid}"},
		Reclaim: regen, Priority: generic,
		Explain: "WebKit storage for {bundleid}",
	},
	{
		ID: "cache.user.savedstate", Match: "~/Library/Saved Application State", Bucket: appData,
		Category: "Saved state", Owner: "Saved state", Reclaim: regen,
		Explain: "window state restored when an application reopens",
	},
	{
		ID: "cache.user.savedstate-owner", Match: "~/Library/Saved Application State/{bundleid}.savedState", Bucket: appData,
		Category: "Saved state", Owner: "{bundleid}", OwnerKeys: []string{"app:{bundleid}"}, Reclaim: regen,
		Explain: "saved window state for {bundleid}",
	},
}
