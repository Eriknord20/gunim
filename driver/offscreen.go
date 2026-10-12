package driver

import (
	"image/color"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// Offscreen returns a window backed by no display.
//
// It keeps the op list from the last frame instead of drawing it, which
// makes it the driver for tests, for golden-image comparisons once a
// rasteriser exists, and for running an interface headlessly on a build
// machine. Its Presented and Input channels stay quiet, so whoever
// holds it decides when a frame reaches the "screen".
func Offscreen(size geom.Size) *OffscreenWindow {
	return &OffscreenWindow{
		size:      size,
		scale:     1,
		zoom:      1,
		rate:      60,
		presented: make(chan Frame),
		input:     make(chan any),
	}
}

// An OffscreenWindow records frames rather than showing them.
type OffscreenWindow struct {
	mu    sync.Mutex
	size  geom.Size
	scale float32
	// pinned says the window was last asked to stay above other windows,
	// and cloaked that it was last taken off the screen.
	pinned, cloaked bool
	// zoom multiplies scale, and divides the size the content is laid out in.
	zoom float32
	rate float64
	ops  []paint.Op
	// damage is the part of the window the last frame changed.
	damage geom.Rect
	// cursor is the pointer's shape last set.
	cursor input.Cursor
	clip   string
	// clipImage is the clipboard's picture, as PNG.
	clipImage []byte
	// clipErr is what reading the clipboard fails with, for a test.
	clipErr error
	// chooser answers ChooseFiles; see SetChooser.
	chooser func(ChooseOptions) ([]string, error)
	// saver answers SaveFile; see SetSaver.
	saver func(SaveOptions) (string, error)
	// open and reveal answer Open and Reveal; see SetLauncher.
	open, reveal func(string) error
	// link answers OpenLink; see SetLinkOpener.
	link func(string) error
	// share and vibrate answer Share and Vibrate; see SetSharer and
	// SetVibrator.
	share   func(Share) error
	vibrate func([]time.Duration) error
	// compass answers WatchHeading; see SetCompass.
	compass func(on bool)
	// anchor is where a popup was last attached, and origin where the
	// window sits on its pretend screen.
	anchor geom.Rect
	origin geom.Point
	// out holds the files of each drag handed to other programs.
	out [][]string
	// access says whether something listens for the accessibility tree,
	// and tree is the last one published.
	access bool
	tree   *access.Tree
	// hidden says the window is hidden, kept to show again.
	hidden bool
	// region is where the window was last told it takes the pointer, nil for all of it; see SetPointerRegion.
	region []geom.Rect
	// under is the colour last set under the window's frames.
	under color.NRGBA
	// frame is the pretend frame of a window made chromeless.
	frame *OffscreenFrame
	// safe is the part of the window a pretend phone's bars lie over;
	// see SetSafeArea.
	safe geom.Insets
	// keyboard is how far up a pretend soft keyboard reaches; see
	// SetKeyboardCover.
	keyboard float32

	// radius, edge and blends are the shape the window says it shows; see
	// SetOutline.
	radius, edge float32
	blends       bool
	// workArea is the pretend screen's work area, in screen space, for
	// the room it leaves a popup; see SetWorkArea.
	workArea geom.Rect
	// title, full, attention, raised and border are what the
	// application last asked of the window's frame.
	title     string
	full      bool
	attention int
	raised    int
	border    Border

	presented chan Frame
	input     chan any
}

// SetTitle implements [Titler].
func (w *OffscreenWindow) SetTitle(title string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.title = title
}

// Title returns the title last set.
func (w *OffscreenWindow) Title() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.title
}

// SetWorkArea sets the pretend screen's work area, in screen space, where the window sits at its origin, for a test
// of popups that fit themselves to it. Until it is set there is no end to it.
func (w *OffscreenWindow) SetWorkArea(area geom.Rect) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.workArea = area
}

// PopupRoom implements [PopupRoomer]: the room between the anchor and the work area's edges.
func (w *OffscreenWindow) PopupRoom(anchor geom.Rect) Room {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The work area in the window's space
	a := w.workArea.Add(geom.Pt(-w.origin.X, -w.origin.Y))
	if w.workArea.Empty() {
		return NoRoomLimit
	}
	return Room{Below: a.Max.Y - anchor.Max.Y, Above: anchor.Min.Y - a.Min.Y, Left: anchor.Min.X - a.Min.X, Right: a.Max.X - anchor.Min.X}
}

