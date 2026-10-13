package filemanager

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// The limits of thumbnails.
const (
	thumbBudget  = 96 << 20
	defaultTile  = 128
	minTile      = 64
	maxTile      = 320
	thumbWorkers = 4
	// tileSettle is how long a new tile size waits for the next before it is saved.
	tileSettle = 400 * time.Millisecond
)

// thumbSizes are the sizes thumbnails are made at, in pixels across; a tile takes the smallest that covers it.
var thumbSizes = []int{96, 128, 192, 256, 384, 512}

// thumbBucket returns the size a thumbnail px pixels across is made at.
func thumbBucket(px int) int {
	for _, s := range thumbSizes {
		if s >= px {
			return s
		}
	}
	return thumbSizes[len(thumbSizes)-1]
}

// viewable reports whether the file called name is a picture the app can decode.
func viewable(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "png", "jpg", "jpeg", "gif", "bmp", "webp", "tif", "tiff":
		return true
	}
	return false
}

// thumbKey names a thumbnail: of the file at path on the file system of ID fs, as it was at mod, made at size.
type thumbKey struct {
	fs   string
	path string
	size int
	mod  int64
}

// thumbDone is a thumbnail made, or why it could not be.
type thumbDone struct {
	img  *paint.Image
	full image.Point
	err  error
}

// thumbJob is a thumbnail to make. online says the file keeps its contents online only, so the thumbnail comes from
// the system's cache or not at all.
type thumbJob struct {
	key       thumbKey
	dir, name string
	online    bool
}

// thumbState is the thumbnails the app has made, and those waiting for a worker.
type thumbState struct {
	cache map[thumbKey]thumbDone
	// order is the keys of cache, oldest first, and bytes what their pixels take.
	order []thumbKey
	bytes int
	queue []thumbJob
	busy  map[thumbKey]bool
	// made counts the thumbnails decoded, for tests.
	made int
	// tileSeq counts the tile sizes taken, so only the last of a run is saved.
	tileSeq int
}

func (t *thumbState) init() {
	t.cache = map[thumbKey]thumbDone{}
	t.busy = map[thumbKey]bool{}
}

// workers is how many thumbnails are made at once.
func workers() int { return min(thumbWorkers, max(1, runtime.NumCPU()/2)) }

