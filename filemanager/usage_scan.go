package filemanager

import (
	"cmp"
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// The bounds of a disk usage scan: how many folders it reads at once,
// which hides the time a server takes to answer, and how many items it
// keeps at most, as each costs memory.
const (
	scanReaders  = 8
	scanMostKept = 5_000_000
)

// unode is one item of the tree a disk usage scan reads: a file, or a
// folder, which d holds the rest of. Its size is its own for a file, and
// all under it for a folder. A drive holds millions of files, so a file
// keeps no more than it needs: what a cloud provider has of it follows
// from its cloud state.
type unode struct {
	name   string
	parent *unode
	d      *udir
	size   int64
	tint   Tint
	cloud  CloudState
	// odd marks a link, or an item that could not be read: a file of no
	// size and of no type.
	odd bool
}

// udir is what a folder of the tree has beyond a file.
type udir struct {
	kids []*unode
	// dirs are the folders among kids, and top the largest files among
	// them by each measure, largest first, once asked for: a folder's
	// files do not change once it is read.
	dirs []*unode
	top  [3][]*unode
	// online, local and pinned are the bytes under it a cloud provider
	// keeps online only, has on this device, and always keeps here.
	online, local, pinned int64
	files, folders        int32
	// pending counts the folders under it, itself too, not yet read.
	pending int32
	// err says why it could not be read, and skipped why it was not.
	err, skipped string
	// tints are the bytes of each group of files under it.
	tints [tintCount]int64
}

// tintCount is how many groups of files there are.
const tintCount = int(TintProgram) + 1

// utype is the files of one type a scan found.
type utype struct {
	ext          string
	tint         Tint
	size, online int64
	files        int
}

// scanner reads all a folder holds, several folders at once, into a
// tree. Everything on it is the scanner's lock's, but the fields set
// before it starts.
type scanner struct {
	fsys     FS
	ps       PathStyle
	root     string
	vols     VolumeNamer
	vol      string
	cloud    CloudReporter
	online   OnlineReporter
	hasCloud bool

	mu    sync.Mutex
	wake  *sync.Cond
	tree  *unode
	types map[string]*utype
	queue []scanJob
	busy  int
	kept  int
	errs  int
	first string
	// sawCloud says some file is with a cloud provider.
	sawCloud bool
	stopped  string
	done     bool
	start    time.Time
	took     time.Duration
	// gen counts the changes to the tree.
	gen int
}

// scanJob is a folder to read: its node and its whole path.
type scanJob struct {
	n    *unode
	path string
}

// newScanner returns a scanner of the folder root on fsys, not started.
func newScanner(fsys FS, root string) *scanner {
	s := &scanner{fsys: fsys, ps: fsys.Paths(), root: root, types: map[string]*utype{}}
	s.wake = sync.NewCond(&s.mu)
	s.vols, _ = optional[VolumeNamer](fsys)
	if s.ps == DrivePaths && root == "/" {
		// The list of the drives, each its own volume: all are read.
		s.vols = nil
	}
	if s.vols != nil {
		s.vol, _ = s.vols.Volume(root)
	}
	if c, ok := optional[CloudReporter](fsys); ok {
		s.cloud, s.hasCloud = c, true
	} else if o, ok := optional[OnlineReporter](fsys); ok {
		s.online, s.hasCloud = o, true
	}
	name := s.ps.Base(root)
	if strings.Trim(name, `/\`) == "" {
		// A drive or a share: named by its whole path.
		name = ""
	}
	s.tree = &unode{name: name, d: &udir{pending: 1}}
	s.start = time.Now()
	s.queue = []scanJob{{n: s.tree, path: root}}
	return s
}

// run reads the tree until it is read or ctx ends, and then calls done.
func (s *scanner) run(ctx context.Context, done func()) {
	stop := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		// Said at once, though a reader may wait on a server a while yet.
		if !s.done && s.stopped == "" {
			s.stopped = "Stopped before the end"
			s.took = time.Since(s.start)
		}
		s.wake.Broadcast()
		s.mu.Unlock()
	})
	var wg sync.WaitGroup
	for range scanReaders {
		wg.Go(func() { s.read(ctx) })
	}
	wg.Wait()
	stop()
	s.mu.Lock()
	s.done = true
	if s.stopped == "" || s.took == 0 {
		s.took = time.Since(s.start)
	}
	if ctx.Err() != nil && s.stopped == "" {
		s.stopped = "Stopped before the end"
	}
	s.gen++
	s.mu.Unlock()
	done()
}

// read reads folders off the queue until none is left or ctx ends.
func (s *scanner) read(ctx context.Context) {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && s.busy > 0 && ctx.Err() == nil {
			s.wake.Wait()
		}
		if len(s.queue) == 0 || ctx.Err() != nil || s.stopped != "" {
			s.wake.Broadcast()
			s.mu.Unlock()
			return
		}
		j := s.queue[0]
		s.queue[0] = scanJob{}
		s.queue = s.queue[1:]
		s.busy++
		s.mu.Unlock()

		kids, sub, err := s.readDir(ctx, j)

		s.mu.Lock()
		s.busy--
		if ctx.Err() == nil {
			s.take(j, kids, sub, err)
		}
		s.wake.Broadcast()
		s.mu.Unlock()
	}
}

// systemTrees are the folders under / on a Unix system that hold no
// files on a disk, which a scan of / leaves out when it cannot tell
// their drives apart.
var systemTrees = []string{"proc", "sys", "dev", "run"}

// readDir reads the folder of j: its items, and the folders among them
// to read next.
func (s *scanner) readDir(ctx context.Context, j scanJob) ([]*unode, []scanJob, error) {
	ds, err := s.fsys.ReadDir(ctx, j.path)
	if err != nil {
		return nil, nil, err
	}
	kids := make([]*unode, 0, len(ds))
	var sub []scanJob
	for _, d := range ds {
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		n := &unode{name: d.Name(), parent: j.n}
		switch {
		case err != nil:
			// Counted as a file of no size.
			n.odd = true
		case info.IsDir() && !isLink(info):
			n.d = &udir{}
			path := s.ps.Join(j.path, n.name)
			n.d.skipped = s.skip(j, path)
			if n.d.skipped == "" {
				n.d.pending = 1
				sub = append(sub, scanJob{n: n, path: path})
			}
			if s.cloud != nil {
				n.cloud = s.cloud.Cloud(j.path, info)
			}
		case isLink(info):
			// A link takes next to no room, and is not followed.
			n.odd = true
		default:
			n.size = info.Size()
			n.tint = tintOfName(n.name)
			switch {
			case s.cloud != nil:
				n.cloud = s.cloud.Cloud(j.path, info)
			case s.online != nil && s.online.OnlineOnly(info):
				n.cloud = CloudOnline
			}
		}
		kids = append(kids, n)
	}
	return kids, sub, nil
}

// skip says why the folder at path, in j's, is not read, or "" to read it.
func (s *scanner) skip(j scanJob, path string) string {
	if s.vols != nil && s.vol != "" {
		if v, err := s.vols.Volume(path); err == nil && v != s.vol {
			return "On another drive"
		}
	}
	unix := s.ps == SlashPaths || s.ps == SystemPaths && runtime.GOOS != "windows"
	if unix && j.n == s.tree && s.root == "/" && slices.Contains(systemTrees, s.ps.Base(path)) {
		return "Kept by the system, not on a disk"
	}
	return ""
}

// take puts the items read of j's folder in the tree, with the folders
// to read next, or the error that stopped the reading.
func (s *scanner) take(j scanJob, kids []*unode, sub []scanJob, err error) {
	s.gen++
	n := j.n
	if err != nil {
		n.d.err = err.Error()
		s.errs++
		if s.first == "" {
			s.first = s.ps.Show(j.path) + ": " + n.d.err
		}
	}
	if s.kept+len(kids) > scanMostKept {
		kids = kids[:max(0, scanMostKept-s.kept)]
		keep := make(map[*unode]bool, len(kids))
		for _, k := range kids {
			keep[k] = true
		}
		sub = slices.DeleteFunc(sub, func(j scanJob) bool { return !keep[j.n] })
		s.stopped = "Stopped at " + count(scanMostKept) + " items"
	}
	s.kept += len(kids)
	n.d.kids = kids
	var size, online, local, pinned int64
	var files, folders int32
	var tints [tintCount]int64
	for _, k := range kids {
		if k.d != nil {
			folders++
			n.d.dirs = append(n.d.dirs, k)
			continue
		}
		files++
		size += k.size
		on, lo, pin := k.clouds()
		online += on
		local += lo
		pinned += pin
		tints[k.tint] += k.size
		if k.cloud != CloudNone {
			s.sawCloud = true
		}
		if k.odd {
			continue
		}
		ext := typeExt(k.name)
		t := s.types[ext]
		if t == nil {
			t = &utype{ext: ext, tint: k.tint}
			s.types[ext] = t
		}
		t.size += k.size
		t.online += on
		t.files++
	}
	// Each folder under it waits to be read, and it has been.
	pending := int32(len(sub)) - 1
	for p := n; p != nil; p = p.parent {
		p.size += size
		p.d.online += online
		p.d.local += local
		p.d.pinned += pinned
		p.d.files += files
		p.d.folders += folders
		p.d.pending += pending
		for t, b := range tints {
			p.d.tints[t] += b
		}
	}
	s.queue = append(s.queue, sub...)
}

// typeExt is the extension of the file name, in lower case with no dot,
// or "" for a name with none.
func typeExt(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == strings.ToLower(strings.TrimPrefix(name, ".")) {
		return ""
	}
	return ext
}

// tintOfName is the group the file name's colour comes from.
func tintOfName(name string) Tint { return tintOf(entry{Name: name}) }

// clouds are the bytes of n a cloud provider keeps online only, has on
// this device, and always keeps here.
func (n *unode) clouds() (online, local, pinned int64) {
	if n.d != nil {
		return n.d.online, n.d.local, n.d.pinned
	}
	switch n.cloud {
	case CloudOnline:
		return n.size, 0, 0
	case CloudLocal:
		return 0, n.size, 0
	case CloudPinned:
		return 0, 0, n.size
	case CloudNone, CloudFolder:
	}
	return 0, 0, 0
}

// measure is what m sizes n by.
func (n *unode) measure(m Measure) int64 {
	online, _, _ := n.clouds()
	switch m {
	case MeasureDevice:
		return n.size - online
	case MeasureOnline:
		return online
	case MeasureAll:
	}
	return n.size
}

// find returns the item of key under the root, or nil.
func (s *scanner) find(key string) *unode {
	n := s.tree
	if key == "" {
		return n
	}
	for name := range strings.SplitSeq(key, "/") {
		if n.d == nil {
			return nil
		}
		i := slices.IndexFunc(n.d.kids, func(k *unode) bool { return k.name == name })
		if i < 0 {
			return nil
		}
		n = n.d.kids[i]
	}
	return n
}

// The bounds of what a state of Disk usage sends: how many items at most,
// how many of one folder at most, and the least share of the folder in
// focus an item takes to be sent one by one.
const (
	usageMostItems = 3000
	usageMostKids  = 1000
	usageLeast     = 1.0 / 40_000
)

// snapshot is the state of the tree as Disk usage shows it, with focus
// first and the folders of open each with all their items. Items too
// small to show go together in one item for each folder.
func (s *scanner) snapshot(focus string, open map[string]bool, m Measure) (u Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Stopped, it reads no more, though a reader may not have come back.
	over := s.done || s.stopped != ""
	u.Reading, u.Stopped, u.Errs, u.FirstErr = !over, s.stopped, s.errs, s.first
	u.Cloud = s.sawCloud
	took := s.took
	if !over {
		took = time.Since(s.start)
	}
	u.Took = tookWords(took)
	u.Total = s.item(s.tree, "", -1)
	f := s.find(focus)
	if f == nil || f.d == nil {
		f, focus = s.tree, ""
	}
	u.Focus = focus
	u.Crumbs = s.crumbs(focus)
	u.Types = s.typesBy(m)
	u.Items = s.prune(f, focus, open, m)
	return u
}

// tookWords says how long d is, as people read it.
func tookWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
}

// crumbs are the folders from the root down to the one of key.
func (s *scanner) crumbs(key string) []Crumb {
	out := []Crumb{{Name: s.tree.name, Path: ""}}
	if s.tree.name == "" {
		out[0].Name = s.ps.Show(s.root)
	}
	if key == "" {
		return out
	}
	names := strings.Split(key, "/")
	for i, name := range names {
		out = append(out, Crumb{Name: name, Path: strings.Join(names[:i+1], "/")})
	}
	return out
}

// item is how Disk usage shows n, of key, in the folder of index parent.
func (s *scanner) item(n *unode, key string, parent int32) UsageItem {
	online, local, pinned := n.clouds()
	it := UsageItem{Key: key, Name: n.name, Parent: parent, Size: n.size, Online: online, Local: local,
		Pinned: pinned, Tint: n.tint, Cloud: n.cloud}
	if n.d == nil {
		it.Files = 1
		return it
	}
	it.Dir = true
	it.Files, it.Folders = int(n.d.files), int(n.d.folders)
	it.Reading = n.d.pending > 0 && !s.done && s.stopped == ""
	it.Err, it.Skipped = n.d.err, n.d.skipped
	it.Tint = TintFolder
	best := int64(0)
	for t, b := range n.d.tints {
		if b > best {
			best, it.Tint = b, Tint(t)
		}
	}
	if parent < 0 && it.Name == "" {
		it.Name = s.ps.Show(s.root)
	}
	return it
}

// prune picks the items under f, of key, to send: those of the folders
// open first, then the largest folders' items, until there are
// usageMostItems or the rest are too small to show.
func (s *scanner) prune(f *unode, key string, open map[string]bool, m Measure) []UsageItem {
	whole := max(f.measure(m), 1)
	least := max(int64(float64(whole)*usageLeast), 1)
	// opened holds the folders whose items are sent, each with those it
	// sends one by one, largest first, and the rest together.
	type opening struct {
		kids []*unode
		rest int
	}
	opened := map[*unode]*opening{}
	sent := 1
	var queue folderHeap
	queue.m = m
	expand := func(n *unode, all bool) {
		if n.d == nil || len(n.d.kids) == 0 || opened[n] != nil {
			return
		}
		// Only the largest files can be sent, and all the folders may be:
		// a folder can hold millions of files.
		kids := slices.Concat(topFiles(n, m), n.d.dirs)
		slices.SortStableFunc(kids, func(a, b *unode) int { return larger(a, b, m) })
		// The largest, and the folders that could not be read or were
		// not, which take no room but have something to say.
		o := &opening{}
		for _, k := range kids {
			notable := k.d != nil && (k.d.err != "" || k.d.skipped != "")
			if len(o.kids) < usageMostKids && (all || notable || k.measure(m) >= least) {
				o.kids = append(o.kids, k)
			}
		}
		o.rest = len(n.d.kids) - len(o.kids)
		opened[n] = o
		sent += len(o.kids)
		if o.rest > 0 {
			sent++
		}
		for _, k := range o.kids {
			if k.d != nil {
				heap.Push(&queue, k)
			}
		}
	}
	expand(f, open[key])
	// The folders open in the tree, from the top down, each under one
	// open too, so its rows show.
	var keys []string
	for k, on := range open {
		if on && k != key && (key == "" || strings.HasPrefix(k, key+"/")) {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Compare(strings.Count(a, "/"), strings.Count(b, "/")) })
	for _, k := range keys {
		n := s.find(k)
		if n != nil && n.parent != nil && opened[n.parent] != nil && slices.Contains(opened[n.parent].kids, n) {
			expand(n, true)
		}
	}
	for queue.Len() > 0 && sent < usageMostItems {
		n, _ := heap.Pop(&queue).(*unode)
		if n == nil || n.measure(m) < least*8 {
			break
		}
		expand(n, false)
	}
	// Each folder before its items, in order.
	out := make([]UsageItem, 0, sent)
	var walk func(n *unode, k string, parent int32)
	walk = func(n *unode, k string, parent int32) {
		at := int32(len(out))
		it := s.item(n, k, parent)
		o := opened[n]
		it.More = n.d != nil && len(n.d.kids) > 0 && o == nil
		out = append(out, it)
		if o == nil {
			return
		}
		for _, kid := range o.kids {
			kk := kid.name
			if k != "" {
				kk = k + "/" + kid.name
			}
			walk(kid, kk, at)
		}
		if o.rest > 0 {
			out = append(out, s.rest(n, o.kids, o.rest, k, at))
		}
	}
	walk(f, key, -1)
	return out
}

// rest is the n items of folder f, of key, at parent, too small to send,
// as one: all f holds but the items sent.
func (s *scanner) rest(f *unode, sent []*unode, n int, key string, parent int32) UsageItem {
	it := UsageItem{Key: key + "\x00rest", Parent: parent, Rest: n, Tint: TintOther, Size: f.size,
		Online: f.d.online, Local: f.d.local, Pinned: f.d.pinned, Files: int(f.d.files), Folders: int(f.d.folders)}
	for _, k := range sent {
		it.Size -= k.size
		on, lo, pin := k.clouds()
		it.Online -= on
		it.Local -= lo
		it.Pinned -= pin
		if k.d != nil {
			it.Files -= int(k.d.files)
			it.Folders -= 1 + int(k.d.folders)
		} else {
			it.Files--
		}
	}
	it.Name = plural(n, "smaller item")
	return it
}

// larger orders a before b where m sizes it larger, or by name where the
// two are the same size.
func larger(a, b *unode, m Measure) int {
	if c := cmp.Compare(b.measure(m), a.measure(m)); c != 0 {
		return c
	}
	return strings.Compare(a.name, b.name)
}

// topFiles are the usageMostKids largest files of folder n by m, largest
// first, found once and kept.
func topFiles(n *unode, m Measure) []*unode {
	if top := n.d.top[m]; top != nil || len(n.d.kids) == len(n.d.dirs) {
		return top
	}
	// The least of the largest found so far on top.
	h := &fileHeap{m: m}
	for _, k := range n.d.kids {
		if k.d != nil {
			continue
		}
		if len(h.ns) < usageMostKids {
			heap.Push(h, k)
		} else if larger(k, h.ns[0], m) < 0 {
			h.ns[0] = k
			heap.Fix(h, 0)
		}
	}
	top := h.ns
	slices.SortFunc(top, func(a, b *unode) int { return larger(a, b, m) })
	n.d.top[m] = top
	return top
}

// fileHeap holds files, the smallest by m on top.
type fileHeap struct {
	ns []*unode
	m  Measure
}

func (h *fileHeap) Len() int           { return len(h.ns) }
func (h *fileHeap) Less(i, j int) bool { return larger(h.ns[i], h.ns[j], h.m) > 0 }
func (h *fileHeap) Swap(i, j int)      { h.ns[i], h.ns[j] = h.ns[j], h.ns[i] }

// Push implements [heap.Interface].
func (h *fileHeap) Push(x any) {
	if n, ok := x.(*unode); ok {
		h.ns = append(h.ns, n)
	}
}

// Pop implements [heap.Interface].
func (h *fileHeap) Pop() any {
	n := h.ns[len(h.ns)-1]
	h.ns = h.ns[:len(h.ns)-1]
	return n
}

// typesBy are the types found, largest by m first, the smallest put
// together past the most a page lists.
func (s *scanner) typesBy(m Measure) []UsageType {
	const most = 40
	out := make([]UsageType, 0, len(s.types))
	for _, t := range s.types {
		label := "No extension"
		if t.ext != "" {
			label = typeLabel("x." + t.ext)
		}
		out = append(out, UsageType{Ext: t.ext, Label: label, Tint: t.tint, Size: t.size, Online: t.online,
			Device: t.size - t.online, Files: t.files})
	}
	by := func(t UsageType) int64 {
		switch m {
		case MeasureDevice:
			return t.Device
		case MeasureOnline:
			return t.Online
		case MeasureAll:
		}
		return t.Size
	}
	slices.SortFunc(out, func(a, b UsageType) int {
		if c := cmp.Compare(by(b), by(a)); c != 0 {
			return c
		}
		return strings.Compare(a.Ext, b.Ext)
	})
	if len(out) > most {
		o := UsageType{Label: "Other types", Others: true, Tint: TintOther}
		for _, t := range out[most-1:] {
			o.Size += t.Size
			o.Online += t.Online
			o.Device += t.Device
			o.Files += t.Files
		}
		out = append(out[:most-1], o)
	}
	return out
}

// folderHeap holds folders, the largest by m on top.
type folderHeap struct {
	ns []*unode
	m  Measure
}

func (h *folderHeap) Len() int           { return len(h.ns) }
func (h *folderHeap) Less(i, j int) bool { return h.ns[i].measure(h.m) > h.ns[j].measure(h.m) }
func (h *folderHeap) Swap(i, j int)      { h.ns[i], h.ns[j] = h.ns[j], h.ns[i] }

// Push implements [heap.Interface].
func (h *folderHeap) Push(x any) {
	if n, ok := x.(*unode); ok {
		h.ns = append(h.ns, n)
	}
}

// Pop implements [heap.Interface].
func (h *folderHeap) Pop() any {
	n := h.ns[len(h.ns)-1]
	h.ns = h.ns[:len(h.ns)-1]
	return n
}