// SetBorder implements [Borderer].
func (w *OffscreenWindow) SetBorder(b Border) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.border = b
}

// Border returns the border last set.
func (w *OffscreenWindow) Border() Border {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.border
}

// SetFullScreen implements [FullScreener].
func (w *OffscreenWindow) SetFullScreen(on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.full = on
}

// FullScreen implements [FullScreener].
func (w *OffscreenWindow) FullScreen() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.full
}

// RequestAttention implements [Attender].
func (w *OffscreenWindow) RequestAttention() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attention++
}

// ToFront implements [Fronter].
func (w *OffscreenWindow) ToFront() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.raised++
}

// Raised returns how many times the window was brought to the front.
func (w *OffscreenWindow) Raised() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.raised
}

// Attention returns how many times attention was asked for.
func (w *OffscreenWindow) Attention() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.attention
}

// Presented implements [Window]. It stays quiet until [OffscreenWindow.Tick].
func (w *OffscreenWindow) Presented() <-chan Frame { return w.presented }

// Input implements [Window]. Feed it with [OffscreenWindow.Post].
func (w *OffscreenWindow) Input() <-chan any { return w.input }

// Tick reports the frame in flight as shown, blocking until the window
// takes the report.
//
// A real driver reports each frame as its swap returns. This one waits
// for the test, so a test decides exactly when the display is ready for
// the next frame.
func (w *OffscreenWindow) Tick() { w.presented <- Frame{Shown: time.Now()} }

// Post delivers a platform event, blocking until the window reads it.
func (w *OffscreenWindow) Post(ev any) { w.input <- ev }

// Present implements [Window] by keeping the op list and the damage.
func (w *OffscreenWindow) Present(ops []paint.Op, damage geom.Rect) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The engine reuses its buffers, the ops themselves among them, two
	// frames on, so keep a copy for Ops to return.
	w.ops = keepOps(ops)
	w.damage = damage
	return nil
}

// keepOps copies ops deep enough that later frames leave the copy as
// it is: each op, and the glyphs, items or cells it lists. What an op
// points to that the engine never reuses, such as a mesh, an image or
// a gradient, is shared.
func keepOps(ops []paint.Op) []paint.Op {
	kept := make([]paint.Op, len(ops))
	for i, op := range ops {
		switch op := op.(type) {
		case *paint.RRectOp:
			c := *op
			kept[i] = &c
		case *paint.TextOp:
			c := *op
			c.Glyphs = slices.Clone(op.Glyphs)
			kept[i] = &c
		case *paint.MaskOp:
			c := *op
			kept[i] = &c
		case *paint.SceneOp:
			c := *op
			c.Scene.Items = slices.Clone(op.Scene.Items)
			kept[i] = &c
		case *paint.ImageOp:
			c := *op
			kept[i] = &c
		case *paint.CellsOp:
			c := *op
			c.Cells = slices.Clone(op.Cells)
			kept[i] = &c
		case *paint.LayerOp:
			c := *op
			kept[i] = &c
		default:
			// A LayerEndOp holds nothing.
			kept[i] = op
		}
	}
	return kept
}

// SetCursor implements [CursorSetter].
func (w *OffscreenWindow) SetCursor(c input.Cursor) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cursor = c
}

// Cursor returns the pointer's shape last set.
func (w *OffscreenWindow) Cursor() input.Cursor {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursor
}

// Damage returns the part of the window the last frame changed, in
// logical pixels: empty when it changed nothing.
func (w *OffscreenWindow) Damage() geom.Rect {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.damage
}

// Ops returns the commands recorded by the last frame.
func (w *OffscreenWindow) Ops() []paint.Op {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ops
}

// SetSafeArea has the window play one a phone's bars lie over, in from
// its edges by in, from the next frame, for a test of what keeps clear
// of them.
func (w *OffscreenWindow) SetSafeArea(in geom.Insets) {
	w.mu.Lock()
	w.safe = in
	w.mu.Unlock()
}

// SafeArea implements [SafeAreaer].
func (w *OffscreenWindow) SafeArea() geom.Insets {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.safe
}

// SetKeyboardCover has a pretend soft keyboard cover the window's
// bottom, up by h, from the next frame, for a test of what moves clear
// of it.
func (w *OffscreenWindow) SetKeyboardCover(h float32) {
	w.mu.Lock()
	w.keyboard = h
	w.mu.Unlock()
}

// KeyboardCover implements [KeyboardCoverer].
func (w *OffscreenWindow) KeyboardCover() float32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.keyboard
}