// handleIcons takes the intents of the icon view.
func (a *app) handleIcons(in gunim.Intent) bool {
	switch v := in.(type) {
	case NeedThumbs:
		a.needThumbs(v)
	case TileSized:
		a.tileSized(v.Size)
	case Command:
		switch v.Name {
		case CmdViewDetails, CmdViewIcons:
			a.setView(v.Name == CmdViewIcons)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// viewMode is how the folder at path shows.
func (a *app) viewMode(path string) ViewMode {
	icons, ok := a.prefs.Views[a.viewKey(path)]
	if !ok {
		icons = a.prefs.Icons
	}
	tile := a.prefs.Tile
	if tile == 0 {
		tile = defaultTile
	}
	return ViewMode{Path: path, Icons: icons, Tile: tile}
}

// viewKey is the key the settings keep the view of the folder at path
// under: the path, on the computer's own file system, and the path after
// the file system's ID on another, so a server's folder does not take the
// view of the computer's folder of the same path.
func (a *app) viewKey(path string) string {
	if id := a.fs.ID(); id != "" {
		return id + "\x00" + path
	}
	return path
}

// publishView tells the window how the folder showing shows.
func (a *app) publishView() { a.patch(a.viewMode(a.nav.path)) }

// setView shows the folder showing as icons or as details, and remembers that for it and for folders not seen yet.
func (a *app) setView(icons bool) {
	key := a.viewKey(a.nav.path)
	a.savePrefs(func(p *prefs) {
		if p.Views == nil {
			p.Views = map[string]bool{}
		}
		p.Views[key] = icons
		p.Icons = icons
	})
	a.publishView()
}

// needThumbs makes the thumbnails the window asks for: at once from the cache, and the rest in the background, in
// place of those it asked for before.
func (a *app) needThumbs(v NeedThumbs) {
	n := &a.nav
	if v.Gen != n.gen {
		return
	}
	size := thumbBucket(v.Size)
	t := &a.thumbs
	t.queue = t.queue[:0]
	for _, i := range v.Rows {
		if i < 0 || i >= len(n.rows) {
			continue
		}
		e := n.rows[i]
		if e.Dir || !viewable(e.Name) {
			continue
		}
		job := thumbJob{key: thumbKey{fs: a.fs.ID(), path: a.ps.Join(n.path, e.Name), size: size, mod: e.Mod.UnixNano()},
			dir: n.path, name: e.Name, online: e.Online}
		if done, ok := t.cache[job.key]; ok {
			a.sendThumb(job, done)
			continue
		}
		if !t.busy[job.key] {
			t.queue = append(t.queue, job)
		}
	}
	a.pumpThumbs()
}

// pumpThumbs starts the next jobs while workers are free.
func (a *app) pumpThumbs() {
	t := &a.thumbs
	for len(t.busy) < workers() && len(t.queue) > 0 {
		job := t.queue[0]
		t.queue = t.queue[1:]
		t.busy[job.key] = true
		fsys := a.fs
		go func() {
			img, full, err := makeThumb(fsys, job.key.path, job.key.size, job.online)
			a.post(func() { a.thumbMade(job, thumbDone{img: img, full: full, err: err}) })
		}()
	}
}

// thumbMade keeps a thumbnail made, sends it to the window, and starts the next job.
func (a *app) thumbMade(job thumbJob, done thumbDone) {
	t := &a.thumbs
	delete(t.busy, job.key)
	t.made++
	t.cache[job.key] = done
	t.order = append(t.order, job.key)
	t.bytes += thumbBytes(done)
	for t.bytes > thumbBudget && len(t.order) > 1 {
		old := t.order[0]
		t.order = t.order[1:]
		t.bytes -= thumbBytes(t.cache[old])
		delete(t.cache, old)
	}
	a.sendThumb(job, done)
	a.pumpThumbs()
}

func thumbBytes(d thumbDone) int {
	if d.img == nil {
		return 64
	}
	w, h := d.img.Size()
	return 4 * w * h
}

func (a *app) sendThumb(job thumbJob, done thumbDone) {
	th := Thumb{Dir: job.dir, Name: job.name, Size: job.key.size, Image: done.img}
	if done.err != nil {
		th.Err = done.err.Error()
	}
	a.patch(th)
}

// cachedThumb returns the largest thumbnail made of the file at path as it was at mod, and the picture's own size,
// or nil.
func (a *app) cachedThumb(path string, mod int64) (*paint.Image, image.Point) {
	for i := len(thumbSizes) - 1; i >= 0; i-- {
		if d, ok := a.thumbs.cache[thumbKey{fs: a.fs.ID(), path: path, size: thumbSizes[i], mod: mod}]; ok && d.img != nil {
			return d.img, d.full
		}
	}
	return nil, image.Point{}
}

// makeThumb reads the picture at path on fsys and makes it at most size pixels across, and returns the picture's own
// size. A file kept online only is not read: its thumbnail is the one the system keeps, if any, and its size is
// unknown.
func makeThumb(fsys FS, path string, size int, online bool) (*paint.Image, image.Point, error) {
	if online {
		img, err := systemThumb(fsys, path, size)
		if err != nil || img == nil {
			return nil, image.Point{}, err
		}
		return paint.NewImageFit(img, size, size), image.Point{}, nil
	}
	img, full, err := decodePicture(fsys, path)
	if err != nil {
		return nil, full, err
	}
	return paint.NewImageFit(img, size, size), full, nil
}

// decodePicture reads the picture at path on fsys, and returns it with its size. A picture of more than maxPixels is
// refused, with its size in the error.
func decodePicture(fsys FS, path string) (img image.Image, size image.Point, err error) {
	name := fsys.Paths().Base(path)
	f, err := fsys.Open(path)
	if err != nil {
		return nil, image.Point{}, fmt.Errorf("reading %s: %w", name, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			img, err = nil, fmt.Errorf("reading %s: %w", name, cerr)
		}
	}()
	cfg, _, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return nil, image.Point{}, fmt.Errorf("reading the picture %s: %w", name, err)
	}
	size = image.Pt(cfg.Width, cfg.Height)
	if cfg.Width*cfg.Height > maxPixels {
		return nil, size, fmt.Errorf("%s is too large to show: %d × %d pixels", name, cfg.Width, cfg.Height)
	}
	if _, serr := f.Seek(0, io.SeekStart); serr != nil {
		return nil, size, fmt.Errorf("reading %s: %w", name, serr)
	}
	img, _, err = image.Decode(bufio.NewReader(f))
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			err = errors.New("the file ends early")
		}
		return nil, size, fmt.Errorf("reading the picture %s: %w", name, err)
	}
	return img, size, nil
}

// systemThumb returns the thumbnail the system keeps of the file at path on fsys, or nil where it keeps none.
func systemThumb(fsys FS, path string, size int) (image.Image, error) {
	if o, ok := optional[OnlineReporter](fsys); ok {
		return o.SystemThumb(path, size)
	}
	return nil, nil
}

// enteredFolder closes the viewer, and tells the window how the folder about to show shows.
func (a *app) enteredFolder() {
	a.closeViewer()
	a.publishView()
}

// tileSized takes a new tile size, and saves it once the slider or the wheel has rested.
func (a *app) tileSized(size float32) {
	tile := min(max(size, minTile), maxTile)
	a.prefs.Tile = tile
	a.thumbs.tileSeq++
	seq := a.thumbs.tileSeq
	time.AfterFunc(tileSettle, func() {
		a.post(func() {
			if seq == a.thumbs.tileSeq {
				a.savePrefs(func(p *prefs) { p.Tile = tile })
			}
		})
	})
}
