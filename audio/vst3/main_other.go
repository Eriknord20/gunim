//go:build !darwin && !js

package vst3

// onMain reports that the package's own thread runs f: on this system a
// plugin's editor needs no thread of its own.
func onMain(func()) bool { return false }

// onExit runs f, a module's exit, on the plugins' thread.
func onExit(f func()) { onUI(f) }
