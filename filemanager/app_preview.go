package filemanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // GIF thumbnails
	_ "image/jpeg" // JPEG thumbnails
	_ "image/png"  // PNG thumbnails
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "golang.org/x/image/bmp" // BMP thumbnails
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // TIFF thumbnails
	_ "golang.org/x/image/webp" // WebP thumbnails

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// The limits of a preview.
const (
	thumbSize   = 320
	maxImage    = 64 << 20
	maxPixels   = 12000 * 12000
	textHead    = 16 << 10
	textLines   = 200
	countEvery  = 150 * time.Millisecond
	previewWait = 60 * time.Millisecond
)

// previewState is the subject of the preview pane and the work on it.
type previewState struct {
	seq    int
	cancel context.CancelFunc
	// subject is what the pane shows, so a selection that changes back
	// and forth costs nothing.
	subject string
	// fetch is the path of a file kept online only that the user asked to download to preview.
	fetch string
}

func (p *previewState) stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

// handlePreview takes the intents of the preview pane.
func (a *app) handlePreview(in gunim.Intent) bool {
	switch v := in.(type) {
	case RevealPath:
		a.reveal(v.Path)
	case FetchPreview:
		a.preview.fetch, a.preview.subject = v.Path, ""
		a.showPreview()
	case Command:
		if v.Name != CmdReveal {
			return false
		}
		paths := a.selectedPaths()
		if len(paths) == 0 {
			paths = []string{a.nav.path}
		}
		a.reveal(paths[0])
	default:
		return false
	}
	return true
}

func (a *app) reveal(path string) {
	reveal, ok := a.systemReveal()
	if !ok {
		return
	}
	go func() {
		if err := reveal(path); err != nil {
			a.post(func() { a.fail(fmt.Sprintf("Showing %s: %v", a.ps.Show(path), err)) })
		}
	}()
}

// showPreview works out the preview for the selection, in the background.
func (a *app) showPreview() { a.showPreviewOf(a.selectedEntries()) }

// showPreviewOf works out the preview for sel, the rows selected, in order.
func (a *app) showPreviewOf(sel []entry) {
	n := &a.nav
	if n.loading {
		// The preview stays as it is until the folder is read.
		return
	}
	var subject string
	switch {
	case n.err != nil:
		subject = "none"
	case len(sel) == 1:
		subject = "item:" + a.ps.Join(n.path, sel[0].Name) + sel[0].Mod.String()
	case len(sel) > 1:
		subject = fmt.Sprintf("many:%d:%s", len(sel), sel[0].Name)
	default:
		subject = "folder:" + n.path + n.mod.String()
	}
	p := &a.preview
	if subject == p.subject {
		return
	}
	p.subject = subject
	p.stop()
	p.seq++
	seq := p.seq
	ctx, cancel := context.WithCancel(a.ctx)
	p.cancel = cancel
	switch {
	case subject == "none":
		a.patch(Preview{Seq: seq})
	case len(sel) > 1:
		a.patch(manyPreview(seq, sel))
	case len(sel) == 1:
		path := a.ps.Join(n.path, sel[0].Name)
		e, fsys, fetch := sel[0], a.fs, a.preview.fetch == path
		go func() {
			// A selection moving fast with the arrow keys settles first.
			select {
			case <-time.After(previewWait):
			case <-ctx.Done():
				return
			}
			pv := itemPreview(ctx, fsys, seq, path, e, fetch)
			if ctx.Err() != nil {
				return
			}
			a.post(func() {
				if a.preview.seq == seq {
					pv.NoReveal = a.fetches()
					a.patch(pv)
					if e.Dir {
						a.count(ctx, seq, path)
					}
				}
			})
		}()
	default:
		info := entry{Name: a.ps.placeName(n.path), Dir: true, Type: "Folder", Mod: n.mod}
		key := ""
		if a.systemIconsOn() {
			key = "dir"
		}
		pv := Preview{Seq: seq, Title: info.Name, Type: "This folder", Path: n.path, Shown: a.ps.Show(n.path), Tint: TintFolder, IconKey: key, Dir: true,
			Facts: []Fact{{"Holds", plural(len(n.all), "item")}, {"Modified", fmtTime(n.mod)}, {"In", a.ps.placeName(a.ps.Dir(n.path))}}, NoReveal: a.fetches()}
		a.patch(pv)
	}
}

// manyPreview sums up several items.
func manyPreview(seq int, sel []entry) Preview {
	var size int64
	dirs, files := 0, 0
	for _, e := range sel {
		if e.Dir {
			dirs++
		} else {
			files++
			size += e.Size
		}
	}
	pv := Preview{Seq: seq, Title: plural(len(sel), "item") + " selected", Type: "Selection", Tint: TintOther}
	if files > 0 {
		pv.Facts = append(pv.Facts, Fact{"Files", fmt.Sprintf("%d, %s", files, humanBytes(size))})
	}
	if dirs > 0 {
		pv.Facts = append(pv.Facts, Fact{"Folders", strconv.Itoa(dirs)})
	}
	return pv
}

