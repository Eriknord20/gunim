package filemanager

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
)

// What another program cuts or copies reaches a window through the
// system's clipboard. Explorer puts paths there. Remote Desktop, and
// Explorer copying out of a zip, put the files' contents there instead,
// with no path to read them at: the program hands each file over when
// it is asked for it.

// sysClip is the system's clipboard, as Paste reads it.
type sysClip interface {
	// seq changes whenever the clipboard does, and is 0 where the
	// system does not say.
	seq() uint32
	// hasFiles reports whether the clipboard holds files, as paths or
	// as contents, without reading them.
	hasFiles() bool
	// files reads the files the clipboard holds.
	files() (systemFiles, error)
	// outside runs fn with the files the clipboard holds as contents.
	// fn must stay on the goroutine it is called on.
	outside(fn func(outsideFiles) error) error
	// empty empties the clipboard, where it is still as it was at seq.
	empty(seq uint32)
}

// systemClip is the system's clipboard; a test puts one of its own in.
var systemClip sysClip = osClip{}

// systemFiles is what another program left on the system clipboard as
// files: paths on the computer, cut where cut is set; or, where paths is
// empty, the names of the items held as contents only. seq is the
// clipboard's seq as it was read.
type systemFiles struct {
	paths []string
	cut   bool
	names []string
	seq   uint32
}

// outsideItem is an item another program put on the system clipboard
// as its contents. name is where it goes in the folder it is pasted
// into, with "/" between its folders. size is -1 where the program does
// not say, and mod is zero where it does not say.
type outsideItem struct {
	name string
	dir  bool
	size int64
	mod  time.Time
}

// outsideFiles is what the system clipboard holds as contents.
type outsideFiles interface {
	items() []outsideItem
	// open opens items()[i], a file, to read.
	open(i int) (io.ReadCloser, error)
}

// fileDescriptorSize is the size of a FILEDESCRIPTORW, the description of
// one item a FILEGROUPDESCRIPTORW holds.
const fileDescriptorSize = 592

// What the flags of a FILEDESCRIPTORW say it holds, and the attribute of
// a folder.
const (
	fdAttributes  = 0x04
	fdWritesTime  = 0x20
	fdFileSize    = 0x40
	attrDirectory = 0x10
)

