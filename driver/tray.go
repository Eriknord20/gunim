package driver

import (
	"errors"
	"image"
)

// ErrNoTray says the platform has no tray to show an icon in.
var ErrNoTray = errors.New("there is no system tray here")

// Tray is an icon in the system tray, the notification area on Windows,
// with a menu, for an application that runs without a window in front.
type Tray struct {
	// Icon is the icon at several sizes, for the tray to pick from, and
	// Tooltip what shows as the pointer rests on it.
	Icon    []image.Image
	Tooltip string
	// Items are the lines of its menu.
	Items []TrayItem
	// OnPick runs with the ID of the line picked from the menu, and
	// OnClick with a click on the icon itself, where the tray has one
	// apart from the menu's. Both run on a goroutine of the driver's,
	// and are to hand the work on rather than do it there.
	OnPick  func(id int)
	OnClick func()
	// OnNotifyClick runs with a click on a message [TrayNotifier.TrayNotify] showed, as for a message that opens the
	// page it is about. Windows does not say which message, and may say so for a click in the notification centre
	// too, after a newer message: open a page that fits any message shown, or keep only one kind of message waiting.
	// The program takes the foreground as the click comes, so a window raised from here comes to the front.
	//
	// OnNotifyGone runs when the message shown last leaves the screen, closed or timed out, as it may then wait in
	// the notification centre. Both run on a goroutine of the driver's. Only Windows says so.
	OnNotifyClick func()
	OnNotifyGone  func()
}

// TrayItem is a line of a tray icon's menu: a command, a submenu, or a
// line between groups.
type TrayItem struct {
	// Title is what the line says. ID is what OnPick hears when it is
	// picked, and means nothing for a submenu or a separator.
	Title string
	ID    int
	// Items make the line a submenu of them.
	Items []TrayItem
	// Checked ticks the line, Disabled greys it out, and Separator makes
	// it a line between groups and nothing else.
	Checked   bool
	Disabled  bool
	Separator bool
	// Default draws the line in bold, as what a click on the icon does.
	Default bool
}

// A TrayNotifier is a [Driver] that shows a message from its tray icon,
// as the system shows one.
type TrayNotifier interface {
	// TrayNotify shows a message from the tray icon, as the system shows
	// one, and fails where no icon is up or the platform has none.
	TrayNotify(title, body string) error
}

// A Trayer is a [Driver] that can show a tray icon.
type Trayer interface {
	// SetTray shows t in place of the tray icon shown before, and a
	// Tray with no Icon takes it away. It returns an error where the
	// platform has no tray to show one in.
	SetTray(t Tray) error
}
