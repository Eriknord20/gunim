package vst3

import (
	"os"
	"path/filepath"
	"testing"
)

// A bundle whose library is named otherwise than the bundle, as its
// Info.plist names it, is found by that name; one naming none, by the
// bundle's.
func TestABundleNamesItsOwnLibrary(t *testing.T) {
	dir := t.TempDir()
	makeBundle := func(bundle, exe, plist string) string {
		b := filepath.Join(dir, bundle+".vst3")
		if err := os.MkdirAll(filepath.Join(b, "Contents", "MacOS"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b, "Contents", "MacOS", exe), nil, 0o755); err != nil {
			t.Fatal(err)
		}
		if plist != "" {
			if err := os.WriteFile(filepath.Join(b, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return b
	}
	named := makeBundle("Ozone EQ", "PluginHooksVST3", `<?xml version="1.0"?><plist><dict>
	<key>CFBundleExecutable</key>
	<string>PluginHooksVST3</string>
</dict></plist>`)
	plain := makeBundle("Plain", "Plain", "")
	for _, c := range []struct{ bundle, want string }{
		{named, filepath.Join(named, "Contents", "MacOS", "PluginHooksVST3")},
		{plain, filepath.Join(plain, "Contents", "MacOS", "Plain")},
	} {
		got, err := binaryPath(c.bundle)
		if err != nil || got != c.want {
			t.Errorf("binaryPath(%s) = %q, %v, want %q", filepath.Base(c.bundle), got, err, c.want)
		}
	}
}
