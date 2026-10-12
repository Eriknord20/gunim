package zipcrypt

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fox is what fox.txt in the test archives holds.
var fox = strings.Repeat("The quick brown fox jumps over the lazy dog.\n", 40)

// The archives in testdata were made by Info-ZIP's zip -P (legacy.zip)
// and 7-Zip (aes256.zip, and aes128-store.zip stored, not compressed),
// with the password hunter2.
func TestOpenReadsWhatOtherToolsProtect(t *testing.T) {
	for _, name := range []string{"legacy.zip", "aes256.zip", "aes128-store.zip"} {
		t.Run(name, func(t *testing.T) {
			zr, err := zip.OpenReader(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = zr.Close() }()
			for _, f := range zr.File {
				if !Encrypted(f) {
					t.Fatalf("%s is not protected", f.Name)
				}
				if _, err := Open(f, "wrong"); !errors.Is(err, ErrPassword) && !errors.Is(err, ErrDamaged) {
					t.Fatalf("%s opened with the wrong password: %v", f.Name, err)
				}
				got := read(t, f, "hunter2")
				want := map[string]string{"fox.txt": fox, "tiny.txt": "tiny"}[f.Name]
				if got != want {
					t.Fatalf("%s holds %q", f.Name, got)
				}
			}
		})
	}
}

func read(t *testing.T, f *zip.File, password string) string {
	t.Helper()
	rc, err := Open(f, password)
	if err != nil {
		t.Fatalf("opening %s: %v", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading %s: %v", f.Name, err)
	}
	return string(b)
}

// write makes a zip of files, protected with password, with a folder.
func write(t *testing.T, password string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := NewWriter(zip.NewWriter(&buf), password)
	if _, err := zw.Create(&zip.FileHeader{Name: "folder/"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, err := zw.Create(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: when})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// when is the time the test entries were changed.
var when = time.Date(2026, 10, 10, 12, 34, 56, 0, time.UTC)

func TestTheWriterWritesWhatOpenReads(t *testing.T) {
	big := make([]byte, 300<<10)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"fox.txt": fox, "empty.txt": "", "folder/one.txt": "1", "räksmörgås.txt": "åäö", "big.bin": string(big)}
	data := write(t, "pässword", files)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "folder/" {
			if Encrypted(f) {
				t.Fatal("the folder is marked protected")
			}
			continue
		}
		if !Encrypted(f) {
			t.Fatalf("%s is not protected", f.Name)
		}
		if _, err := f.Open(); err == nil {
			t.Fatalf("%s opened with no password", f.Name)
		}
		if _, err := Open(f, "password"); !errors.Is(err, ErrPassword) {
			t.Fatalf("%s opened with the wrong password: %v", f.Name, err)
		}
		if got := read(t, f, "pässword"); got != files[f.Name] {
			t.Fatalf("%s holds %d bytes, not what was written", f.Name, len(got))
		}
		// AE-2: a CRC in the clear would tell what a small file holds.
		if f.CRC32 != 0 {
			t.Fatalf("%s stores its CRC, %08x", f.Name, f.CRC32)
		}
		if !f.Modified.Equal(when) {
			t.Fatalf("%s was changed at %v, want %v", f.Name, f.Modified, when)
		}
	}
	if bytes.Contains(data, []byte("quick brown fox")) {
		t.Fatal("the zip holds the text in the clear")
	}
}

func TestADamagedEntryDoesNotCheck(t *testing.T) {
	data := write(t, "secret", map[string]string{"fox.txt": fox})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	f := zr.File[1]
	at, err := f.DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	// A byte in the middle of what is stored, past the salt and check.
	bad := bytes.Clone(data)
	bad[at+40] ^= 0xff
	zr, err = zip.NewReader(bytes.NewReader(bad), int64(len(bad)))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := Open(zr.File[1], "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err == nil {
		t.Fatal("a damaged entry read without an error")
	}
}

func TestAnEmptyPasswordProtectsNothing(t *testing.T) {
	data := write(t, "", map[string]string{"a.txt": "a"})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if Encrypted(f) {
			t.Fatalf("%s is protected with no password", f.Name)
		}
	}
}

