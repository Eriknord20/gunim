package vst3

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// readJournal reads the journal at path.
func readJournal(t *testing.T, path string) journalFile {
	t.Helper()
	var j journalFile
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatal(err)
	}
	return j
}

// TestAModuleThatCrashedIsSkipped scans a folder of a bundle that lists
// nothing, so the scan loads its module. The journal names the module
// while it loads, and nothing after. A journal still naming it as the
// next library starts, as a crash leaves it, has that library skip it
// and list it as failed, until Retry loads it again.
func TestAModuleThatCrashedIsSkipped(t *testing.T) {
	// The library scans the system's folders too: point them at an empty
	// one, so the plugins installed on the machine stay out of the test.
	empty := t.TempDir()
	for _, env := range []string{"CommonProgramFiles", "LOCALAPPDATA", "HOME", "USERPROFILE"} {
		t.Setenv(env, empty)
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "Crashy.vst3")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(t.TempDir(), "journal.json")
	var loaded []string
	beforeLoad = func(path string) {
		loaded = append(loaded, path)
		if j := readJournal(t, journal); j.Loading != path {
			t.Errorf("loading %s, the journal names %q", path, j.Loading)
		}
	}
	t.Cleanup(func() { beforeLoad = nil })

	lib := NewLibraryAt(journal, dir)
	if len(lib.Failed()) != 0 || !slices.Equal(loaded, []string{bundle}) {
		t.Fatalf("a first scan failed %v, and loaded %v", lib.Failed(), loaded)
	}
	if j := readJournal(t, journal); j.Loading != "" || len(j.Crashed) != 0 {
		t.Fatalf("after the scan, the journal holds %+v", j)
	}

	// The program dies loading it.
	b, _ := json.Marshal(journalFile{Loading: bundle})
	if err := os.WriteFile(journal, b, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded = nil
	lib = NewLibraryAt(journal, dir)
	if !slices.Equal(lib.Failed(), []string{bundle}) || len(loaded) != 0 {
		t.Fatalf("after a crash, the scan failed %v, and loaded %v", lib.Failed(), loaded)
	}
	// Still skipped by the library after.
	if lib := NewLibraryAt(journal, dir); !slices.Equal(lib.Failed(), []string{bundle}) {
		t.Fatalf("a library after that fails %v", lib.Failed())
	}
	// Tried again: it loads, and fails as other than a crash.
	if err := lib.Retry(bundle); err == nil || errors.Is(err, ErrCrashed) || !slices.Equal(loaded, []string{bundle}) {
		t.Fatalf("retried, it loaded %v: %v", loaded, err)
	}
	if len(lib.Failed()) != 0 {
		t.Fatalf("retried, it is listed as failed: %v", lib.Failed())
	}
	if j := readJournal(t, journal); j.Loading != "" || len(j.Crashed) != 0 {
		t.Fatalf("after the retry, the journal holds %+v", j)
	}
}

// TestAPluginWhoseModuleCrashedIsNotFound has plug look up a plugin of
// LSP's, whose bundle lists it, so the scan loads nothing: a journal
// naming LSP's module as crashed keeps Find from loading it, with the
// crash as the reason.
func TestAPluginWhoseModuleCrashedIsNotFound(t *testing.T) {
	m := lsp(t)
	journal := filepath.Join(t.TempDir(), "journal.json")
	b, _ := json.Marshal(journalFile{Crashed: []string{m.Path}})
	if err := os.WriteFile(journal, b, 0o644); err != nil {
		t.Fatal(err)
	}
	lib := NewLibraryAt(journal, filepath.Dir(m.Path))
	var id string
	for _, f := range lib.Plugins() {
		if f.Name == "Sampler Mono" {
			id = f.ID
		}
	}
	if id == "" || lib.Find(id) || !errors.Is(lib.Err(id), ErrCrashed) {
		t.Fatalf("the sampler %q was found, or would not load for %v", id, lib.Err(id))
	}
}
