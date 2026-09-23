package jvm_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/detect/detecttest"
	"github.com/asamgx/storix/internal/detect/jvm"
)

const home = detecttest.Home

// rootedStat scopes the detector's filesystem questions to the fixture.
//
// Some of these directories are absolute — /Library/Java belongs to the
// machine, not to a user — and detecttest's environment stats the real
// filesystem. Without this, a test asserting "nothing is installed" passes or
// fails according to whether the developer running it has a JDK.
func rootedStat(f *detecttest.Fixture) func(string) (detect.FileInfo, error) {
	root := strings.TrimSuffix(f.Real, detecttest.Home)
	return func(p string) (detect.FileInfo, error) {
		if !strings.HasPrefix(p, root) {
			p = filepath.Join(root, p)
		}
		return detect.Stat(p)
	}
}

// fixtureEnv is detecttest's environment with the filesystem scoped.
func fixtureEnv(t *testing.T, f *detecttest.Fixture, fixture string) detect.Env {
	t.Helper()
	env := f.Env(t, fixture)
	env.Stat = rootedStat(f)
	return env
}

// corpus is a machine with the full JVM and Android toolchain, which this one
// does not have: only /Library/Java, ~/.android and the two caches exist here.
func corpus() map[string]int64 {
	return map[string]int64{
		home + "/.gradle/caches/modules-2/x":                     900_000,
		home + "/.gradle/wrapper/dists/gradle-8.10/x":            500_000,
		home + "/.gradle/daemon/8.10/daemon.log":                 80_000,
		home + "/.gradle/native/x":                               70_000,
		home + "/.m2/repository/org/apache/x":                    800_000,
		home + "/.sdkman/candidates/java/21.0.5-tem/x":           700_000,
		home + "/Library/Java/JavaVirtualMachines/tem-21/x":      600_000,
		"/Library/Java/JavaVirtualMachines/jdk-17.jdk/x":         400_000,
		home + "/Library/Android/sdk/system-images/android-35/x": 1_000_000,
		home + "/Library/Android/sdk/platforms/android-35/x":     300_000,
		home + "/Library/Android/sdk/build-tools/35.0.0/x":       200_000,
		home + "/Library/Android/sdk/ndk/27.0.0/x":               150_000,
		home + "/Library/Android/sdk/emulator/x":                 140_000,
		home + "/Library/Android/sdk/sources/android-35/x":       130_000,
		home + "/.android/avd/Pixel_8.avd/userdata.img":          120_000,
		home + "/Library/Caches/JNA/temp/x":                      90_000,
		home + "/Library/Caches/JetPackCache/x":                  60_000,
	}
}

// TestNothingInstalled: no Gradle, no Maven, no JDK is Missing.
func TestNothingInstalled(t *testing.T) {
	f := detecttest.Build(t, map[string]int64{home + "/Documents/note.txt": 10})
	facts, err := detecttest.Probe(t, jvm.New(), fixtureEnv(t, f, "testdata/missing.json"))
	if !errors.Is(err, detect.ErrMissing) {
		t.Fatalf("Probe error = %v, want ErrMissing", err)
	}
	claims, sum := jvm.New().Classify(f.Tree, facts, f.Context)
	if len(claims) != 0 || !sum.Empty() {
		t.Errorf("%d claims with no JVM toolchain", len(claims))
	}
}

