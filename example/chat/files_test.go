package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/marrasen/gunim/chat"
)

func TestTheFilesAreaOpensFromTheSidebarAndWalksFolders(t *testing.T) {
	h := newHarness(t)
	h.a.handle(AreaChosen{Area: "files"})
	h.frames(5)
	s := h.a.state()
	if s.Area != "files" || s.Files.Path != "" || len(s.Files.Entries) == 0 || !s.Files.Entries[0].Folder {
		t.Fatalf("the files area shows %q at %q with %d entries, want the project's top, folders first",
			s.Area, s.Files.Path, len(s.Files.Entries))
	}
	if h.v.areas.shown != 1 {
		t.Fatal("the pane still shows the conversation")
	}

	h.a.handle(FolderOpened{Path: "Design/Old"})
	h.frames(5)
	if s := h.a.state(); s.Files.Path != "Design/Old" || len(s.Files.Entries) != 1 {
		t.Fatalf("the folder open is %q with %d entries, want Design/Old with one", s.Files.Path, len(s.Files.Entries))
	}
	h.a.handle(FolderOpened{Path: "No such folder"})
	if s := h.a.state(); s.Files.Path != "Design/Old" {
		t.Fatalf("a folder that is not there moved the view to %q", s.Files.Path)
	}

	h.a.handle(ConversationChosen{ID: h.a.current.ID})
	h.frames(5)
	if s := h.a.state(); s.Area != "" || h.v.areas.shown != 0 {
		t.Fatal("choosing a conversation left the files showing")
	}
}

func TestDroppedFilesUploadIntoTheFolderOpen(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, make([]byte, 700_000), 0o600); err != nil {
		t.Fatal(err)
	}
	h.a.handle(AreaChosen{Area: "files"})
	h.a.handle(FolderOpened{Path: "Design"})
	h.a.handle(FilesDropped{Folder: "Design", Paths: []string{notes, filepath.Join(dir, "missing.txt"), dir}})
	h.frames(2)

	s := h.a.state().Files
	if len(s.Transfers) != 3 {
		t.Fatalf("%d transfers, want one for each file dropped", len(s.Transfers))
	}
	if s.Transfers[1].State != chat.TransferFailed || s.Transfers[1].Reason == "" {
		t.Fatalf("a file that is not there stands at %+v, want failed with the reason", s.Transfers[1])
	}
	if s.Transfers[2].State != chat.TransferFailed {
		t.Fatalf("a folder dropped stands at %+v, want failed", s.Transfers[2])
	}
	h.until("the file is uploaded", func() bool {
		return slices.ContainsFunc(h.a.state().Files.Entries, func(e FileEntry) bool {
			return e.Name == "notes.txt" && e.Size == 700_000 && e.By == me
		})
	})

	h.a.handle(chat.TransferDismissed{ID: s.Transfers[1].ID})
	if n := len(h.a.state().Files.Transfers); n != 2 {
		t.Fatalf("%d transfers after taking a failed one away, want two", n)
	}
}

func TestUploadsWaitForTheConnection(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, make([]byte, 300_000), 0o600); err != nil {
		t.Fatal(err)
	}
	h.a.handle(LinkToggled{})
	h.a.handle(AreaChosen{Area: "files"})
	h.a.handle(FilesDropped{Paths: []string{path}})
	if st := h.a.state().Files.Transfers[0].State; st != chat.TransferWaiting {
		t.Fatalf("offline, the upload stands at %v, want waiting", st)
	}
	h.a.handle(LinkToggled{})
	h.until("the file is uploaded once the connection is back", func() bool {
		return slices.ContainsFunc(h.a.state().Files.Entries, func(e FileEntry) bool { return e.Name == "plan.md" })
	})
}

func TestASharedFileShowsInTheConversationAndOpensFromIt(t *testing.T) {
	h := newHarness(t)
	general := h.a.current
	h.a.handle(AreaChosen{Area: "files"})
	h.a.handle(FileShared{Folder: "Design", Name: "Colours.pdf", Conversation: general.ID})
	h.frames(5)
	s := h.a.state()
	if s.Area != "" || s.ID != general.ID {
		t.Fatalf("after sharing, the pane shows %q %q, want the conversation shared in", s.Area, s.ID)
	}
	last := s.Items[len(s.Items)-1]
	if last.File.Name != "Colours.pdf" || last.File.Folder != "Design" || last.File.Size != 1_204_551 || !last.Mine {
		t.Fatalf("the last message shares %+v, want the file", last.File)
	}

	h.a.handle(chat.FileOpened{Folder: last.File.Folder, Name: last.File.Name})
	h.frames(5)
	s = h.a.state()
	if s.Area != "files" || s.Files.Path != "Design" || s.Files.Selected != "Colours.pdf" {
		t.Fatalf("opening the file shows %q at %q with %q selected", s.Area, s.Files.Path, s.Files.Selected)
	}
	row := h.v.files.grid.Selected()
	if row < 0 || s.Files.Entries[row].Name != "Colours.pdf" {
		t.Fatal("the grid does not have the file selected")
	}
}
