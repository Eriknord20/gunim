package filemanager

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// Windows' attributes say how a cloud provider keeps an item, as
// Explorer reads them; one pinned and not yet down is still online.
func TestWindowsCloud(t *testing.T) {
	for _, c := range []struct {
		attrs        uint32
		dir, inCloud bool
		want         CloudState
	}{
		{0, false, false, CloudNone},
		{0, false, true, CloudLocal},
		{0, true, true, CloudFolder},
		{fileAttributePinned, false, true, CloudPinned},
		{fileAttributePinned, true, true, CloudPinned},
		{fileAttributeRecallOnDataAccess, false, true, CloudOnline},
		{fileAttributeRecallOnDataAccess, false, false, CloudOnline},
		{fileAttributeRecallOnOpen, true, true, CloudOnline},
		{fileAttributePinned | fileAttributeOffline, false, true, CloudOnline},
	} {
		if got := WindowsCloud(c.attrs, c.dir, c.inCloud); got != c.want {
			t.Errorf("WindowsCloud(%#x, %v, %v) = %v, want %v", c.attrs, c.dir, c.inCloud, got, c.want)
		}
	}
}

// statesFS says how a cloud provider keeps each item by its name.
type statesFS struct{ slashFS }

func (statesFS) Cloud(_ string, info fs.FileInfo) CloudState {
	switch {
	case strings.HasPrefix(info.Name(), "online"):
		return CloudOnline
	case strings.HasPrefix(info.Name(), "pinned"):
		return CloudPinned
	case info.IsDir():
		return CloudFolder
	}
	return CloudLocal
}

// A file system that says how its items are kept has the listing mark
// each before its name, where a long name cannot push it out of sight,
// and an online-only file is one the window does not read by itself.
func TestTheCloudMarksGoBeforeTheName(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "Docs/a.txt", "local.txt", "online.txt", "pinned.txt")
	es, err := listDir(t.Context(), statesFS{slashFS{root: root}}, "/")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]CloudState{}
	for _, e := range es {
		got[e.Name] = e.Cloud
		if e.Online != (e.Cloud == CloudOnline) {
			t.Errorf("%s is online %v, kept %v", e.Name, e.Online, e.Cloud)
		}
	}
	if got["Docs"] != CloudFolder || got["local.txt"] != CloudLocal || got["online.txt"] != CloudOnline || got["pinned.txt"] != CloudPinned {
		t.Fatalf("the states are %v", got)
	}
	// The folder's row, wherever the directory listed it: the order
	// is the file system's, which differs from one machine to another.
	docs := slices.IndexFunc(es, func(e entry) bool { return e.Name == "Docs" })
	pg := &listingPage{b: &browser{icons: map[string]SystemIcon{}}, blocks: map[int][]Row{0: {rowOf(es[docs]), {Name: "plain.txt"}}}}
	for i, want := range []int{5, 3} {
		r, _ := pg.row(i)
		name := r.Cells[0]
		if len(name) != want {
			t.Fatalf("row %d's name cell is %+v", i, name)
		}
		if last := name[len(name)-1].Text; last != []string{"Docs", "plain.txt"}[i] {
			t.Fatalf("row %d ends with %q, not its name", i, last)
		}
	}
}
