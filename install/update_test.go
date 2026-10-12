package install

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "1.2.0", 0},
		{"v1.2.0", "v1.10.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.0.0-beta.1", "v1.0.0", -1},
		{"v1.0.0-beta.2", "v1.0.0-beta.10", -1},
		{"v1.0.0-beta.2", "v1.0.0-alpha.9", 1},
		{"v1.0.0-1", "v1.0.0-beta", -1},
		{"v1.0.0-beta", "v1.0.0-beta.1", -1},
		{"v1.0.0+build.5", "v1.0.0", 0},
		{"dev", "v1.0.0", 0},
		{"v1.0", "v1.0.0", 0},
	} {
		if got := compare(c.a, c.b); got != c.want {
			t.Errorf("compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for v, want := range map[string]bool{"v1.2.3": true, "1.2.3-rc.1": true, "dev": false, "": false, "v01.2.3": false, "v1.2.3-": false} {
		if IsRelease(v) != want {
			t.Errorf("IsRelease(%q) = %v", v, !want)
		}
	}
}

// testSigner signs the test's releases, and testKey is its public key,
// as App.UpdateKey holds it.
var (
	testSigner = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	testKey    = publicKey(testSigner)
)

// publicKey is k's public key, as App.UpdateKey holds it.
func publicKey(k ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k[ed25519.SeedSize:])
}

// sign is testSigner's signature of sums, as gunimsign writes it.
func sign(sums []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(testSigner, sums)) + "\n")
}

// lookFor runs a's updater until stop, which waits for it to end, as
// the test's cleanup does too, before the test's timings go back.
func lookFor(t *testing.T, a App) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		keepUpToDate(ctx, a)
		close(done)
	}()
	stop = func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return stop
}

// release is a release as the test's GitHub serves it.
type release struct {
	tag, notes string
	prerelease bool
	files      map[string][]byte
}

// fakeGitHub serves releases as GitHub's API does, each with a
// SHA256SUMS of its files.
func fakeGitHub(t *testing.T, rels ...release) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	list := make([]map[string]any, 0, len(rels))
	for _, r := range rels {
		var sums strings.Builder
		assets := make([]map[string]string, 0, len(r.files)+2)
		for name, body := range r.files {
			sum := sha256.Sum256(body)
			fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
			url := "/dl/" + r.tag + "/" + name
			mux.HandleFunc(url, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				_, _ = w.Write(body)
			})
			assets = append(assets, map[string]string{"name": name, "browser_download_url": srv.URL + url})
		}
		sumsURL := "/dl/" + r.tag + "/SHA256SUMS"
		body := sums.String()
		mux.HandleFunc(sumsURL, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		assets = append(assets, map[string]string{"name": "SHA256SUMS", "browser_download_url": srv.URL + sumsURL})
		sig := sign([]byte(body))
		mux.HandleFunc(sumsURL+".sig", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(sig) })
		assets = append(assets, map[string]string{"name": "SHA256SUMS.sig", "browser_download_url": srv.URL + sumsURL + ".sig"})
		list = append(list, map[string]any{"tag_name": r.tag, "prerelease": r.prerelease, "html_url": "https://example.com/" + r.tag,
			"body": r.notes, "assets": assets})
	}
	mux.HandleFunc("/repos/marrasen/studio/releases", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(list)
	})
	return srv
}

func asset(version string) string {
	return "studio_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + exeSuffix
}

// The newest release with a program for this system is the latest; a
// pre-release only for a source that takes them.
func TestGitHubLatest(t *testing.T) {
	srv := fakeGitHub(t,
		release{tag: "v1.3.0-beta.1", prerelease: true, files: map[string][]byte{asset("1.3.0-beta.1"): []byte("beta")}},
		release{tag: "v1.2.0", files: map[string][]byte{asset("1.2.0"): []byte("one two")}},
		release{tag: "v1.4.0", files: map[string][]byte{"studio_1.4.0_plan9_mips" + exeSuffix: []byte("elsewhere")}},
		release{tag: "v1.1.0", files: map[string][]byte{asset("1.1.0"): []byte("the older one")}},
	)
	g := GitHub{Repo: "marrasen/studio", API: srv.URL}
	r, err := g.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "v1.2.0" || r.Name != asset("1.2.0") {
		t.Fatalf("latest %+v, want v1.2.0", r)
	}
	g.Prerelease = true
	r, err = g.Latest(context.Background())
	if err != nil || r.Version != "v1.3.0-beta.1" {
		t.Fatalf("latest with pre-releases %+v, %v", r, err)
	}
	a := App{Name: "studio", Version: "v1.2.0", Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey}
	if _, newer, err := Check(context.Background(), a); err != nil || newer {
		t.Fatalf("Check says newer %v, %v; want the same", newer, err)
	}
	a.Version = "dev"
	if _, newer, _ := Check(context.Background(), a); newer {
		t.Error("a build from a working tree was behind")
	}
}