// Resize changes the window's size for the next frame.
func (w *OffscreenWindow) Resize(size geom.Size) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.size = size
}

// Place implements [Placer] by taking the size, at the window's zoom, and keeping the anchor.
func (w *OffscreenWindow) Place(anchor geom.Rect, size geom.Size) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.anchor, w.size = anchor, geom.Sz(size.W*w.zoom, size.H*w.zoom)
	return nil
}

// ListenForAccess makes the window want its accessibility tree, as a
// screen reader would, and keep the last one published.
func (w *OffscreenWindow) ListenForAccess() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.access = true
}

// AccessWanted implements [AccessPublisher].
func (w *OffscreenWindow) AccessWanted() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.access
}

// PublishAccess implements [AccessPublisher].
func (w *OffscreenWindow) PublishAccess(t *access.Tree) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tree = t
}

// AccessTree returns the last accessibility tree published.
func (w *OffscreenWindow) AccessTree() *access.Tree {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tree
}

// DragOut implements [DragOuter] by keeping the paths; a test ends the
// drag by sending [DragOutEnded] through Input.
func (w *OffscreenWindow) DragOut(paths []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.out = append(w.out, paths)
	return nil
}

// DraggedOut returns the files of each drag handed to other programs.
func (w *OffscreenWindow) DraggedOut() [][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out
}

// SetOrigin puts the window's top left corner at p on its pretend
// screen, for tests that drag between windows.
func (w *OffscreenWindow) SetOrigin(p geom.Point) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.origin = p
}

// Placement implements [PlacementReader]: the window at its origin on the pretend screen, at its size in device
// pixels, maximized where its pretend frame is.
func (w *OffscreenWindow) Placement() (Placement, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	size := w.size.Point().Mul(w.scale)
	return Placement{
		Bounds:    geom.Rect{Min: w.origin, Max: w.origin.Add(size)},
		Maximized: w.frame != nil && w.frame.IsMaximized,
	}, true
}

// ToScreen implements [Screener].
func (w *OffscreenWindow) ToScreen(p geom.Point) geom.Point {
	w.mu.Lock()
	defer w.mu.Unlock()
	return p.Add(w.origin)
}

// FromScreen implements [Screener].
func (w *OffscreenWindow) FromScreen(p geom.Point) geom.Point {
	w.mu.Lock()
	defer w.mu.Unlock()
	return p.Sub(w.origin)
}

// SetBackground implements [Backgrounder].
func (w *OffscreenWindow) SetBackground(c color.NRGBA) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.under = c
}

// SetOutline sets the shape Outline reports, for a test of a window that cuts its corners. Until it is set the
// window is square and cannot show what is behind it.
func (w *OffscreenWindow) SetOutline(radius, edge float32, blends bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.radius, w.edge, w.blends = radius, edge, blends
}

// Outline implements [Outliner].
func (w *OffscreenWindow) Outline() (radius, edge float32, blends bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.radius, w.edge, w.blends
}

// Background returns the colour last set under the window's frames.
func (w *OffscreenWindow) Background() color.NRGBA {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.under
}

// Transparent implements [Transparent]: an offscreen window blends, as
// a popup does on a display server that blends windows.
func (w *OffscreenWindow) Transparent() bool { return true }

// Anchor returns where the window was last attached, for a popup.
func (w *OffscreenWindow) Anchor() geom.Rect {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.anchor
}

// Size implements [Window].
func (w *OffscreenWindow) Size() geom.Size {
	w.mu.Lock()
	defer w.mu.Unlock()
	return geom.Sz(w.size.W/w.zoom, w.size.H/w.zoom)
}

// Scale implements [Window].
func (w *OffscreenWindow) Scale() float32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.scale * w.zoom
}

// SetZoom implements [Zoomer].
func (w *OffscreenWindow) SetZoom(z float32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.zoom = z
}

// Zoom returns the factor the window's content is zoomed by.
func (w *OffscreenWindow) Zoom() float32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.zoom
}

// RefreshRate implements [Window].
func (w *OffscreenWindow) RefreshRate() float64 { return w.rate }

// Clipboard implements [Window] with a clipboard of the window's own.
func (w *OffscreenWindow) Clipboard() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.clipErr != nil {
		return "", w.clipErr
	}
	return w.clip, nil
}

// SetClipboardError makes reading the clipboard fail with err, as a
// test's stand-in for a clipboard that cannot be read; nil reads it
// again.
func (w *OffscreenWindow) SetClipboardError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clipErr = err
}

