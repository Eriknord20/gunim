package filecopy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// opened returns an open file descriptor reading text.
func opened(t *testing.T, text string) int {
	t.Helper()
	p := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fd, err := dupFD(f)
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func TestTwoFilesOfOneNameKeepItAndTheirOwnBytes(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "copies"))
	got, err := c.Take([]File{{opened(t, "first"), "song.hej"}, {opened(t, "second"), "song.hej"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("paths %q, want two of their own", got)
	}
	for i, want := range []string{"first", "second"} {
		if filepath.Base(got[i]) != "song.hej" {
			t.Errorf("copy %d is named %q", i, filepath.Base(got[i]))
		}
		b, err := os.ReadFile(got[i])
		if err != nil || string(b) != want {
			t.Errorf("copy %d holds %q, %v, want %q", i, b, err, want)
		}
	}
}

func TestANameStaysInsideTheCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "copies")
	c := New(dir)
	for _, name := range []string{"../../escape", `..\..\escape`, "..", ".", "", "/etc/passwd", "a\x00b/../.."} {
		got, err := c.Take([]File{{opened(t, "x"), name}}, nil)
		if err != nil || len(got) != 1 {
			t.Fatalf("name %q: %q, %v", name, got, err)
		}
		rel, err := filepath.Rel(dir, got[0])
		if err != nil || strings.HasPrefix(rel, "..") || strings.Count(rel, string(filepath.Separator)) != 1 {
			t.Errorf("name %q landed at %q", name, got[0])
		}
	}
}

func TestALongNameKeepsItsExtension(t *testing.T) {
	got := SafeName(strings.Repeat("å", 300) + ".hej")
	if len(got) > maxName || !strings.HasSuffix(got, ".hej") || !strings.HasPrefix(got, "å") {
		t.Fatalf("%q, %d bytes", got, len(got))
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("%q was cut inside a letter", got)
	}
}

func TestExtensionsChooseWhatIsTaken(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "copies"))
	got, err := c.Take([]File{
		{opened(t, "a"), "take.HEJ"},
		{opened(t, "b"), "leave.bin"},
		{opened(t, "c"), "take.hejprop"},
	}, []string{".hej", ".hejprop"})
	if len(got) != 2 || filepath.Base(got[0]) != "take.HEJ" || filepath.Base(got[1]) != "take.hejprop" {
		t.Fatalf("took %q", got)
	}
	if err == nil || !strings.Contains(err.Error(), "leave.bin") {
		t.Fatalf("err = %v, want word of leave.bin", err)
	}
}

func TestAFileThatFailedLeavesTheRest(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "copies"))
	got, err := c.Take([]File{{-1, "gone.hej"}, {opened(t, "a"), "here.hej"}}, nil)
	if len(got) != 1 || filepath.Base(got[0]) != "here.hej" {
		t.Fatalf("took %q", got)
	}
	if err == nil || !strings.Contains(err.Error(), "gone.hej") {
		t.Fatalf("err = %v, want word of gone.hej", err)
	}
}

func TestACopyThatFailsLeavesNoFolderBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "copies")
	c := New(dir)
	// A folder reads as a file that fails.
	d, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	fd, err := dupFD(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Take([]File{{fd, "folder.hej"}}, nil)
	if err == nil || len(got) != 0 {
		t.Fatalf("took %q, %v", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left %v behind", entries)
	}
}

func TestANewCacheClearsTheLastRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "copies")
	got, err := New(dir).Take([]File{{opened(t, "a"), "old.hej"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	New(dir)
	if _, err := os.Stat(got[0]); !os.IsNotExist(err) {
		t.Fatalf("the old copy is still there: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dir)); !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == "copies" }) && len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
}

// An application sending without end fills no disk.
func TestACopyLeavesRoomOnTheDisk(t *testing.T) {
	t.Parallel()
	c := New(filepath.Join(t.TempDir(), "cache"))
	c.free = func(string) (int64, bool) { return reserve + 100, true }
	if _, err := c.copy(strings.NewReader(strings.Repeat("x", 100)), "fits.hej"); err != nil {
		t.Errorf("a file that fits: %v", err)
	}
	if p, err := c.copy(strings.NewReader(strings.Repeat("x", 101)), "big.hej"); err == nil {
		t.Errorf("a file past the room was copied to %s", p)
	}
	c.free = func(string) (int64, bool) { return reserve - 1, true }
	if p, err := c.copy(strings.NewReader("x"), "none.hej"); err == nil {
		t.Errorf("with no room a file was copied to %s", p)
	}
	ents, err := os.ReadDir(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("the failed copies left %v", ents)
	}
}