// 7-Zip, where it is installed, reads what Create writes.
func TestSevenZipReadsWhatTheWriterWrites(t *testing.T) {
	sz, err := exec.LookPath("7z")
	if err != nil {
		t.Skip("7z is not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "made.zip")
	if werr := os.WriteFile(path, write(t, "hunter2", map[string]string{"fox.txt": fox, "räksmörgås.txt": "åäö", "empty.txt": ""}), 0o600); werr != nil {
		t.Fatal(werr)
	}
	out := filepath.Join(dir, "out")
	if b, xerr := exec.CommandContext(t.Context(), sz, "x", "-phunter2", "-o"+out, path).CombinedOutput(); xerr != nil {
		t.Fatalf("7z: %v\n%s", xerr, b)
	}
	got, err := os.ReadFile(filepath.Join(out, "fox.txt"))
	if err != nil || string(got) != fox {
		t.Fatalf("7z extracted %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(out, "räksmörgås.txt")); err != nil || string(got) != "åäö" {
		t.Fatalf("7z extracted the file of a name beyond ASCII as %q, %v", got, err)
	}
	if b, terr := exec.CommandContext(t.Context(), sz, "t", "-pwrong", path).CombinedOutput(); terr == nil {
		t.Fatalf("7z took the wrong password:\n%s", b)
	}
}

// A zip made on Windows may hold a password beyond ASCII in code page
// 850: legacy-cp850.zip was made by zip -P with åäö written so.
func TestOpenTakesAPasswordWrittenInCodePage850(t *testing.T) {
	zr, err := zip.OpenReader(filepath.Join("testdata", "legacy-cp850.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	if got := read(t, zr.File[0], "åäö"); got != "tiny" {
		t.Fatalf("tiny.txt holds %q", got)
	}
	if _, err := Open(zr.File[0], "aao"); !errors.Is(err, ErrPassword) && !errors.Is(err, ErrDamaged) {
		t.Fatalf("opened with the wrong password: %v", err)
	}
}

func TestLegacyFormsOfAPassword(t *testing.T) {
	if got := legacyForms("plain"); len(got) != 1 {
		t.Fatalf("an ASCII password has the forms %q", got)
	}
	got := legacyForms("åäö")
	want := []string{"åäö", "\x86\x84\x94", "\xe5\xe4\xf6"}
	if !slices.Equal(got, want) {
		t.Fatalf("åäö has the forms %q, want %q", got, want)
	}
	if got := legacyForms("日本"); len(got) != 1 {
		t.Fatalf("a password neither code page holds has the forms %q", got)
	}
}

// Each entry of a zip made on Windows, its password in code page 850,
// opens: a wrong way of writing the password passes the check byte of
// some of them, once in 256 times, and must not be taken for the right.
func TestEveryEntryOpensWhateverWayThePasswordPasses(t *testing.T) {
	zipTool, err := exec.LookPath("zip")
	if err != nil {
		t.Skip("zip is not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if merr := os.Mkdir(src, 0o755); merr != nil {
		t.Fatal(merr)
	}
	for i := range 400 {
		name := filepath.Join(src, "f"+strconv.Itoa(i))
		if werr := os.WriteFile(name, []byte(strings.Repeat(strconv.Itoa(i), i+1)), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	path := filepath.Join(dir, "many.zip")
	cmd := exec.CommandContext(t.Context(), zipTool, "-q", "-r", "-P", "sk\x94l", path, "src")
	cmd.Dir = dir
	if b, zerr := cmd.CombinedOutput(); zerr != nil {
		t.Fatalf("zip: %v\n%s", zerr, b)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		i, _ := strconv.Atoi(strings.TrimPrefix(f.Name, "src/f"))
		if got := read(t, f, "sköl"); got != strings.Repeat(strconv.Itoa(i), i+1) {
			t.Fatalf("%s holds %q", f.Name, got)
		}
	}
}