// Stage puts a release checked against its sums in place of the
// installed program, from the program itself or from an archive.
func TestStage(t *testing.T) {
	dir := t.TempDir()
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	f, _ := zw.Create("studio/studio" + exeSuffix)
	_, _ = f.Write([]byte("from the zip"))
	_ = zw.Close()
	srv := fakeGitHub(t,
		release{tag: "v2.0.0", files: map[string][]byte{asset("2.0.0"): []byte("two")}},
	)
	a := App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey}
	exe := filepath.Join(dir, "studio"+exeSuffix)
	if err := os.WriteFile(exe, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, newer, err := Check(context.Background(), a)
	if err != nil || !newer {
		t.Fatalf("Check %+v, %v, %v", r, newer, err)
	}
	if err := Stage(context.Background(), a, r); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "two" {
		t.Fatalf("the program holds %q after the update", raw)
	}

	zsrv := fakeGitHub(t, release{tag: "v3.0.0", files: map[string][]byte{
		"studio_3.0.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip": zipped.Bytes()}})
	a.Updates = GitHub{Repo: "marrasen/studio", API: zsrv.URL}
	r, _, err = Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if err := Stage(context.Background(), a, r); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "from the zip" {
		t.Fatalf("the program holds %q after the update from a zip", raw)
	}

	// A program that is not what its sums say is not put in place.
	bad := r
	bad.Name, bad.Version = "nothing", "v4.0.0"
	if err := Stage(context.Background(), a, bad); err == nil {
		t.Fatal("staged a release whose sums say nothing of it")
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "from the zip" {
		t.Fatalf("a failed update left %q", raw)
	}
}

// The ID is made from the name, and mistakes in an App are said.
func TestCheckApp(t *testing.T) {
	a := App{Name: "Marras Mastering Studio!"}
	if a.id() != "marras-mastering-studio" {
		t.Errorf("id %q", a.id())
	}
	for _, bad := range []App{
		{},
		{Name: "x", ID: "a/b"},
		{Name: "x", FileTypes: []FileType{{Exts: []string{"album"}}}},
		{Name: "x", Choices: []Choice{{Key: PickDesktop}}},
	} {
		if err := bad.check(); err == nil {
			t.Errorf("%+v passed", bad)
		}
	}
}

// The updater does what the mode says at each look: installs and says
// so, tells the program, or does nothing.
func TestKeepUpToDateFollowsTheMode(t *testing.T) {
	was, wasEvery := updateFirst, updateEvery
	updateFirst, updateEvery = time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { updateFirst, updateEvery = was, wasEvery })
	srv := fakeGitHub(t, release{tag: "v2.0.0", files: map[string][]byte{asset("2.0.0"): []byte("two")}})
	dir := t.TempDir()
	available, updated := make(chan Release, 4), make(chan Release, 4)
	a := App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey,
		Available: func(r Release) { available <- r }, Updated: func(r Release) { updated <- r }}
	exe := filepath.Join(dir, "studio"+exeSuffix)
	if err := os.WriteFile(exe, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := manifest{ID: "studio", Version: "v1.0.0", Exe: "studio" + exeSuffix, Updates: UpdatesNotify}
	if err := m.write(dir); err != nil {
		t.Fatal(err)
	}
	stop := lookFor(t, a)
	select {
	case r := <-available:
		if r.Version != "v2.0.0" {
			t.Fatalf("told of %s", r.Version)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("set to notify, the program was not told")
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "one" {
		t.Fatal("set to notify, the release was put in place unasked")
	}
	stop()

	if err := SetUpdates(a, UpdatesInstall); err != nil {
		t.Fatal(err)
	}
	lookFor(t, a)
	select {
	case <-updated:
	case <-time.After(5 * time.Second):
		t.Fatal("set to install, the release was not put in place")
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "two" {
		t.Fatalf("set to install, the program holds %q", raw)
	}
}

// An update runs only with a signature App.UpdateKey checks, and comes
// over https only, from anywhere but this computer.
func TestUpdatesAreSigned(t *testing.T) {
	srv := fakeGitHub(t, release{tag: "v2.0.0", files: map[string][]byte{asset("2.0.0"): []byte("two")}})
	dir := t.TempDir()
	exe := filepath.Join(dir, "studio"+exeSuffix)
	if err := os.WriteFile(exe, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey}
	r, _, err := Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	stranger := a
	stranger.UpdateKey = publicKey(other)
	unsigned := r
	unsigned.Signature = ""
	for name, c := range map[string]struct {
		a App
		r Release
	}{
		"signed with another key": {stranger, r},
		"with no signature":       {a, unsigned},
	} {
		if err := Stage(context.Background(), c.a, c.r); err == nil {
			t.Errorf("a release %s was put in place", name)
		}
		if raw, _ := os.ReadFile(exe); string(raw) != "one" {
			t.Fatalf("a release %s left the program holding %q", name, raw)
		}
	}
	plain := r
	plain.Program = "http://example.com/studio"
	if err := Stage(context.Background(), a, plain); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("a program over plain http: %v", err)
	}
	if err := Stage(context.Background(), a, r); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "two" {
		t.Fatalf("a signed release left the program holding %q", raw)
	}
	keyless := a
	keyless.UpdateKey = ""
	if err := keyless.check(); err == nil {
		t.Error("an App with Updates and no UpdateKey passed")
	}
}

// A copy that started before a newer version was installed leaves that
// version in place: Stage refuses an older release, and the updater
// neither stages it nor tells of it.
func TestAnOlderCopyLeavesANewerInstall(t *testing.T) {
	was, wasEvery := updateFirst, updateEvery
	updateFirst, updateEvery = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { updateFirst, updateEvery = was, wasEvery })
	srv := fakeGitHub(t, release{tag: "v1.5.0", files: map[string][]byte{asset("1.5.0"): []byte("five")}})
	dir := t.TempDir()
	exe := filepath.Join(dir, "studio"+exeSuffix)
	if err := os.WriteFile(exe, []byte("six"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := manifest{ID: "studio", Version: "v1.6.0", Exe: "studio" + exeSuffix, Updates: UpdatesInstall}
	if err := m.write(dir); err != nil {
		t.Fatal(err)
	}
	heard := make(chan Release, 4)
	a := App{Name: "studio", Version: "v1.4.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey,
		Available: func(r Release) { heard <- r }, Updated: func(r Release) { heard <- r }}
	r, newer, err := Check(context.Background(), a)
	if err != nil || !newer {
		t.Fatalf("Check %+v, %v, %v; v1.5.0 is newer than this build", r, newer, err)
	}
	if err := Stage(context.Background(), a, r); err == nil {
		t.Error("Stage put v1.5.0 over v1.6.0")
	}
	for _, mode := range []UpdateMode{UpdatesInstall, UpdatesNotify} {
		if err := SetUpdates(a, mode); err != nil {
			t.Fatal(err)
		}
		stop := lookFor(t, a)
		select {
		case r := <-heard:
			t.Errorf("set to %s, the updater acted on %s over v1.6.0", mode, r.Version)
		case <-time.After(200 * time.Millisecond):
		}
		stop()
		if raw, _ := os.ReadFile(exe); string(raw) != "six" {
			t.Fatalf("set to %s, the installed v1.6.0 now holds %q", mode, raw)
		}
	}
}

// Writers at once to one file, as an update and an installer, each
// write through a file of their own: the file ends whole, as one of
// them wrote it, with nothing left beside it.
func TestWritersAtOnceNeverMix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "studio")
	bodies := make([][]byte, 8)
	for i := range bodies {
		bodies[i] = bytes.Repeat([]byte{byte('a' + i)}, 1<<20)
	}
	done := make(chan error, len(bodies))
	for _, b := range bodies {
		go func() { done <- writeFile(path, b, 0o755) }()
	}
	for range bodies {
		if err := <-done; err != nil && !movesAside {
			t.Error(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	whole := false
	for _, b := range bodies {
		whole = whole || bytes.Equal(got, b)
	}
	if !whole {
		t.Fatalf("the file holds %d bytes mixed from the writers", len(got))
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "studio" && e.Name() != "studio.old" {
			t.Errorf("a write left %s", e.Name())
		}
	}
}

// A link in a zip, named as the program, is not taken for it.
func TestALinkInAZipIsNoProgram(t *testing.T) {
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	h := &zip.FileHeader{Name: "studio/studio"}
	h.SetMode(os.ModeSymlink | 0o777)
	f, _ := zw.CreateHeader(h)
	_, _ = f.Write([]byte("/usr/bin/evil"))
	_ = zw.Close()
	if body, err := unpack(zipped.Bytes(), "studio.zip", "studio"); err == nil {
		t.Fatalf("a link in the zip was taken as the program: %q", body)
	}
}

// stagedOver installs v1.0.0, holding "one", in a folder of its own,
// and stages v2.0.0, holding "two", over it, as an update does. It
// returns the old release's App, the program, and its folder.
func stagedOver(t *testing.T) (a App, exe, dir string) {
	t.Helper()
	srv := fakeGitHub(t, release{tag: "v2.0.0", files: map[string][]byte{asset("2.0.0"): []byte("two")}})
	dir = t.TempDir()
	exe = filepath.Join(dir, "studio"+exeSuffix)
	if err := os.WriteFile(exe, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := manifest{ID: "studio", Version: "v1.0.0", Exe: "studio" + exeSuffix, Updates: UpdatesInstall}
	if err := m.write(dir); err != nil {
		t.Fatal(err)
	}
	a = App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey}
	r, _, err := Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if err := Stage(context.Background(), a, r); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{exe: "two", exe + ".old": "one"} {
		if raw, _ := os.ReadFile(f); string(raw) != want {
			t.Fatalf("staged, %s holds %q, want %q", filepath.Base(f), raw, want)
		}
	}
	if _, ok := readTrial(exe); !ok {
		t.Fatal("staged, the release is not on trial")
	}
	return a, exe, dir
}

// A release that keeps ending as it starts gives way to the program
// before it, which the updates then leave in place.
func TestABadUpdateGivesWay(t *testing.T) {
	was, wasRun := updateFirst, trialRun
	updateFirst, trialRun = time.Millisecond, time.Hour
	t.Cleanup(func() { updateFirst, trialRun = was, wasRun })
	a, exe, dir := stagedOver(t)
	two := a
	two.Version = "v2.0.0"
	for start := 1; start <= trialStarts; start++ {
		if onTrial(two, exe) {
			t.Fatalf("start %d of %d gave way", start, trialStarts)
		}
		if tr, _ := readTrial(exe); tr.Starts != start {
			t.Fatalf("start %d counted as %d", start, tr.Starts)
		}
	}
	if !onTrial(two, exe) {
		t.Fatalf("start %d, after %d that ended soon, did not give way", trialStarts+1, trialStarts)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "one" {
		t.Fatalf("given way, the program holds %q", raw)
	}
	if _, ok := readTrial(exe); ok {
		t.Error("given way, the trial stayed")
	}
	if m, _ := readManifest(dir, "studio"); m == nil || m.Skip != "v2.0.0" {
		t.Fatalf("given way, the install keeps %+v", m)
	}
	heard := make(chan Release, 1)
	a.Updated = func(r Release) { heard <- r }
	stop := lookFor(t, a)
	select {
	case r := <-heard:
		t.Errorf("the updater put %s in place again", r.Version)
	case <-time.After(200 * time.Millisecond):
	}
	stop()
	if raw, _ := os.ReadFile(exe); string(raw) != "one" {
		t.Fatalf("the updater put the release that gave way back: %q", raw)
	}
}

// A release that runs a while passes, and the program before it goes.
func TestAGoodUpdatePasses(t *testing.T) {
	was := trialRun
	trialRun = 20 * time.Millisecond
	t.Cleanup(func() { trialRun = was })
	a, exe, _ := stagedOver(t)
	a.Version = "v2.0.0"
	if onTrial(a, exe) {
		t.Fatal("a first start gave way")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, onIt := readTrial(exe)
		_, oldErr := os.Stat(exe + ".old")
		if !onIt && oldErr != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after running a while, the trial is %v and the old program %v", onIt, oldErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "two" {
		t.Fatalf("passed, the program holds %q", raw)
	}
}

// Moving the program aside for a new one, a copy moved aside before
// that cannot go, as one still running from the tray on Windows, is
// moved out of the way, and the program before goes to .old all the
// same, for the update to give way to.
func TestMoveAsidePastABusyOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "studio")
	if err := os.WriteFile(path, []byte("two"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A folder with a file in it cannot be removed, as a running
	// program's file cannot on Windows.
	if err := os.MkdirAll(filepath.Join(path+".old", "running"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := moveAside(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(old); old != path+".old" || string(raw) != "two" {
		t.Fatalf("moved aside to %s, holding %q", old, raw)
	}
	if busy := leftovers(path, ".old"); len(busy) != 1 {
		t.Fatalf("the copy in the way went to %v, want one name of its own", busy)
	}
}

// A newer release put in place while one is on trial keeps the last
// one that passed: a bad v3 over an untried v2 gives way to v1.
func TestANewerReleaseOnTrialKeepsTheOneThatPassed(t *testing.T) {
	was := trialRun
	trialRun = time.Hour
	t.Cleanup(func() { trialRun = was })
	a, exe, _ := stagedOver(t)
	srv := fakeGitHub(t, release{tag: "v3.0.0", files: map[string][]byte{asset("3.0.0"): []byte("three")}})
	a.Updates = GitHub{Repo: "marrasen/studio", API: srv.URL}
	r, _, err := Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if err = Stage(context.Background(), a, r); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{exe: "three", exe + ".old": "one"} {
		if raw, _ := os.ReadFile(f); string(raw) != want {
			t.Fatalf("v3 staged over v2 on trial: %s holds %q, want %q", filepath.Base(f), raw, want)
		}
	}
	three := a
	three.Version = "v3.0.0"
	for range trialStarts {
		onTrial(three, exe)
	}
	if !onTrial(three, exe) {
		t.Fatal("v3, ending as it started, did not give way")
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "one" {
		t.Fatalf("v3 gave way to %q, want v1, the last that passed", raw)
	}
}

// A download that fails while a release is on trial leaves all as it
// was: the release, its trial, and the program kept before it.
func TestAFailedDownloadOnTrialChangesNothing(t *testing.T) {
	a, exe, _ := stagedOver(t)
	r, _, err := Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.Name, bad.Version = "nothing", "v4.0.0"
	if err = Stage(context.Background(), a, bad); err == nil {
		t.Fatal("staged a release whose sums say nothing of it")
	}
	for f, want := range map[string]string{exe: "two", exe + ".old": "one"} {
		if raw, _ := os.ReadFile(f); string(raw) != want {
			t.Errorf("after a failed download, %s holds %q, want %q", filepath.Base(f), raw, want)
		}
	}
	if tr, ok := readTrial(exe); !ok || tr.Version != "v2.0.0" {
		t.Errorf("after a failed download the trial is %+v, %v", tr, ok)
	}
	// The same release again, as from a second copy of the program, is
	// in place already: nothing moves.
	if err = Stage(context.Background(), a, r); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(exe + ".old"); string(raw) != "one" {
		t.Errorf("staged again, .old holds %q, want v1", raw)
	}
}

// The program being replaced, started just as a newer release is put
// in place, leaves the trial and what it keeps alone.
func TestTheProgramBeingReplacedLeavesTheTrial(t *testing.T) {
	a, exe, _ := stagedOver(t)
	if onTrial(a, exe) {
		t.Fatal("v1, started beside v2's trial, gave way")
	}
	if tr, ok := readTrial(exe); !ok || tr.Version != "v2.0.0" || tr.Starts != 0 {
		t.Errorf("v1's start left the trial %+v, %v", tr, ok)
	}
	if raw, _ := os.ReadFile(exe + ".old"); string(raw) != "one" {
		t.Errorf("v1's start left .old holding %q", raw)
	}
}

// A redirect to plain http on this computer, from a server out there,
// is refused: only a request that began here may come back here.
func TestARedirectHomeIsRefused(t *testing.T) {
	req := func(addr string) *http.Request {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, addr, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	home := req("http://127.0.0.1:9/secret")
	if err := client.CheckRedirect(home, []*http.Request{req("https://example.com/program")}); err == nil {
		t.Error("a redirect from https out there to http on this computer was followed")
	}
	if err := client.CheckRedirect(home, []*http.Request{req("http://127.0.0.1:8000/program")}); err != nil {
		t.Errorf("a redirect between servers on this computer was refused: %v", err)
	}
}
