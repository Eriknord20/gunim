package filemanager

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// Fact is one line of what the preview pane says about an item.
type Fact struct {
	Label, Value string
}

// Preview is what the preview pane shows: one item, several, or the
// folder itself. Seq changes with each new subject.
type Preview struct {
	Seq   int
	Title string
	Type  string
	// Path is the item's path, for the Reveal and Copy path links; empty
	// for several items. Shown is the path as the user reads it, which
	// the pane shows and Copy path copies.
	Path, Shown string
	// NoReveal leaves out the link that shows the item in the system's
	// file manager, for an item of a file system that cannot.
	NoReveal bool
	Tint     Tint
	// IconKey names the system icon the item shows with, as a row's
	// does, and is empty where there is none; Dir says it is a folder.
	IconKey string
	Dir     bool
	Facts   []Fact
	// Image is a picture's thumbnail, and Text the start of a text file,
	// Cut set when there is more.
	Image *paint.Image
	Text  string
	Cut   bool
	// Counting is set while a folder's contents are still being counted.
	Counting bool
	// Online says the file's contents are online only, and the preview left them there: reading them would download
	// the file. Image may still be the thumbnail the system keeps.
	Online bool
	// Err says why the preview is incomplete.
	Err string
}

// Counted is a patch: how much a folder holds, so far or in all.
type Counted struct {
	Seq      int
	Items    string
	Size     string
	Counting bool
	Err      string
}

// FetchPreview asks to download the file at Path, kept online only, and preview it.
type FetchPreview struct {
	Path string
}

// RevealPath asks to show a path in the system's file manager.
type RevealPath struct {
	Path string
}

func init() {
	gunim.RegisterType[Preview]("files.preview")
	gunim.RegisterType[Counted]("files.counted")
	gunim.RegisterType[RevealPath]("files.reveal")
	gunim.RegisterType[FetchPreview]("files.fetchpreview")
}
