//go:build !js

package vst3

import (
	"path/filepath"
	"testing"
)

// TestBundleOfFindsTheVST3Folder finds the .vst3 folder from the library in its Contents/MacOS, and nothing for a
// bare file.
func TestBundleOfFindsTheVST3Folder(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("Plug-Ins", "VST3", "Synth.vst3")
	if got := bundleOf(filepath.Join(dir, "Contents", "MacOS", "Synth")); got != dir {
		t.Errorf("bundleOf the library in a bundle = %q, want %q", got, dir)
	}
	if got := bundleOf(filepath.Join("Plug-Ins", "Synth.vst3")); got != "" {
		t.Errorf("bundleOf a bare file = %q, want none", got)
	}
}
