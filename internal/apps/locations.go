package apps

import (
	"regexp"
	"strings"

	"github.com/asamgx/storix/internal/classify"
)

// KeyKind is what the name of a direct child of a location means.
type KeyKind uint8

const (
	// KeyBundleID is a directory named after a bundle identifier.
	KeyBundleID KeyKind = iota + 1
	// KeyNameOrID is a directory that may be either an identifier or a
	// display name: Application Support holds both "Code" and "notion.id".
	KeyNameOrID
	// KeyGroupContainer is a group container, possibly team-id prefixed.
	KeyGroupContainer
	// KeyPlist is a preferences file named "<id>.plist".
	KeyPlist
	// KeyByHostPlist is "<id>.<UUID>.plist" under Preferences/ByHost.
	KeyByHostPlist
	// KeySavedState is "<id>.savedState".
	KeySavedState
	// KeyCookies is "<id>.binarycookies".
	KeyCookies
	// KeyLaunchItem is "<label>.plist" in a LaunchAgents directory.
	KeyLaunchItem
	// KeyReceipt is "<pkgid>.plist" or "<pkgid>.bom" under the receipts dir.
	KeyReceipt
	// KeyHelperTool is a privileged helper named after its job label.
	KeyHelperTool
)

// Location is one directory whose children belong to applications. The table
// is docs/04's list of application data locations, typed so that the category
// and the default reclaimability travel with the path rather than being
// re-decided at each use.
type Location struct {
	// Dir is the display path, with "~" standing for the scan user's home.
	Dir string
	// Key says how to read a child's name.
	Key KeyKind
	// Category is the sub-heading the report groups the bytes under.
	Category string
	// Reclaim is the default tag for an installed owner. A cache is
	// regenerable whoever owns it; a container is the user's data.
	Reclaim classify.Reclaim
	// Depth is how far below Dir the candidates are. It is 1 everywhere
	// except a vendor directory, whose children are the real candidates.
	Depth int
}

