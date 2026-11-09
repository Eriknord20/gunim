// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's.

package glfw

import (
	"fmt"
	"strings"

	"github.com/ebitengine/purego/objc"

	"github.com/marrasen/gunim/internal/cocoa"
)

// FileDialog describes a file dialog for [Window.ShowFileDialog].
type FileDialog struct {
	// Save shows a save panel, and Name is the file name it starts with.
	// Without it an open panel shows.
	Save bool
	Name string
	// Message is shown at the top of the panel; a sheet has no title.
	Message string
	// Folder is the folder the panel opens in, "" for the system's choice.
	Folder string
	// Folders chooses folders instead of files, and Multiple lets more
	// than one be chosen. Both are for an open panel.
	Folders, Multiple bool
	// Extensions narrow the files shown to those ending in them, without
	// the dot. None shows every file.
	Extensions []string
}

// nsModalResponseOK is NSModalResponseOK, the panel's answer when a file
// was chosen.
const nsModalResponseOK = 1

// nsModalResponseCancel is NSModalResponseCancel, the answer a sheet ends
// with when its window is destroyed.
const nsModalResponseCancel = 0

// ShowFileDialog shows the system's open or save panel as a sheet on the
// window, and returns at once. done runs on the main thread when the
// panel closes, with the paths chosen, none where it was cancelled. It
// must be called on the main thread.
func (w *Window) ShowFileDialog(d FileDialog, done func(paths []string)) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	if w.platform.object == 0 {
		return fmt.Errorf("glfw: the window for a file dialog is destroyed: %w", InvalidValue)
	}
	pool := cocoa.NSAutoreleasePool_new()
	defer pool.Release()

	class, maker := "NSOpenPanel", "openPanel"
	if d.Save {
		class, maker = "NSSavePanel", "savePanel"
	}
	panel := objc.ID(objc.GetClass(class)).Send(objc.RegisterName(maker))
	// The sheet holds the panel while it shows; this keeps it past the
	// pool until the answer is read.
	panel.Send(sel_retain)
	if d.Message != "" {
		panel.Send(objc.RegisterName("setMessage:"), nsString(d.Message))
	}
	if d.Folder != "" {
		url := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("fileURLWithPath:isDirectory:"), nsString(d.Folder), true)
		panel.Send(objc.RegisterName("setDirectoryURL:"), url)
	}
	if d.Save {
		if d.Name != "" {
			panel.Send(objc.RegisterName("setNameFieldStringValue:"), nsString(d.Name))
		}
		panel.Send(objc.RegisterName("setCanCreateDirectories:"), true)
	} else {
		panel.Send(objc.RegisterName("setCanChooseFiles:"), !d.Folders)
		panel.Send(objc.RegisterName("setCanChooseDirectories:"), d.Folders)
		panel.Send(objc.RegisterName("setAllowsMultipleSelection:"), d.Multiple)
	}
	if len(d.Extensions) > 0 && !d.Folders {
		types := objc.ID(objc.GetClass("NSMutableArray")).Send(objc.RegisterName("array"))
		for _, e := range d.Extensions {
			types.Send(objc.RegisterName("addObject:"), nsString(strings.TrimPrefix(e, ".")))
		}
		panel.Send(objc.RegisterName("setAllowedFileTypes:"), types)
	}

	var handler objc.Block
	// finished keeps a second answer from releasing the panel again.
	finished := false
	handler = objc.NewBlock(func(_ objc.Block, response int) {
		if finished {
			return
		}
		finished = true
		pool := cocoa.NSAutoreleasePool_new()
		defer pool.Release()
		var paths []string
		if response == nsModalResponseOK {
			if d.Save {
				paths = appendURLPath(paths, panel.Send(objc.RegisterName("URL")))
			} else {
				urls := panel.Send(objc.RegisterName("URLs"))
				n := int(urls.Send(objc.RegisterName("count")))
				for i := range n {
					paths = appendURLPath(paths, urls.Send(objc.RegisterName("objectAtIndex:"), i))
				}
			}
		}
		panel.Send(sel_release)
		handler.Release()
		done(paths)
	})
	panel.Send(objc.RegisterName("beginSheetModalForWindow:completionHandler:"), w.platform.object, handler)
	return nil
}

// appendURLPath appends the path of the file URL url, where it has one.
func appendURLPath(paths []string, url objc.ID) []string {
	if url == 0 {
		return paths
	}
	p := url.Send(objc.RegisterName("path"))
	if p == 0 {
		return paths
	}
	return append(paths, cocoa.NSString{ID: p}.String())
}

// nsString returns s as an autoreleased NSString.
func nsString(s string) objc.ID {
	return cocoa.NSString_alloc().InitWithUTF8String(s).Send(sel_autorelease)
}

// endAttachedSheet ends the sheet on w, if one shows, as cancelled.
func (w *Window) endAttachedSheet() {
	sheet := w.platform.object.Send(objc.RegisterName("attachedSheet"))
	if sheet != 0 {
		w.platform.object.Send(objc.RegisterName("endSheet:returnCode:"), sheet, nsModalResponseCancel)
	}
}