// SetClipboard implements [Window].
func (w *OffscreenWindow) SetClipboard(s string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clip = s
	return nil
}

// ClipboardImage implements [ImageClipboard].
func (w *OffscreenWindow) ClipboardImage() ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clipImage, nil
}

// SetClipboardImage implements [ImageClipboard]: it puts a picture, as PNG, on the window's clipboard, where
// [OffscreenWindow.ClipboardImage] reads it back. A test calls it too, as its stand-in for copying one in another
// program, so it takes png as it is without checking it is a PNG, and nil takes the picture off but leaves the
// text. The window keeps a copy, so the caller may reuse png.
func (w *OffscreenWindow) SetClipboardImage(png []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if png == nil {
		w.clipImage = nil
		return nil
	}
	w.clipImage = append([]byte{}, png...)
	return nil
}

// Close implements [Window].
func (w *OffscreenWindow) Close() error { return nil }

// SetCloaked implements [Cloaker]: Cloaked reports it.
func (w *OffscreenWindow) SetCloaked(on bool) {
	w.mu.Lock()
	w.cloaked = on
	w.mu.Unlock()
}

// Cloaked reports whether the window was last cloaked; see [Cloaker].
func (w *OffscreenWindow) Cloaked() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cloaked
}

// Hide implements [Recycler].
func (w *OffscreenWindow) Hide() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hidden = true
	return nil
}

// Show implements [Recycler].
func (w *OffscreenWindow) Show() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hidden = false
	return nil
}

// SetPointerRegion implements [PointerRegioner].
func (w *OffscreenWindow) SetPointerRegion(rects []geom.Rect) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.region = slices.Clone(rects)
}

// PointerRegion returns where the window was last told it takes the pointer, in its logical pixels, and nil where
// it takes it all over.
func (w *OffscreenWindow) PointerRegion() []geom.Rect {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.region)
}

// Hidden reports whether the window is hidden, kept to show again.
func (w *OffscreenWindow) Hidden() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hidden
}

// OffscreenFrame is what an offscreen window made chromeless was asked
// to do, for a test to read.
type OffscreenFrame struct {
	// Native says the pretend system moves and sizes the window itself,
	// as Windows does.
	Native bool
	// Caption and Maximize are the title bar last reported.
	Caption  []geom.Rect
	Maximize geom.Rect
	// Moves counts the moves started, Resizes the edges sized by, and
	// Minimized the minimizes.
	Moves     int
	Resizes   []Edge
	Minimized int
	// IsMaximized is the window's pretend state.
	IsMaximized bool
}

// MakeChromeless makes the window one whose application draws its
// title bar, with a pretend system that moves and sizes it itself when
// native, and leaves that to the engine otherwise.
func (w *OffscreenWindow) MakeChromeless(native bool) *OffscreenFrame {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frame = &OffscreenFrame{Native: native}
	return w.frame
}

// Chromeless implements [Framer].
func (w *OffscreenWindow) Chromeless() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.frame != nil
}

// SetTitleBar implements [Framer].
func (w *OffscreenWindow) SetTitleBar(caption []geom.Rect, maximize geom.Rect) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frame != nil {
		w.frame.Caption, w.frame.Maximize = slices.Clone(caption), maximize
	}
}

// NativeFrame implements [Framer].
func (w *OffscreenWindow) NativeFrame() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.frame != nil && w.frame.Native
}

// StartMove implements [Framer].
func (w *OffscreenWindow) StartMove() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frame != nil {
		w.frame.Moves++
	}
	return nil
}

// StartResize implements [Framer].
func (w *OffscreenWindow) StartResize(e Edge) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frame != nil {
		w.frame.Resizes = append(w.frame.Resizes, e)
	}
	return nil
}

// Minimize implements [Framer].
func (w *OffscreenWindow) Minimize() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frame != nil {
		w.frame.Minimized++
	}
	return nil
}

// SetMaximized implements [Framer]. The pretend system reports no
// change: a test sends [WindowMaximized] as a real one would.
func (w *OffscreenWindow) SetMaximized(on bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frame != nil {
		w.frame.IsMaximized = on
	}
	return nil
}

// Maximized implements [Framer].
func (w *OffscreenWindow) Maximized() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.frame != nil && w.frame.IsMaximized
}

// SetPinned implements [Pinner].
func (w *OffscreenWindow) SetPinned(on bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pinned = on
	return nil
}