// itemPreview reads what the pane shows for the item at path on fsys. A file kept online only is read, and so
// downloaded, only when fetch says the user asked.
func itemPreview(ctx context.Context, fsys FS, seq int, path string, e entry, fetch bool) Preview {
	ps := fsys.Paths()
	pv := Preview{Seq: seq, Title: e.Name, Type: e.Type, Path: path, Shown: ps.Show(path), Tint: tintOf(e), IconKey: e.iconKey, Dir: e.Dir}
	if !e.Dir {
		size := humanBytes(e.Size)
		if e.Size >= 1024 {
			size += fmt.Sprintf(" (%d bytes)", e.Size)
		}
		pv.Facts = append(pv.Facts, Fact{"Size", size})
	}
	pv.Facts = append(pv.Facts, Fact{"Modified", fmtTime(e.Mod)})
	if l, ok := fsys.(Linker); ok && e.Kind == KindLink {
		target, err := l.Readlink(path)
		if err != nil {
			pv.Err = "Reading the link: " + err.Error()
			return pv
		}
		pv.Facts = append(pv.Facts, Fact{"Points to", target})
	}
	pv.Facts = append(pv.Facts, Fact{"In", ps.placeName(ps.Dir(path))})
	switch {
	case e.Dir:
		pv.Counting = true
	case e.Broken:
	case e.Online && !fetch:
		pv.Online = true
		img, err := systemThumb(fsys, path, thumbSize)
		switch {
		case err != nil:
			pv.Err = err.Error()
		case img != nil:
			pv.Image = paint.NewImageFit(img, thumbSize, thumbSize)
		}
	case tintOf(e) == TintImage:
		img, size, err := thumbnail(fsys, path, e.Size)
		switch {
		case err != nil:
			pv.Err = err.Error()
		case img != nil:
			pv.Image = img
			pv.Facts = append([]Fact{{"Picture", fmt.Sprintf("%d × %d", size.X, size.Y)}}, pv.Facts...)
		}
	default:
		text, cut, err := textStart(ctx, fsys, path)
		switch {
		case err != nil:
			pv.Err = err.Error()
		default:
			pv.Text, pv.Cut = text, cut
		}
	}
	return pv
}

func fmtTime(t time.Time) string { return t.Format("2006-01-02 15:04:05") }

// thumbnail reads the picture at path on fsys and makes it at most
// thumbSize across. A picture too large to read quickly gives none.
func thumbnail(fsys FS, path string, size int64) (*paint.Image, image.Point, error) {
	if size > maxImage {
		return nil, image.Point{}, nil
	}
	name := fsys.Paths().Base(path)
	b, err := readFile(fsys, path)
	if err != nil {
		return nil, image.Point{}, fmt.Errorf("reading %s: %w", name, err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if errors.Is(err, image.ErrFormat) {
		return nil, image.Point{}, nil
	}
	if err != nil {
		return nil, image.Point{}, fmt.Errorf("reading the picture %s: %w", name, err)
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, image.Pt(cfg.Width, cfg.Height), nil
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, image.Point{}, fmt.Errorf("reading the picture %s: %w", name, err)
	}
	full := img.Bounds().Size()
	scale := min(1, float64(thumbSize)/float64(max(full.X, full.Y)))
	w, h := max(1, int(float64(full.X)*scale)), max(1, int(float64(full.Y)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return paint.NewImage(dst), full, nil
}

// textStart reads the start of the file at path on fsys when it holds
// text, and says whether there is more.
func textStart(ctx context.Context, fsys FS, path string) (head string, more bool, err error) {
	name := fsys.Paths().Base(path)
	f, err := fsys.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", name, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			head, more, err = "", false, fmt.Errorf("reading %s: %w", name, cerr)
		}
	}()
	buf := make([]byte, textHead)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", false, fmt.Errorf("reading %s: %w", name, err)
	}
	if ctx.Err() != nil {
		return "", false, nil
	}
	cut := n == len(buf)
	b := buf[:n]
	if bytes.IndexByte(b, 0) >= 0 {
		return "", false, nil
	}
	// A rune cut off at the end of the buffer is not a sign of binary.
	for k := 0; k < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); k++ {
		b = b[:len(b)-1]
	}
	if !utf8.Valid(b) {
		return "", false, nil
	}
	lines := strings.SplitAfter(string(b), "\n")
	if len(lines) > textLines {
		lines, cut = lines[:textLines], true
	}
	return strings.ReplaceAll(strings.Join(lines, ""), "\t", "    "), cut, nil
}

// count counts what the folder at path holds, and patches the preview as
// it goes.
func (a *app) count(ctx context.Context, seq int, path string) {
	fsys := a.fs
	go func() {
		var items int
		var size int64
		last := time.Now()
		send := func(counting bool, err error) {
			c := Counted{Seq: seq, Items: plural(items, "item"), Size: humanBytes(size), Counting: counting}
			if err != nil {
				c.Err = err.Error()
			}
			a.post(func() {
				if a.preview.seq == seq {
					a.patch(c)
				}
			})
		}
		err := walkTree(ctx, fsys, path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("reading %s: %w", p, err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if p == path {
				return nil
			}
			items++
			if d.Type().IsRegular() {
				info, err := d.Info()
				if err != nil {
					return fmt.Errorf("reading %s: %w", p, err)
				}
				size += info.Size()
			}
			if time.Since(last) >= countEvery {
				last = time.Now()
				send(true, nil)
			}
			return nil
		})
		if errors.Is(err, context.Canceled) {
			return
		}
		send(false, err)
	}()
}
