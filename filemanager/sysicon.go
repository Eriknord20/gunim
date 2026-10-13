package filemanager

import (
	"image"
	"strings"
	"sync"

	"github.com/marrasen/gunim/paint"
)

// The icons Windows shows for files, in place of the window's own, as the
// user chooses in the View menu. An icon is read once per kind of file,
// by its extension, and so never from the file: one kept online is not
// downloaded. A program, a shortcut and an icon file carry their own,
// read from this computer's file itself.

// SystemIcon carries the icon Windows shows for the items of Key, large
// for a tile and small for a row, or nothing when it could not be read.
type SystemIcon struct {
	Key          string
	Large, Small *paint.Image
}

// iconsHere says the system gives icons for files, and systemIconOf
// reads one; a test sets them.
var (
	iconsHere    = systemIconsHere
	systemIconOf = systemIcon
)

// sysIcons are the icons read, for every window of the program: a kind
// of file's icon is the same in each. A file's own icon, as a program's,
// is kept among the latest mostOwn alone, as a folder of them would
// otherwise fill the memory; one that could not be read is read again.
var sysIcons struct {
	sync.Mutex
	got  map[string]SystemIcon
	own  []string
	busy map[string][]func(SystemIcon)
	// kinds and files are the keys waiting to be read: a kind's, and a
	// file's, each read on a goroutine of its own, so a file whose icon is
	// slow to come, as a shortcut to a share that is gone, holds up no
	// kind's.
	kinds, files iconQueue
}

// mostOwn is how many files' own icons are kept.
const mostOwn = 512

// iconQueue is keys waiting to be read, which never makes the one that
// adds a key wait.
type iconQueue struct {
	keys []string
	wake chan struct{}
}

// iconKey names the icon of entry e in the folder dir: its kind's, or for
// a file of this computer that carries its own, the file's.
func (a *app) iconKey(dir string, e entry) string {
	if e.Dir {
		return "dir"
	}
	ext := ""
	if i := strings.LastIndexByte(e.Name, '.'); i > 0 {
		ext = strings.ToLower(e.Name[i:])
	}
	if ownIcon(ext) && a.fs.ID() == "" && !e.Online && e.Kind != KindLink && !e.Broken {
		return "file:" + a.ps.Join(dir, e.Name) + "@" + e.Mod.String()
	}
	return "ext:" + ext
}

// systemIconsOn reports whether the window shows Windows' icons.
func (a *app) systemIconsOn() bool {
	return iconsHere && (a.prefs.SystemIcons == nil || *a.prefs.SystemIcons)
}

// keyIcons names the icons of the entries es of the folder dir, when the
// window shows Windows' icons, and asks for those it has not sent.
func (a *app) keyIcons(dir string, es []entry) {
	a.nameIcons(dir, es)
	a.sendIcons(es)
}

// nameIcons names the icons of the entries es of the folder dir, when the
// window shows Windows' icons, asking for none: the tree under a folder
// can hold a great many, and they are asked for as their rows show.
func (a *app) nameIcons(dir string, es []entry) {
	on := a.systemIconsOn()
	for i := range es {
		es[i].iconKey = ""
		if on {
			es[i].iconKey = a.iconKey(dir, es[i])
		}
	}
}

// sendIcons asks for the icons of es the window has not been sent.
func (a *app) sendIcons(es []entry) {
	for i := range es {
		if k := es[i].iconKey; k != "" && !a.iconsSent[k] {
			a.iconsSent[k] = true
			a.wantIcon(k)
		}
	}
}

// wantIcon sends the icon of key to the window, read now unless it was
// before. It runs on the window's serve loop, and never waits there.
func (a *app) wantIcon(key string) {
	sysIcons.Lock()
	if ic, ok := sysIcons.got[key]; ok {
		sysIcons.Unlock()
		a.patch(ic)
		return
	}
	send := func(ic SystemIcon) { a.post(func() { a.patch(ic) }) }
	if waiting, ok := sysIcons.busy[key]; ok {
		sysIcons.busy[key] = append(waiting, send)
		sysIcons.Unlock()
		return
	}
	if sysIcons.busy == nil {
		sysIcons.busy, sysIcons.got = map[string][]func(SystemIcon){}, map[string]SystemIcon{}
		sysIcons.kinds.wake, sysIcons.files.wake = make(chan struct{}, 1), make(chan struct{}, 1)
		go readIcons(&sysIcons.kinds)
		go readIcons(&sysIcons.files)
	}
	sysIcons.busy[key] = []func(SystemIcon){send}
	q := &sysIcons.kinds
	if strings.HasPrefix(key, "file:") {
		q = &sysIcons.files
	}
	q.keys = append(q.keys, key)
	sysIcons.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// readIcons reads the icons q waits for, one at a time, for as long as
// the program runs.
func readIcons(q *iconQueue) {
	for range q.wake {
		for {
			sysIcons.Lock()
			if len(q.keys) == 0 {
				sysIcons.Unlock()
				break
			}
			key := q.keys[0]
			q.keys = q.keys[1:]
			sysIcons.Unlock()
			ic := readIcon(key)
			sysIcons.Lock()
			keep(key, ic)
			waiting := sysIcons.busy[key]
			delete(sysIcons.busy, key)
			sysIcons.Unlock()
			for _, send := range waiting {
				send(ic)
			}
		}
	}
}

// keep keeps the icon of key, read as ic, under sysIcons' lock.
func keep(key string, ic SystemIcon) {
	if !strings.HasPrefix(key, "file:") {
		sysIcons.got[key] = ic
		return
	}
	if ic.Large == nil {
		return
	}
	sysIcons.got[key] = ic
	sysIcons.own = append(sysIcons.own, key)
	if len(sysIcons.own) > mostOwn {
		delete(sysIcons.got, sysIcons.own[0])
		sysIcons.own = sysIcons.own[1:]
	}
}

// readIcon reads the icon key names.
func readIcon(key string) SystemIcon {
	var large, small image.Image
	var err error
	switch {
	case key == "dir":
		large, small, err = systemIconOf("", "", true)
	case strings.HasPrefix(key, "ext:"):
		large, small, err = systemIconOf("", strings.TrimPrefix(key, "ext:"), false)
	case strings.HasPrefix(key, "file:"):
		path := strings.TrimPrefix(key, "file:")
		if at := strings.LastIndexByte(path, '@'); at >= 0 {
			path = path[:at]
		}
		large, small, err = systemIconOf(path, "", false)
	}
	ic := SystemIcon{Key: key}
	if err == nil && large != nil && small != nil {
		ic.Large, ic.Small = paint.NewImage(large), paint.NewImage(small)
	}
	return ic
}

// clearIcon holds the place of an icon not read.
var clearIcon = sync.OnceValue(func() *paint.Image { return paint.NewImage(image.NewNRGBA(image.Rect(0, 0, 1, 1))) })
