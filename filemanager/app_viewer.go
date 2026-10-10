package filemanager

import (
	"context"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// viewerState is the picture viewer: whether it shows, the pictures of the folder it steps through, and the
// decoding of the one showing.
type viewerState struct {
	open bool
	// opened counts the viewers opened, for each one's ID.
	opened int
	id     gunim.ID
	seq    int
	pics   []entry
	at     int
	view   Viewing
	cancel context.CancelFunc
	// asked is the size the picture showing is decoded at, or being decoded at.
	asked [2]int
}

func (v *viewerState) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
}

// handleViewer takes the intents of the viewer.
func (a *app) handleViewer(in gunim.Intent) bool {
	switch v := in.(type) {
	case OpenViewer:
		a.openViewer(v.Gen, v.Row)
	case ViewerStep:
		a.stepViewer(v.Dir)
	case ViewerWants:
		a.viewerWants(v)
	case ViewerClosed:
		a.viewerClosed(v.Seq)
	case Command:
		switch v.Name {
		case CmdViewer:
			a.openViewerOnCursor()
		case CmdViewerNext:
			a.stepViewer(1)
		case CmdViewerPrev:
			a.stepViewer(-1)
		case CmdViewerClose:
			a.closeViewer()
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// openViewerOnCursor opens the viewer on the row the keyboard is on.
func (a *app) openViewerOnCursor() {
	n := &a.nav
	for i, e := range n.rows {
		if e.Name == n.cursor {
			a.openViewer(n.gen, i)
			return
		}
	}
}

// openViewer shows the picture in row of listing gen large, any other file in the text viewer, and opens a folder as
// a double click does.
func (a *app) openViewer(gen, row int) {
	n := &a.nav
	if gen != n.gen || row < 0 || row >= len(n.rows) {
		return
	}
	e := n.rows[row]
	switch {
	case e.Broken:
		return
	case e.Dir:
		a.activate(e)
		return
	case !viewable(e.Name):
		// Not a picture: its text, in the text viewer.
		a.openReading(e)
		return
	}
	v := &a.viewer
	v.pics = v.pics[:0]
	for _, r := range n.rows {
		if !r.Dir && viewable(r.Name) {
			v.pics = append(v.pics, r)
		}
		if r.Name == e.Name {
			v.at = len(v.pics) - 1
		}
	}
	a.showPicture(0)
}

// stepViewer goes to the next picture, or with dir -1 the one before, and selects it in the listing.
func (a *app) stepViewer(dir int) {
	v := &a.viewer
	if !v.open || v.at+dir < 0 || v.at+dir >= len(v.pics) {
		return
	}
	v.at += dir
	a.showPicture(dir)
}

// showPicture shows the picture v.at in the viewer, opening it if need be, and selects it in the listing.
func (a *app) showPicture(travel int) {
	v := &a.viewer
	n := &a.nav
	e := v.pics[v.at]
	path := a.ps.Join(n.path, e.Name)
	v.stop()
	v.seq++
	v.asked = [2]int{}
	thumb, full := a.cachedThumb(path, e.Mod.UnixNano())
	v.view = Viewing{Seq: v.seq, Travel: travel, Path: path, Name: e.Name, Index: v.at + 1, Count: len(v.pics),
		Thumb: thumb, W: full.X, H: full.Y, Loading: true}
	if v.open {
		a.send(a.c.Update(v.id, v.view))
	} else {
		v.open = true
		v.opened++
		v.id = a.ids.viewer(v.opened)
		a.send(a.c.Mount(gunim.Root, v.id, "viewer", v.view))
	}
	clear(n.sel)
	n.sel[e.Name] = true
	n.cursor = e.Name
	a.publishSelection()
	a.publishStatus()
	a.showPreview()
}

// viewerWants decodes the picture showing to fill the size the viewer asks for, when it has none that large.
func (a *app) viewerWants(w ViewerWants) {
	v := &a.viewer
	if !v.open || w.Seq != v.seq || w.W <= v.asked[0] && w.H <= v.asked[1] || v.view.Err != "" {
		return
	}
	if v.view.W > 0 && v.asked[0] >= v.view.W && v.asked[1] >= v.view.H {
		// It is decoded whole already.
		return
	}
	v.asked = [2]int{max(w.W, v.asked[0]), max(w.H, v.asked[1])}
	v.stop()
	ctx, cancel := context.WithCancel(a.ctx)
	v.cancel = cancel
	seq, path, fit, fsys := v.seq, v.view.Path, v.asked, a.fs
	go func() {
		img, size, err := decodePicture(fsys, path)
		var pic *paint.Image
		if err == nil && ctx.Err() == nil {
			pic = paint.NewImageFit(img, fit[0], fit[1])
		}
		if ctx.Err() != nil {
			return
		}
		a.post(func() { a.pictureDecoded(seq, pic, size.X, size.Y, err) })
	}()
}

// pictureDecoded shows a picture decoded for viewer state seq.
func (a *app) pictureDecoded(seq int, pic *paint.Image, w, h int, err error) {
	v := &a.viewer
	if !v.open || seq != v.seq {
		return
	}
	v.view.Loading = false
	v.view.W, v.view.H = w, h
	if err != nil {
		v.view.Err = err.Error()
		a.logLine("Showing a picture", v.view.Err)
	} else {
		v.view.Image = pic
	}
	a.send(a.c.Update(v.id, v.view))
}

// viewerClosed takes word that the viewer closed itself, and gives the keyboard back to the listing.
func (a *app) viewerClosed(seq int) {
	v := &a.viewer
	if !v.open || seq != v.seq {
		return
	}
	v.open = false
	v.stop()
	a.patch(FocusListing{})
}

// closeViewer closes the viewer from the application's side.
func (a *app) closeViewer() {
	v := &a.viewer
	if !v.open {
		return
	}
	v.open = false
	v.stop()
	a.send(a.c.Unmount(v.id))
	a.patch(FocusListing{})
}
