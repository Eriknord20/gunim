package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTheFilterLooksInTheFoldersUnder(t *testing.T) {
	h := newHarness(t, "report.md", "notes.txt", "a/report.txt", "a/b/old-report.pdf", "a/b/plan.txt", ".hidden/report.txt")
	h.do(Command{Name: CmdDeepFilter})
	h.do(FilterChanged{Text: "REPORT"})
	want := []string{filepath.Join("a", "b", "old-report.pdf"), "report.md", filepath.Join("a", "report.txt")}
	h.until("the filter finds the items under the folder", func() bool { return slices.Equal(h.shown(), want) })
	pg := h.b.listing.cur
	if len(pg.grid.Columns) != int(SortFolder)+1 || pg.grid.Columns[SortFolder].Title != "Folder" {
		t.Fatalf("the grid has no Folder column: %v", pg.grid.Columns)
	}
	row, _ := pg.row(0)
	if name, folder := row.Cells[0][len(row.Cells[0])-1].Text, row.Cells[SortFolder][0].Text; name != "old-report.pdf" ||
		folder != filepath.Join("a", "b") {
		t.Fatalf("the first row shows %q in %q", name, folder)
	}
	if !h.b.path.deep.Active {
		t.Fatal("the button is not lit")
	}

	h.do(Command{Name: CmdHidden})
	h.until("the hidden folder's items show", func() bool { return len(h.shown()) == 4 })
	h.do(Command{Name: CmdHidden})

	h.do(Command{Name: CmdDeepFilter})
	h.until("the filter looks in the folder alone", func() bool { return slices.Equal(h.shown(), []string{"report.md"}) })
	if len(pg.grid.Columns) != int(SortFolder) {
		t.Fatal("the Folder column stayed")
	}
	if p, err := loadPrefs(h.a.prefsPath); err != nil || p.DeepFilter {
		t.Fatalf("the setting saved as %v, %v", p.DeepFilter, err)
	}
}

func TestGoingToTheFolderOfAnItemFound(t *testing.T) {
	h := newHarness(t, "a/b/plan.txt", "a/b/other.txt", "top.txt")
	h.do(Command{Name: CmdDeepFilter})
	h.do(FilterChanged{Text: "plan"})
	found := filepath.Join("a", "b", "plan.txt")
	h.until("the filter finds the file", func() bool { return slices.Equal(h.shown(), []string{found}) })
	h.pick(found)
	h.do(Command{Name: CmdGoToFolder})
	h.until("its folder shows, with it selected", func() bool {
		return h.a.nav.path == filepath.Join(h.dir, "a", "b") && slices.Equal(h.selected(), []string{"plan.txt"})
	})
	if h.a.nav.filter != "" || h.a.tree.root != "" {
		t.Fatal("the filter or its tree stayed")
	}
}

func TestRenamingAnItemFoundKeepsItSelected(t *testing.T) {
	h := newHarness(t, "a/plan.txt", "top.txt")
	h.do(Command{Name: CmdDeepFilter})
	h.do(FilterChanged{Text: "plan"})
	found := filepath.Join("a", "plan.txt")
	h.until("the filter finds the file", func() bool { return slices.Equal(h.shown(), []string{found}) })
	h.pick(found)
	h.do(Command{Name: CmdRename})
	h.until("the prompt shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	p, _ := h.a.ops.dialogs[0].state.(Prompt)
	if p.Text != "plan.txt" {
		t.Fatalf("the prompt offers %q", p.Text)
	}
	h.answer(Prompted{Token: p.Token, Text: "plan2.txt", OK: true})
	renamed := filepath.Join("a", "plan2.txt")
	h.until("the file is renamed and found again", func() bool {
		return h.exists(renamed) && slices.Equal(h.shown(), []string{renamed}) && slices.Equal(h.selected(), []string{renamed})
	})
}

func TestTheWalkStopsAtItsBounds(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, "a/1.txt", "a/2.txt", "b/3.txt", "c.txt")
	var got []entry
	var stopped string
	read := func(b bounds) {
		got, stopped = nil, ""
		walkEntries(context.Background(), LocalFS(), dir, b, func(es []entry, errs []walkError, s string, done bool) {
			got = append(got, es...)
			if done {
				stopped = s
			}
		})
	}
	read(bounds{items: 100, time: deepBounds.time})
	if len(got) != 6 || stopped != "" {
		t.Fatalf("the walk found %d items and stopped with %q", len(got), stopped)
	}
	for _, e := range got {
		if filepath.Join(e.Name[:e.at], e.base()) != e.Name || filepath.Base(e.Name) != e.base() {
			t.Fatalf("%q names %q at %d", e.Name, e.base(), e.at)
		}
	}
	read(bounds{items: 4, time: deepBounds.time})
	if len(got) != 4 || stopped != "Stopped at 4 items" {
		t.Fatalf("the bounded walk found %d items and stopped with %q", len(got), stopped)
	}
}

func TestTheWalkDoesNotFollowLinks(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, "a/x.txt")
	if err := os.Symlink(dir, filepath.Join(dir, "a", "loop")); err != nil {
		t.Skip("links cannot be made here:", err)
	}
	var got []string
	walkEntries(context.Background(), LocalFS(), dir, deepBounds, func(es []entry, _ []walkError, _ string, _ bool) {
		for _, e := range es {
			got = append(got, e.Name)
		}
	})
	slices.Sort(got)
	want := []string{"a", filepath.Join("a", "loop"), filepath.Join("a", "x.txt")}
	if !slices.Equal(got, want) {
		t.Fatalf("the walk found %v", got)
	}
}
