package main

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// folder is a folder of a project's files.
type folder struct {
	name    string
	folders []*folder
	files   []*file
}

// file is one of a project's files: its name, its size in bytes, when it last changed and who changed it.
type file struct {
	name string
	size int64
	at   time.Time
	by   string
}

// seedFiles gives p a few folders of files, changed over the last weeks by its people.
func (a *app) seedFiles(p *project) {
	var people []string
	for _, c := range p.convs {
		people = append(people, c.people...)
	}
	now := time.Now().Round(0)
	f := func(name string, size int64) *file {
		return &file{name: name, size: size, at: now.Add(-time.Duration(a.rng.IntN(30*24*60)) * time.Minute),
			by: a.pick(people)}
	}
	p.files = &folder{
		folders: []*folder{
			{name: "Design", files: []*file{
				f("Main window.png", 482_113), f("Colours.pdf", 1_204_551), f("Icons.svg", 38_902),
			}, folders: []*folder{
				{name: "Old", files: []*file{f("First sketch.png", 912_004)}},
			}},
			{name: "Meeting notes", files: []*file{
				f("Kick-off.md", 4_210), f("Planning week 38.md", 6_882), f("Retrospective.md", 3_120),
			}},
			{name: "Releases", files: []*file{
				f("Release checklist.md", 1_904), f("Changelog.md", 22_470),
			}},
		},
		files: []*file{f("Budget.xlsx", 88_320), f("README.md", 2_560), f("Team photo.jpg", 3_402_118)},
	}
}

// folderAt returns the folder at path, its names joined by slashes from the project's top, which is the empty path.
func (p *project) folderAt(path string) (*folder, bool) {
	f := p.files
	if path == "" {
		return f, true
	}
	for _, name := range strings.Split(path, "/") {
		i := slices.IndexFunc(f.folders, func(g *folder) bool { return g.name == name })
		if i < 0 {
			return nil, false
		}
		f = f.folders[i]
	}
	return f, true
}

// entriesOf returns what f holds, its folders first, each part in order of name.
func entriesOf(f *folder) []FileEntry {
	out := make([]FileEntry, 0, len(f.folders)+len(f.files))
	for _, g := range f.folders {
		out = append(out, FileEntry{Name: g.name, Folder: true, Items: len(g.folders) + len(g.files), At: newest(g)})
	}
	for _, x := range f.files {
		out = append(out, FileEntry{Name: x.name, Size: x.size, At: x.at, By: x.by})
	}
	slices.SortStableFunc(out, func(a, b FileEntry) int {
		if a.Folder != b.Folder {
			return map[bool]int{true: -1, false: 1}[a.Folder]
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// share posts a message sharing the file v names in the conversation it names, and shows w the conversation.
func (a *app) share(w *window, v FileShared) {
	p := a.projects[w.project]
	f, ok := p.folderAt(v.Folder)
	if !ok {
		return
	}
	i := slices.IndexFunc(f.files, func(x *file) bool { return x.name == v.Name })
	c := slices.IndexFunc(p.convs, func(c *conv) bool { return c.ID == v.Conversation })
	if i < 0 || c < 0 {
		return
	}
	m := a.add(p.convs[c], me, "", time.Now().Round(0))
	m.file = FileRef{Folder: v.Folder, Name: v.Name, Size: f.files[i].size}
	p.convs[c].readTo = m.ID
	a.send(m)
	w.area = ""
	a.open(w, p.convs[c])
}

// newest returns when anything in f last changed, or the zero time for an empty folder.
func newest(f *folder) time.Time {
	var t time.Time
	for _, x := range f.files {
		if x.at.After(t) {
			t = x.at
		}
	}
	for _, g := range f.folders {
		if n := newest(g); n.After(t) {
			t = n
		}
	}
	return t
}