// TestCachesAndToolchains is the split the detector exists for: Gradle's
// caches and Maven's repository come back from the network, the JDKs and the
// emulator images are installations.
func TestCachesAndToolchains(t *testing.T) {
	f := detecttest.Build(t, corpus())
	facts, err := detecttest.Probe(t, jvm.New(), fixtureEnv(t, f, "testdata/this-machine.json"))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	claims, sum := jvm.New().Classify(f.Tree, facts, f.Context)

	want := []struct {
		path    string
		owner   string
		reclaim classify.Reclaim
		key     string
	}{
		{home + "/.gradle", "Gradle", classify.ToolManaged, "cli:gradle"},
		{home + "/.gradle/caches", "Gradle", classify.Regenerable, "cli:gradle"},
		{home + "/.gradle/wrapper", "Gradle", classify.ToolManaged, "cli:gradle"},
		{home + "/.gradle/daemon", "Gradle", classify.Regenerable, "cli:gradle"},
		{home + "/.gradle/native", "Gradle", classify.Regenerable, "cli:gradle"},
		{home + "/.m2/repository", "Maven", classify.Regenerable, "cli:mvn"},
		{home + "/.sdkman", "SDKMAN", classify.ToolManaged, "cli:sdk"},
		{home + "/Library/Java/JavaVirtualMachines", "Java", classify.ToolManaged, "cli:java"},
		{"/Library/Java", "Java", classify.ToolManaged, "cli:java"},
		{home + "/Library/Android/sdk", "Android SDK", classify.ToolManaged, "app:com.google.android.studio"},
		{home + "/Library/Android/sdk/system-images", "Android SDK", classify.ToolManaged, "app:com.google.android.studio"},
		{home + "/Library/Android/sdk/sources", "Android SDK", classify.Regenerable, "app:com.google.android.studio"},
		{home + "/.android/avd", "Android", classify.ToolManaged, "app:com.google.android.studio"},
		{home + "/Library/Caches/JNA", "JNA", classify.Regenerable, "cli:java"},
		{home + "/Library/Caches/JetPackCache", "Android", classify.Regenerable, "app:com.google.android.studio"},
	}
	for _, w := range want {
		c, ok := detecttest.ClaimAt(claims, w.path)
		if !ok {
			t.Errorf("no claim at %s", w.path)
			continue
		}
		if c.Bucket != classify.BucketDeveloper {
			t.Errorf("%s bucket = %s, want developer", w.path, c.Bucket)
		}
		if c.Owner != w.owner {
			t.Errorf("%s owner = %q, want %q", w.path, c.Owner, w.owner)
		}
		if c.Reclaim != w.reclaim {
			t.Errorf("%s reclaim = %s, want %s", w.path, c.Reclaim, w.reclaim)
		}
		if !detecttest.HasKey(c, w.key) {
			t.Errorf("%s keys = %v, want %q", w.path, c.OwnerKeys, w.key)
		}
	}
	if _, ok := detecttest.Tool(sum, home+"/Library/Android/sdk/system-images"); !ok {
		t.Error("the system images have no summary row, and they are the largest part of the SDK")
	}
}

// TestSystemJavaWithoutAHome: /Library/Java belongs to no user, so it is
// claimed even when the home is unknown.
func TestSystemJavaWithoutAHome(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, _ := jvm.New().Classify(f.Tree, &jvm.Facts{}, classify.Context{})
	if _, ok := detecttest.ClaimAt(claims, "/Library/Java"); !ok {
		t.Error("the system JDKs were dropped because no home was known")
	}
	if _, ok := detecttest.ClaimAt(claims, home+"/.gradle"); ok {
		t.Error("a home-relative path was claimed with no home")
	}
}

// TestNamedDirectoriesOutrankGenericOnes: JNA and JetPackCache are also
// reached by cli-tools, which names them from the directory itself. The
// priority is what keeps the specific answer instead of the alphabet.
func TestNamedDirectoriesOutrankGenericOnes(t *testing.T) {
	f := detecttest.Build(t, corpus())
	claims, _ := jvm.New().Classify(f.Tree, &jvm.Facts{}, f.Context)
	for _, p := range []string{home + "/Library/Caches/JNA", home + "/Library/Caches/JetPackCache"} {
		c, ok := detecttest.ClaimAt(claims, p)
		if !ok {
			t.Errorf("no claim at %s", p)
			continue
		}
		if c.Priority <= 0 {
			t.Errorf("%s has priority %d, so cli-tools wins the tie on the alphabet", p, c.Priority)
		}
	}
}