// fileDescriptors reads the items a FILEGROUPDESCRIPTORW describes, as
// Windows puts one on the clipboard in the format "FileGroupDescriptorW".
// Each item's place in the result is its index in "FileContents".
func fileDescriptors(b []byte) ([]outsideItem, error) {
	if len(b) < 4 {
		return nil, errors.New("the list of files on the clipboard is cut short")
	}
	n := int(binary.LittleEndian.Uint32(b))
	if n < 0 || n > (len(b)-4)/fileDescriptorSize {
		return nil, errors.New("the list of files on the clipboard is cut short")
	}
	items := make([]outsideItem, n)
	for i := range items {
		d := b[4+i*fileDescriptorSize:][:fileDescriptorSize]
		flags := binary.LittleEndian.Uint32(d)
		attrs := binary.LittleEndian.Uint32(d[36:])
		written := binary.LittleEndian.Uint64(d[56:])
		high, low := binary.LittleEndian.Uint32(d[64:]), binary.LittleEndian.Uint32(d[68:])
		name := make([]uint16, 260)
		for j := range name {
			name[j] = binary.LittleEndian.Uint16(d[72+2*j:])
		}
		if end := slices.Index(name, 0); end >= 0 {
			name = name[:end]
		}
		it := outsideItem{name: strings.ReplaceAll(string(utf16.Decode(name)), `\`, "/"), size: -1}
		if flags&fdAttributes != 0 {
			it.dir = attrs&attrDirectory != 0
		}
		if flags&fdFileSize != 0 && !it.dir {
			it.size = int64(high)<<32 | int64(low)
		}
		if flags&fdWritesTime != 0 && written != 0 {
			// A FILETIME counts 100 ns from 1601.
			it.mod = time.Unix(0, (int64(written)-116444736000000000)*100)
		}
		items[i] = it
	}
	return items, nil
}

// topNames returns the names of the items at the top of items, each
// once, in the order they first come.
func topNames(items []outsideItem) []string {
	var names []string
	for _, it := range items {
		top, _, _ := strings.Cut(it.name, "/")
		if !slices.Contains(names, top) {
			names = append(names, top)
		}
	}
	return names
}

// systemClipNewer reports whether Paste takes what another program put
// on the system clipboard rather than what a window cut or copied: where
// the clipboard holds files, and changed after the window's last cut or
// copy, or the window has nothing to paste.
func (a *app) systemClipNewer() bool {
	if !systemClip.hasFiles() {
		return false
	}
	if len(a.ops.clip) == 0 && len(a.ops.away.paths) == 0 {
		return true
	}
	a.hub.mu.Lock()
	seq := a.hub.sysSeq
	a.hub.mu.Unlock()
	return systemClip.seq() != seq
}

// pasteSystem pastes into here what another program put on the system
// clipboard. It reads the clipboard off the serve loop, as the program
// may take a while to hand it over: Remote Desktop asks the computer it
// shows.
func (a *app) pasteSystem(here string) {
	fsID := a.fs.ID()
	go func() {
		sf, err := systemClip.files()
		a.post(func() { a.pastedSystem(fsID, here, sf, err) })
	}()
}

// pastedSystem pastes sf into the folder here on the file system of ID
// fsID, once read; err is why it could not be read.
func (a *app) pastedSystem(fsID, here string, sf systemFiles, err error) {
	switch {
	case err != nil:
		a.fail("Couldn't read the clipboard: " + err.Error())
		return
	case fsID != a.fs.ID():
		a.fail("The window went to another file system before the clipboard was read, so nothing was pasted.")
		return
	case len(sf.paths) == 0 && len(sf.names) == 0:
		a.patch(Notice{Title: "The clipboard holds no files", Kind: "info"})
		return
	}
	if at := a.archiveOf(here, true); at != "" {
		a.refuseArchive(at)
		return
	}
	if len(sf.paths) == 0 {
		a.startOp(job{kind: OpCopy, srcs: sf.names, dest: here, outside: true},
			"Copying "+whatIn(a.ps, sf.names)+" to "+a.ps.placeName(here))
		return
	}
	if fsID != "" && a.opts.Transfer == nil {
		a.fail("Items cannot go between file systems yet.")
		return
	}
	if sf.cut {
		// What was cut is pasted once, as in Explorer.
		systemClip.empty(sf.seq)
	}
	if fsID != "" {
		// Paths from another program are the computer's own.
		a.transfer("", SystemPaths, sf.paths, fsID, here, sf.cut)
		return
	}
	srcs := sf.paths
	if sf.cut {
		a.startOp(job{kind: OpMove, srcs: srcs, dest: here}, "Moving "+a.what(srcs)+" to "+a.ps.placeName(here))
		return
	}
	a.startOp(job{kind: OpCopy, srcs: srcs, dest: here}, "Copying "+a.what(srcs)+" to "+a.ps.placeName(here))
}

// outsideFacts says what it is, as the dialog that asks whether to
// replace an item compares two.
func outsideFacts(it outsideItem) string {
	if it.dir || it.size >= 0 {
		return ItemFacts(it.size, it.dir, it.mod)
	}
	if it.mod.IsZero() {
		return "On the clipboard"
	}
	return "On the clipboard · " + it.mod.Format("2 Jan 2006 15:04")
}

// outsideInfo describes an item pasted as contents, as writeFile takes
// it.
type outsideInfo struct{ it outsideItem }

// Name implements [fs.FileInfo].
func (o outsideInfo) Name() string { return o.it.name }

// Size implements [fs.FileInfo]: 0 where the program does not say.
func (o outsideInfo) Size() int64 { return max(o.it.size, 0) }

// Mode implements [fs.FileInfo]. The clipboard carries no mode, so the
// file takes the one a new file usually has.
func (o outsideInfo) Mode() fs.FileMode {
	if o.it.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}

// ModTime implements [fs.FileInfo]: now, where the program does not say.
func (o outsideInfo) ModTime() time.Time {
	if o.it.mod.IsZero() {
		return time.Now()
	}
	return o.it.mod
}

// IsDir implements [fs.FileInfo].
func (o outsideInfo) IsDir() bool { return o.it.dir }

// Sys implements [fs.FileInfo]: nil, as nothing of the system describes it.
func (o outsideInfo) Sys() any { return nil }

// pasteOutside copies the items called names, which another program put
// on the system clipboard as contents, into dest. Each one that clashes
// with an item there goes as the user says, as in a copy; in a folder
// merged with one there, so does each item inside.
func (r *runner) pasteOutside(names []string, dest string) error {
	if r.env.outside == nil {
		return errors.ErrUnsupported
	}
	return r.env.outside(func(of outsideFiles) error {
		ps := r.env.fs.Paths()
		items := of.items()
		var picked []int
		for i, it := range items {
			parts := strings.Split(it.name, "/")
			if !slices.Contains(names, parts[0]) {
				continue
			}
			for _, part := range parts {
				if err := checkName(ps, part); err != nil {
					return fmt.Errorf("the clipboard holds %s, which cannot be written here: %w", it.name, err)
				}
			}
			picked = append(picked, i)
			r.p.itemsTotal++
			r.p.bytesTotal += max(it.size, 0)
		}
		for _, n := range names {
			if !slices.ContainsFunc(picked, func(i int) bool { return strings.Split(items[i].name, "/")[0] == n }) {
				return fmt.Errorf("%s is no longer on the clipboard", n)
			}
		}
		r.tell(true)
		p := outsidePaste{r: r, of: of, items: items, dest: dest, to: map[string]string{}}
		for _, i := range picked {
			if err := p.item(i); err != nil {
				return err
			}
		}
		return p.stamp()
	})
}

// outsidePaste is a paste of items held as contents under way. to holds
// where each folder it has reached went, by its name on the clipboard;
// merging the folders that went into one already there, whose items may
// clash; skipped the folders the user skipped; done the items pasted, by
// their index; and made the folders it made, to set their times once
// their files are in.
type outsidePaste struct {
	r       *runner
	of      outsideFiles
	items   []outsideItem
	dest    string
	to      map[string]string
	merging map[string]bool
	skipped []string
	done    map[int]bool
	made    []int
}

// item pastes items[i], into the folder its parent went to.
func (p *outsidePaste) item(i int) error {
	r, it := p.r, p.items[i]
	if p.done[i] {
		// A folder its first item reached before it.
		return nil
	}
	if p.done == nil {
		p.done = map[int]bool{}
	}
	p.done[i] = true
	if err := r.ctx.Err(); err != nil {
		return err
	}
	ps := r.env.fs.Paths()
	parent, base := "", it.name
	if at := strings.LastIndex(it.name, "/"); at >= 0 {
		parent, base = it.name[:at], it.name[at+1:]
	}
	if slices.ContainsFunc(p.skipped, func(s string) bool { return parent == s || strings.HasPrefix(parent, s+"/") }) {
		return p.skip(it)
	}
	into := p.dest
	if parent != "" {
		var err error
		if into, err = p.folder(parent); err != nil {
			return err
		}
	}
	r.p.current = it.name
	dst := ps.Join(into, base)
	top := parent == ""
	merge, replace := false, false
	if top || p.merging[parent] {
		if _, err := r.env.fs.Lstat(dst); err == nil || !errors.Is(err, fs.ErrNotExist) {
			var skip bool
			var err error
			dst, skip, merge, replace, err = r.resolveAs(it.name, dst, it.dir, outsideFacts(it))
			if err != nil {
				return err
			}
			if skip {
				if it.dir {
					p.skipped = append(p.skipped, it.name)
				}
				return p.skip(it)
			}
		}
	}
	switch {
	case it.dir && merge:
		if p.merging == nil {
			p.merging = map[string]bool{}
		}
		p.merging[it.name] = true
	case it.dir:
		if err := r.env.fs.Mkdir(dst, 0o755); err != nil {
			return fmt.Errorf("making %s: %w", ps.Show(dst), err)
		}
		p.made = append(p.made, i)
	default:
		in, err := p.of.open(i)
		if err != nil {
			return fmt.Errorf("reading %s from the clipboard: %w", it.name, err)
		}
		werr := r.writeFile(in, it.name, dst, outsideInfo{it})
		if err := errors.Join(werr, in.Close()); err != nil {
			return err
		}
	}
	if it.dir {
		p.to[it.name] = dst
	}
	r.p.items++
	r.tell(false)
	if !top {
		return nil
	}
	switch {
	case replace:
		r.rec.replaced++
	case !merge:
		r.did(it.name, dst)
	}
	r.rec.landed = append(r.rec.landed, ps.Base(dst))
	return nil
}

// folder returns where the folder called name on the clipboard went. A
// program may leave a folder out and list only the files in it; such a
// folder is made, in the folder its own parent went to, when its first
// item comes.
func (p *outsidePaste) folder(name string) (string, error) {
	if to, ok := p.to[name]; ok {
		return to, nil
	}
	for i, it := range p.items {
		if it.name == name {
			if err := p.item(i); err != nil {
				return "", err
			}
			return p.to[name], nil
		}
	}
	err := p.itemOf(outsideItem{name: name, dir: true, size: -1})
	return p.to[name], err
}

// itemOf pastes it, a folder the clipboard leaves out, as the item after
// the ones it holds.
func (p *outsidePaste) itemOf(it outsideItem) error {
	p.items = append(p.items, it)
	p.r.p.itemsTotal++
	return p.item(len(p.items) - 1)
}

// skip counts it as done, the user having skipped it or the folder it
// is in.
func (p *outsidePaste) skip(it outsideItem) error {
	p.r.p.items++
	p.r.p.bytes += max(it.size, 0)
	p.r.tell(false)
	return nil
}

// stamp gives the folders the paste made the times the clipboard says,
// now that nothing more goes in them.
func (p *outsidePaste) stamp() error {
	st, ok := optional[Stamper](p.r.env.fs)
	if !ok {
		return nil
	}
	for _, i := range slices.Backward(p.made) {
		it := p.items[i]
		if it.mod.IsZero() {
			continue
		}
		if err := st.Chtimes(p.to[it.name], it.mod); err != nil {
			return fmt.Errorf("setting the time of %s: %w", p.r.env.fs.Paths().Show(p.to[it.name]), err)
		}
	}
	return nil
}