// Locations is the table of application data directories.
var Locations = []Location{
	{Dir: "~/Library/Containers", Key: KeyBundleID, Category: "Container", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/Group Containers", Key: KeyGroupContainer, Category: "Group container", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/Application Support", Key: KeyNameOrID, Category: "Application Support", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/Caches", Key: KeyNameOrID, Category: "Cache", Reclaim: classify.Regenerable, Depth: 1},
	{Dir: "~/Library/Preferences", Key: KeyPlist, Category: "Preferences", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/Preferences/ByHost", Key: KeyByHostPlist, Category: "Preferences", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/Saved Application State", Key: KeySavedState, Category: "Saved state", Reclaim: classify.Regenerable, Depth: 1},
	{Dir: "~/Library/HTTPStorages", Key: KeyBundleID, Category: "HTTP storage", Reclaim: classify.Regenerable, Depth: 1},
	{Dir: "~/Library/WebKit", Key: KeyBundleID, Category: "WebKit", Reclaim: classify.Regenerable, Depth: 1},
	{Dir: "~/Library/Cookies", Key: KeyCookies, Category: "Cookies", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/Logs", Key: KeyNameOrID, Category: "Logs", Reclaim: classify.Regenerable, Depth: 1},
	{Dir: "~/Library/Application Scripts", Key: KeyBundleID, Category: "Application Scripts", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/LaunchAgents", Key: KeyLaunchItem, Category: "Launch item", Reclaim: classify.UserData, Depth: 1},
	{Dir: "~/Library/Google", Key: KeyNameOrID, Category: "Application Support", Reclaim: classify.Regenerable, Depth: 1},
	{Dir: "/Library/LaunchAgents", Key: KeyLaunchItem, Category: "Launch item", Reclaim: classify.UserData, Depth: 1},
	{Dir: "/Library/LaunchDaemons", Key: KeyLaunchItem, Category: "Launch item", Reclaim: classify.UserData, Depth: 1},
	{Dir: "/Library/Application Support", Key: KeyNameOrID, Category: "Support (system)", Reclaim: classify.UserData, Depth: 1},
	{Dir: "/Library/PrivilegedHelperTools", Key: KeyHelperTool, Category: "Privileged helper", Reclaim: classify.UserData, Depth: 1},
	{Dir: "/private/var/db/receipts", Key: KeyReceipt, Category: "Receipt", Reclaim: classify.System, Depth: 1},
}

// cacheLikeCategories are the categories whose bytes a footprint counts as
// caches rather than data: regenerable whoever owns them.
var cacheLikeCategories = map[string]bool{
	"Cache":         true,
	"HTTP storage":  true,
	"WebKit":        true,
	"Saved state":   true,
	"Logs":          true,
	"Updater cache": true,
}

// CacheLike reports whether a category holds regenerable bytes.
func CacheLike(category string) bool { return cacheLikeCategories[category] }

// byHostSuffix is the "<id>.<UUID>.plist" form of a ByHost preference file.
var byHostSuffix = regexp.MustCompile(`\.[0-9A-Fa-f-]{8,}\.plist$`)

// NormalizeName turns the on-disk name of a direct child of a location into
// the identifier or display name the resolution order matches on.
func NormalizeName(k KeyKind, name string) string {
	switch k {
	case KeyByHostPlist:
		if loc := byHostSuffix.FindStringIndex(name); loc != nil {
			return name[:loc[0]]
		}
		return strings.TrimSuffix(name, ".plist")
	case KeyPlist, KeyLaunchItem, KeyHelperTool:
		return strings.TrimSuffix(name, ".plist")
	case KeyReceipt:
		name = strings.TrimSuffix(name, ".bom")
		return strings.TrimSuffix(name, ".plist")
	case KeySavedState:
		return strings.TrimSuffix(name, ".savedState")
	case KeyCookies:
		return strings.TrimSuffix(name, ".binarycookies")
	default:
		return name
	}
}

// appleNames are directory names macOS owns that are not reverse-DNS, so the
// com.apple test does not catch them. They belong to the rule catalog: the
// apps detector emitting a claim on them would mean attributing macOS's own
// data to an application, and failing to find one would mean an orphan.
var appleNames = map[string]bool{
	"apple":                   true,
	"addressbook":             true,
	"app store":               true,
	"clouddocs":               true,
	"crashreporter":           true,
	"garageband":              true,
	"ilifemediabrowser":       true,
	"knowledge":               true,
	"logic":                   true,
	"mobilesync":              true,
	"proapps":                 true,
	"script editor":           true,
	"siritodayviewextension":  true,
	"syncservices":            true,
	"callhistorydb":           true,
	"callhistorytransactions": true,
	"icloud":                  true,
	"mobile documents":        true,
	".globalpreferences":      true,
	".globalpreferences_m":    true,
	"byhost":                  true,
	"cloudkit":                true,
	"animoji":                 true,
	"assistant":               true,
	"baseband":                true,
	"btserver":                true,
	"siri":                    true,
	"spotlight":               true,
}

// appleNamePrefixes are name families macOS owns.
var appleNamePrefixes = []string{
	"com.apple.", "group.com.apple.", "group.is.workflow.", "group.tvappservices.",
	"is.workflow.", "tvappservices.", "group.apple.",
}

// IsAppleName reports whether a candidate name belongs to macOS. Group
// containers that a team id prefixes are checked on their remainder, so
// "243LU875E5.groups.com.apple.podcasts" is recognised as Apple's.
func IsAppleName(name string) bool {
	s := name
	if _, rest, ok := TeamIDPrefix(s); ok {
		s = rest
	} else if i := strings.IndexByte(s, '.'); i > 0 && IsTeamID(s[:i]) {
		s = s[i+1:]
	}
	if appleNames[strings.ToLower(s)] {
		return true
	}
	if s == "com.apple" || appleGroupRemainder.MatchString(s) {
		return true
	}
	for _, p := range appleNamePrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// vendorDirs are directories under a location that hold one publisher's
// several products rather than one product's data. "Google" holds Chrome's
// updater and Android Studio's caches, so its children are the candidates and
// the directory itself is attributed to the vendor.
var vendorDirs = map[string]bool{
	"google":           true,
	"microsoft":        true,
	"autodesk":         true,
	"wondershare":      true,
	"jetbrains":        true,
	"adobe":            true,
	"mozilla":          true,
	"openai":           true,
	"paloaltonetworks": true,
	"smart code ltd":   true,
	"bravesoftware":    true,
}

// IsVendorDir reports whether a directory name is a publisher folder whose
// children should be resolved individually.
func IsVendorDir(name string) bool { return vendorDirs[strings.ToLower(name)] }

// updaterSuffixes are the shapes an application's self-updater directory
// takes. Resolving them is worth a rule of its own because the bytes are
// always regenerable and the owner is always the application before the
// suffix: "lens-desktop-updater" is Lens, "notion.id.ShipIt" is Notion.
var updaterSuffixes = []string{".ShipIt", "-updater", ".Updater", "-Updater", ".updater"}

// TrimUpdaterSuffix removes an updater suffix and reports whether one was
// there.
func TrimUpdaterSuffix(name string) (string, bool) {
	for _, s := range updaterSuffixes {
		if len(name) > len(s) && strings.HasSuffix(name, s) {
			return name[:len(name)-len(s)], true
		}
	}
	return name, false
}
