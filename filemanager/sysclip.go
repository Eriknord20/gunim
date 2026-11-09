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

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
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
	// outside runs fn with the files the clipboard holds as contents,
	// where it is still as it was at seq, and fails with
	// errClipboardChanged where it is not, or stops being so before a
	// file is opened. fn must stay on the goroutine it is called on.
	outside(seq uint32, fn func(outsideFiles) error) error
	// empty empties the clipboard, where it is still as it was at seq.
	empty(seq uint32)
}

// errClipboardChanged says the clipboard no longer holds what a paste
// began with, which another copy, or a retry long after, would find.
var errClipboardChanged = errors.New("the clipboard changed after the paste began, so what it holds now was not pasted; paste again to take it")

// stillHeld says whether a clipboard whose seq is now still holds what it
// held at seq. A seq of 0 is a system that gives none, which cannot say.
func stillHeld(seq, now uint32) error {
	if seq != 0 && now != seq {
		return errClipboardChanged
	}
	return nil
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

// systemCut is a cut another program left on the system clipboard, as
// it was at seq.
type systemCut struct{ seq uint32 }

// takeCut takes the cut the system clipboard held at seq for a move, and
// reports false where a move is moving it or has moved it. A seq of 0 is
// a system that gives none, where a cut cannot be told from the next:
// every move takes it.
func (h *Hub) takeCut(seq uint32) bool {
	if seq == 0 {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cuts[seq] {
		return false
	}
	if h.cuts == nil {
		h.cuts = map[uint32]bool{}
	}
	h.cuts[seq] = true
	return true
}

// endCut ends the move of the cut at seq. One that moved it keeps it, as
// what it held is gone from where it was, and takes it off the
// clipboard; one that did not lets it be moved again: a move that failed
// partway, too, as what it left can still go. It is safe from any
// goroutine, and the move calls it as it ends rather than once a window
// takes the end, which a window that closed meanwhile never does.
func (h *Hub) endCut(seq uint32, moved bool) {
	if moved {
		systemClip.empty(seq)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.cuts, seq)
}

// cutBusy says a paste of a cut finds it moving, or moved.
func (a *app) cutBusy() {
	a.patch(Notice{Title: "What was cut is moving, or has moved", Body: "If a move of it fails, it can be pasted again.", Kind: "info"})
}

// pasteSystem pastes into here what another program put on the system
// clipboard. It reads the clipboard off the serve loop, as the program
// may take a while to hand it over: Remote Desktop asks the computer it
// shows; and looks for what was cut there too, as it may be on a share.
func (a *app) pasteSystem(here string) {
	fsID := a.fs.ID()
	go func() {
		sf, err := systemClip.files()
		if sf.cut {
			// What a move that failed partway moved is gone from where
			// it was cut; the rest still goes.
			sf.paths = slices.DeleteFunc(sf.paths, func(p string) bool {
				_, err := LocalFS().Lstat(p)
				return errors.Is(err, fs.ErrNotExist)
			})
			if len(sf.paths) == 0 {
				a.post(func() {
					a.patch(Notice{Title: "What was cut is no longer where it was cut", Body: "It moved, or was deleted.", Kind: "info"})
				})
				return
			}
		}
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
		a.startOp(job{kind: OpCopy, srcs: sf.names, dest: here, outside: true, seq: sf.seq},
			"Copying "+whatIn(a.ps, sf.names)+" to "+a.ps.placeName(here))
		return
	}
	if fsID != "" && a.opts.Transfer == nil {
		a.fail("Items cannot go between file systems yet.")
		return
	}
	// What was cut is moved once, as in Explorer. The clipboard keeps it
	// until it has moved, so a move that fails can be pasted again; the
	// hub holds it from the moment a move starts, so a second Ctrl+V, from
	// a held key, another window or a read of the clipboard from before
	// the move ended, does not move the same items again.
	if fsID != "" {
		// Paths from another program are the computer's own.
		var ended func(ok bool)
		if sf.cut {
			if !a.hub.takeCut(sf.seq) {
				a.cutBusy()
				return
			}
			ended = func(ok bool) { a.hub.endCut(sf.seq, ok) }
		}
		a.transfer("", SystemPaths, sf.paths, fsID, here, sf.cut, ended)
		return
	}
	srcs := sf.paths
	if sf.cut {
		a.startOp(job{kind: OpMove, srcs: srcs, dest: here, cut: &systemCut{seq: sf.seq}},
			"Moving "+a.what(srcs)+" to "+a.ps.placeName(here))
		return
	}
	a.startOp(job{kind: OpCopy, srcs: srcs, dest: here}, "Copying "+a.what(srcs)+" to "+a.ps.placeName(here))
}

// sizedReader reads what r holds, a file the clipboard says is size
// bytes long, and fails where it holds fewer or more: a stream the
// program cut short, or memory short of what it said, would else be
// written as the whole file, and replace one there.
type sizedReader struct {
	r          io.Reader
	size, read int64
}

// Read implements [io.Reader].
func (s *sizedReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.read += int64(n)
	switch {
	case err != nil && !errors.Is(err, io.EOF):
		// What failed says why, as a clipboard that changed does, before
		// what came of it.
		return n, err
	case s.read > s.size:
		return n, fmt.Errorf("it holds more than the %d bytes the clipboard says it does", s.size)
	case errors.Is(err, io.EOF) && s.read < s.size:
		return n, fmt.Errorf("it ended after %d of its %d bytes", s.read, s.size)
	}
	return n, err
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
// on the system clipboard as contents, as it was at seq, into dest. Each one that clashes
// with an item there goes as the user says, as in a copy; in a folder
// merged with one there, so does each item inside.
func (r *runner) pasteOutside(names []string, seq uint32, dest string) error {
	if r.env.outside == nil {
		return errors.ErrUnsupported
	}
	return r.env.outside(seq, func(of outsideFiles) error {
		ps := r.env.fs.Paths()
		items := spelledAlike(ps, of.items())
		found := map[string]bool{}
		for _, n := range names {
			found[nameKey(ps, n)] = false
		}
		var picked []int
		for i, it := range items {
			top := topOf(it.name)
			if _, ok := found[nameKey(ps, top)]; !ok {
				continue
			}
			found[nameKey(ps, top)] = true
			for part := range strings.SplitSeq(it.name, "/") {
				if err := checkName(ps, part); err != nil {
					return fmt.Errorf("the clipboard holds %s, which cannot be written here: %w", it.name, err)
				}
			}
			picked = append(picked, i)
			r.p.itemsTotal++
			r.p.bytesTotal += max(it.size, 0)
		}
		for _, n := range names {
			if !found[nameKey(ps, n)] {
				return fmt.Errorf("%s is no longer on the clipboard", n)
			}
		}
		r.tell(true)
		p := outsidePaste{r: r, of: of, items: items, dest: dest,
			to: map[string]string{}, merging: map[string]bool{}, done: map[int]bool{}, landed: map[string][]string{},
			written: map[string][]string{}}
		left := map[string]int{}
		for _, i := range picked {
			left[topOf(items[i].name)]++
		}
		for _, i := range picked {
			if err := p.item(i); err != nil {
				return err
			}
			// An item has landed once all it holds has, so a retry after
			// a failure inside it pastes it again, as after copyAll.
			top := topOf(items[i].name)
			left[top]--
			if left[top] == 0 {
				r.rec.landed = append(r.rec.landed, p.landed[top]...)
			}
		}
		return p.stamp()
	})
}

// outsidePaste is a paste of items held as contents under way. to holds
// where each folder it has reached went, by its name on the clipboard;
// merging the folders that went into one already there, whose items are
// undone on their own; skipped the folders the user skipped; done the
// items pasted, by their index; made the folders it made, to set their
// times once their files are in; landed the names each item at the top
// went to; written the paths the paste wrote, by their looseKey; selfAll
// the answer for every clash between two items of the clipboard, once
// given.
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
	landed  map[string][]string
	written map[string][]string
	selfAll *answer
}

// item pastes items[i], into the folder its parent went to.
func (p *outsidePaste) item(i int) error {
	r, it := p.r, p.items[i]
	if p.done[i] {
		// A folder its first item reached before it.
		return nil
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
	if p.skippedIn(it.name) {
		return p.skip(it)
	}
	if _, ok := p.to[it.name]; ok && it.dir {
		// The clipboard lists the folder twice, or spells it two ways.
		return p.skip(it)
	}
	into := p.dest
	if parent != "" {
		var err error
		if into, err = p.folder(parent); err != nil {
			return err
		}
		if into == "" {
			// The user skipped the folder, which the clipboard lists
			// after this item.
			return p.skip(it)
		}
	}
	r.p.current = it.name
	dst := ps.Join(into, base)
	top := parent == ""
	// An item whose folder went into one already there is undone on its
	// own, as copyDir does in a merge. One in a folder the paste made is
	// undone with that folder.
	own := top || p.merging[parent]
	merge, replace := false, false
	if info, err := r.env.fs.Lstat(dst); err == nil || !errors.Is(err, fs.ErrNotExist) {
		switch {
		case !own && err == nil && it.dir && realDir(info):
			// A folder this paste made, which the clipboard lists again
			// under a name the file system takes for the same one.
			merge = true
		default:
			// What is there was there before, or this paste wrote it a
			// moment ago under a name the file system takes for this
			// one's, as it always did in a folder the paste made: either
			// way the user says what goes, but an answer for all of one
			// kind is not one for the other.
			all := p.allFor(own, dst)
			var skip bool
			dst, skip, merge, replace, err = r.resolveAs(it.name, dst, it.dir, outsideFacts(it), all)
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
		p.merging[it.name] = own
	case it.dir:
		if err := r.env.fs.Mkdir(dst, 0o755); err != nil {
			return fmt.Errorf("making %s: %w", ps.Show(dst), err)
		}
		p.made = append(p.made, i)
		p.written[looseKey(dst)] = append(p.written[looseKey(dst)], dst)
	default:
		in, err := p.of.open(i)
		if err != nil {
			return fmt.Errorf("reading %s from the clipboard: %w", it.name, err)
		}
		var src io.Reader = in
		if it.size >= 0 {
			src = &sizedReader{r: in, size: it.size}
		}
		werr := r.writeFile(src, it.name, dst, outsideInfo{it})
		if err := errors.Join(werr, in.Close()); err != nil {
			return err
		}
		p.written[looseKey(dst)] = append(p.written[looseKey(dst)], dst)
	}
	if it.dir {
		p.to[it.name] = dst
	}
	r.p.items++
	r.tell(false)
	if !own {
		return nil
	}
	switch {
	case replace:
		r.rec.replaced++
	case !merge:
		r.did(it.name, dst)
	}
	if top {
		p.landed[it.name] = append(p.landed[it.name], ps.Base(dst))
	}
	return nil
}

// allFor returns the answer for all that a clash at dst takes: the
// clipboard's own where this paste wrote dst, and the one for what was
// there before where it wrote nothing the loosest file system takes for
// dst. Where it wrote such a path under another spelling, only the user
// can say which the clash is with, as file systems keep and compare
// names in more ways than a name shows: it returns nil, and the user is
// asked about that clash alone, so a name taken too loosely costs a
// question and never a file. A short name Windows gives a file is not
// known for it, as only Windows can say what a short name stands for.
func (p *outsidePaste) allFor(own bool, dst string) **answer {
	if !own {
		return &p.selfAll
	}
	written := p.written[looseKey(dst)]
	switch {
	case len(written) == 0:
		return &p.r.all
	case slices.Contains(written, dst):
		return &p.selfAll
	}
	return nil
}

// looseKey returns path with each name in it taken as loosely as a file
// system may take it, as far as a name shows: as looseName gives it,
// then case-folded in full, as a file system that takes ß for ss does,
// and in one Unicode form, as a Mac takes the composed and decomposed
// spellings of a name for one. The fold is of the decomposed name, as
// Unicode's canonical caseless match has it, and drops the dot above,
// which strings.ToLower drops from İ but not from its decomposed form. It
// is made from what looseName gives, so it never keeps apart two names
// looseName joins: a name taken too loosely costs a question, in allFor,
// where one taken too strictly could cost a file.
func looseKey(path string) string {
	names := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	for i, n := range names {
		folded := cases.Fold().String(norm.NFD.String(looseName(n)))
		names[i] = norm.NFC.String(strings.ReplaceAll(folded, "\u0307", ""))
	}
	return strings.Join(names, "/")
}

// looseName returns name as the loosest file system takes it: in lower
// case, without the dots and spaces Windows drops from the end of one.
func looseName(name string) string { return strings.ToLower(strings.TrimRight(name, ". ")) }

// topOf returns the name of the item at the top that name is in, or name.
func topOf(name string) string {
	top, _, _ := strings.Cut(name, "/")
	return top
}

// folder returns where the folder called name on the clipboard went, or
// "" where the user skipped it. A program may leave a folder out and list
// only the files in it; such a folder is made, in the folder its own
// parent went to, when its first item comes.
func (p *outsidePaste) folder(name string) (string, error) {
	if to, ok := p.to[name]; ok {
		return to, nil
	}
	i := slices.IndexFunc(p.items, func(it outsideItem) bool { return it.name == name })
	if i < 0 {
		p.items = append(p.items, outsideItem{name: name, dir: true, size: -1})
		p.r.p.itemsTotal++
		i = len(p.items) - 1
	}
	if err := p.item(i); err != nil {
		return "", err
	}
	if to, ok := p.to[name]; ok {
		return to, nil
	}
	if p.skippedIn(name) {
		return "", nil
	}
	return "", fmt.Errorf("the clipboard holds %s as a file, and items inside it", name)
}

// skippedIn reports whether name is a folder the user skipped, or is in
// one.
func (p *outsidePaste) skippedIn(name string) bool {
	return slices.ContainsFunc(p.skipped, func(s string) bool { return name == s || strings.HasPrefix(name, s+"/") })
}

// spelledAlike returns items with each folder on the way to an item
// spelled as the first item to reach it spells it, where paths of style
// ps ignore case and drop dots and spaces from the end of a name: a
// folder a program spells two ways goes to one place.
func spelledAlike(ps PathStyle, items []outsideItem) []outsideItem {
	out := slices.Clone(items)
	if !ps.ignoresCase() {
		return out
	}
	first := map[string]string{}
	for i := range out {
		var name, key string
		for part := range strings.SplitSeq(out[i].name, "/") {
			if name != "" {
				name, key = name+"/", key+"/"
			}
			name, key = name+part, key+looseName(part)
			if s, ok := first[key]; ok {
				name = s
			} else {
				first[key] = name
			}
		}
		out[i].name = name
	}
	return out
}

// nameKey returns name as paths of style ps compare it: where they ignore
// case, as looseName gives it, as spelledAlike spells items alike.
func nameKey(ps PathStyle, name string) string {
	if ps.ignoresCase() {
		return looseName(name)
	}
	return name
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