// Pinned reports whether the window was last asked to stay above other windows.
func (w *OffscreenWindow) Pinned() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pinned
}

// SetChooser sets what ChooseFiles answers, for a test standing in for
// the user.
func (w *OffscreenWindow) SetChooser(fn func(ChooseOptions) ([]string, error)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chooser = fn
}

// ChooseFiles implements [FileChooser] with the function SetChooser set,
// and returns [ErrNoChooser] without one.
func (w *OffscreenWindow) ChooseFiles(o ChooseOptions) ([]string, error) {
	w.mu.Lock()
	fn := w.chooser
	w.mu.Unlock()
	if fn == nil {
		return nil, ErrNoChooser
	}
	return fn(o)
}

// SetSaver sets what SaveFile answers, for a test standing in for the user.
func (w *OffscreenWindow) SetSaver(fn func(SaveOptions) (string, error)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.saver = fn
}

// SaveFile implements [FileSaver] with the function SetSaver set, and
// returns [ErrNoChooser] without one.
func (w *OffscreenWindow) SaveFile(o SaveOptions) (string, error) {
	w.mu.Lock()
	fn := w.saver
	w.mu.Unlock()
	if fn == nil {
		return "", ErrNoChooser
	}
	return fn(o)
}

// SetLauncher sets what Open and Reveal do, for a test standing in for
// the system.
func (w *OffscreenWindow) SetLauncher(open, reveal func(path string) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.open, w.reveal = open, reveal
}

// Open implements [Launcher] with the function SetLauncher set, and
// returns [ErrNoLauncher] without one.
func (w *OffscreenWindow) Open(path string) error {
	w.mu.Lock()
	fn := w.open
	w.mu.Unlock()
	if fn == nil {
		return ErrNoLauncher
	}
	return fn(path)
}

// Reveal implements [Launcher] with the function SetLauncher set, and
// returns [ErrNoLauncher] without one.
func (w *OffscreenWindow) Reveal(path string) error {
	w.mu.Lock()
	fn := w.reveal
	w.mu.Unlock()
	if fn == nil {
		return ErrNoLauncher
	}
	return fn(path)
}

// SetLinkOpener sets what OpenLink does, for a test standing in for the
// system's browser.
func (w *OffscreenWindow) SetLinkOpener(fn func(url string) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.link = fn
}

// OpenLink implements [LinkOpener] with the function SetLinkOpener set,
// and returns [ErrNoLinkOpener] without one.
func (w *OffscreenWindow) OpenLink(url string) error {
	w.mu.Lock()
	fn := w.link
	w.mu.Unlock()
	if fn == nil {
		return ErrNoLinkOpener
	}
	return fn(url)
}

// SetSharer sets what Share does, for a test standing in for the system.
func (w *OffscreenWindow) SetSharer(fn func(Share) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.share = fn
}

// Share implements [Sharer] with the function SetSharer set, and
// returns [ErrNoSharer] without one.
func (w *OffscreenWindow) Share(s Share) error {
	w.mu.Lock()
	fn := w.share
	w.mu.Unlock()
	if fn == nil {
		return ErrNoSharer
	}
	return fn(s)
}

// SetVibrator sets what Vibrate does, for a test standing in for the
// device.
func (w *OffscreenWindow) SetVibrator(fn func(pattern []time.Duration) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.vibrate = fn
}

// Vibrate implements [Vibrator] with the function SetVibrator set, and
// returns [ErrNoVibrator] without one.
func (w *OffscreenWindow) Vibrate(pattern ...time.Duration) error {
	w.mu.Lock()
	fn := w.vibrate
	w.mu.Unlock()
	if fn == nil {
		return ErrNoVibrator
	}
	return fn(pattern)
}

// SetCompass gives the window a pretend compass, for a test standing in for a device with one: fn hears each call
// to WatchHeading, and the test hands the window its readings as
// [github.com/marrasen/gunim/input.Heading]. Without one, the window has no compass.
func (w *OffscreenWindow) SetCompass(fn func(on bool)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.compass = fn
}

// HasCompass implements [Compass]: the window has one once SetCompass gave it one.
func (w *OffscreenWindow) HasCompass() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.compass != nil
}

// WatchHeading implements [Compass] with the function SetCompass set.
func (w *OffscreenWindow) WatchHeading(on bool) {
	w.mu.Lock()
	fn := w.compass
	w.mu.Unlock()
	if fn != nil {
		fn(on)
	}
}
