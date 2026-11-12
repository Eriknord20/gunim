//go:build windows

package desktop

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/internal/glfw"
)

// SetTray implements [driver.Trayer], on the main thread, and waits for
// the answer.
func (d *Driver) SetTray(t driver.Tray) error {
	var icon *glfw.TrayIcon
	if len(t.Icon) > 0 {
		icon = &glfw.TrayIcon{Images: t.Icon, Tooltip: t.Tooltip, Items: trayItems(t.Items)}
		if t.OnPick != nil {
			pick := t.OnPick
			icon.OnPick = func(id int) { go pick(id) }
		}
		if t.OnClick != nil {
			click := t.OnClick
			icon.OnClick = func() { go click() }
		}
		if t.OnNotifyGone != nil {
			gone := t.OnNotifyGone
			icon.OnNotifyGone = func() { go gone() }
		}
		if t.OnNotifyClick != nil {
			click := t.OnNotifyClick
			icon.OnNotifyClick = func() { go click() }
		}
	}
	done := make(chan error, 1)
	if !d.post(func() { done <- glfw.SetTrayIcon(icon) }) {
		return errClosed
	}
	return <-done
}

// TrayNotify implements [driver.TrayNotifier], on the main thread.
func (d *Driver) TrayNotify(title, body string) error {
	done := make(chan error, 1)
	if !d.post(func() { done <- glfw.TrayNotify(title, body) }) {
		return errClosed
	}
	return <-done
}

// closeTray takes the icon away, as the application ends, on the main
// thread.
func closeTray() { glfw.CloseTrayIcon() }

// trayItems are a menu's lines as glfw has them.
func trayItems(items []driver.TrayItem) []glfw.TrayMenuItem {
	out := make([]glfw.TrayMenuItem, 0, len(items))
	for _, it := range items {
		out = append(out, glfw.TrayMenuItem{Title: it.Title, ID: it.ID, Items: trayItems(it.Items),
			Checked: it.Checked, Disabled: it.Disabled, Separator: it.Separator, Default: it.Default})
	}
	return out
}
