//go:build !windows && !linux && !darwin && !js

package desktop

import "github.com/marrasen/gunim/driver"

// ChooseFiles implements [driver.FileChooser]; this platform has no file
// dialog in gunim yet.
func (w *Window) ChooseFiles(driver.ChooseOptions) ([]string, error) { return nil, driver.ErrNoChooser }

// SaveFile implements [driver.FileSaver]; this platform has no file dialog
// in gunim yet.
func (w *Window) SaveFile(driver.SaveOptions) (string, error) { return "", driver.ErrNoChooser }
