//go:build js && wasm

package web

import "syscall/js"

// Mirror returns what takes text just now, for the tests outside this package.
func (d *Driver) Mirror() js.Value { return d.text }

// Canvas returns the canvas.
func (d *Driver) Canvas() js.Value { return d.canvas }

// AfterLastWindow readies the driver for a new program, after a test closed its last
// window, which ends the one running.
func (d *Driver) AfterLastWindow() { d.quit = make(chan struct{}) }
