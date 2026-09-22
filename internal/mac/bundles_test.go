package mac

import "testing"

func TestIsBundleName(t *testing.T) {
	bundles := []string{
		"Safari.app", "Foundation.framework", "Photos.photoslibrary",
		"Music.musiclibrary", "TV.tvlibrary", "storix.xcodeproj",
		"storix.xcworkspace", "Try.playground", "vm.pvm", "linux.utm",
		"win.vmwarevm", "backup.sparsebundle", "disk.sparseimage",
		"installer.dmg", "update.pkg", "com.apple.Safari.savedState",
		"file.download", "Share.appex", "Preview.qlgenerator", "driver.kext",
		"Some.bundle", "Audio.plugin", "Network.prefPane", "en.lproj",
		// Case does not matter.
		"SAFARI.APP", "Window.SavedState", "Sound.prefpane",
	}
	for _, name := range bundles {
		if !IsBundleName(name) {
			t.Errorf("IsBundleName(%q) = false, want true", name)
		}
	}
	plain := []string{
		"Library", "Caches", "node_modules", "file.txt", "archive.zip",
		"photo.jpeg", "app", ".app", ".lproj", "", ".gitignore",
		"Application Support", "x.appx", "y.apps",
	}
	for _, name := range plain {
		if IsBundleName(name) {
			t.Errorf("IsBundleName(%q) = true, want false", name)
		}
	}
}

func TestIsBundleNameTakesBaseName(t *testing.T) {
	if !IsBundleName("/Applications/Safari.app") {
		t.Error("a full path to a bundle should be recognised")
	}
	if IsBundleName("/Applications/Safari.app/Contents") {
		t.Error("a directory inside a bundle is not itself a bundle")
	}
}
