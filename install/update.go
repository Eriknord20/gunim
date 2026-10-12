package install

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Source is where newer releases of a program come from.
type Source interface {
	// Latest is the newest release with a program for this system.
	Latest(ctx context.Context) (Release, error)
}

// Release is one release of a program, with its program for this
// system.
type Release struct {
	// Version is the release's version, as "v1.3.0".
	Version string
	// Page is the release's page on the web, and Notes what it says of
	// the release.
	Page, Notes string
	// Name is the file the program comes in: the program itself, or a
	// .zip or .tar.gz holding it. Program is where it downloads from,
	// Sums where the SHA256SUMS file that checks it does, and Signature
	// where SHA256SUMS.sig, its signature, does. Each is an https URL.
	Name, Program, Sums, Signature string
}

// GitHub finds a program's releases on GitHub.
//
// Each release holds the program for each system as a file named
// <repository>_<version>_<goos>_<goarch>, with ".exe" on Windows, as
// "mastering-studio_1.3.0_windows_amd64.exe", or that name with ".zip"
// or ".tar.gz" for the program in an archive, a SHA256SUMS file of
// their checksums, as sha256sum writes it, and SHA256SUMS.sig, its
// signature, as "gunimsign SHA256SUMS" writes it.
type GitHub struct {
	// Repo is the repository, as "marrasen/mastering-studio".
	Repo string
	// Prerelease takes pre-releases too, for a program's beta testers.
	Prerelease bool
	// Asset, when set, names the file of the program for a version, as
	// "1.3.0", and a system, in place of the usual names.
	Asset func(version, goos, goarch string) string
	// API is GitHub's API, for a test to stand in for it.
	API string
}

// apiPatience is how long GitHub is given to say what it has, and
// fetchPatience how long a program takes to download.
const (
	apiPatience   = 15 * time.Second
	fetchPatience = 10 * time.Minute
	// readLimit is the most of an answer read, and programLimit the
	// largest program fetched.
	readLimit    = 4 << 20
	programLimit = 1 << 30
)

// ghRelease is a release as GitHub's API tells of it.
type ghRelease struct {
	Tag        string `json:"tag_name"`
	Page       string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// releases asks GitHub for the repository's latest releases, and keeps
// those the updates take: published, and pre-releases only when asked.
func (g GitHub) releases(ctx context.Context) ([]ghRelease, error) {
	if !strings.Contains(g.Repo, "/") {
		return nil, fmt.Errorf("install: GitHub.Repo %q is not owner/name", g.Repo)
	}
	api := g.API
	if api == "" {
		api = "https://api.github.com"
	}
	ctx, cancel := context.WithTimeout(ctx, apiPatience)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+g.Repo+"/releases?per_page=30", http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gunim-install")
	resp, err := do(req)
	if err != nil {
		return nil, fmt.Errorf("ask GitHub for releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var said []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, readLimit)).Decode(&said); err != nil {
		return nil, fmt.Errorf("read what GitHub answered: %w", err)
	}
	return slices.DeleteFunc(said, func(r ghRelease) bool {
		return r.Draft || r.Prerelease && !g.Prerelease || !IsRelease(r.Tag)
	}), nil
}

// ReleaseNotes implements [Changelog].
func (g GitHub) ReleaseNotes(ctx context.Context) ([]ReleaseNotes, error) {
	said, err := g.releases(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReleaseNotes, 0, len(said))
	for _, r := range said {
		out = append(out, ReleaseNotes{Version: r.Tag, Page: r.Page, Notes: r.Body})
	}
	return out, nil
}

// Latest implements [Source].
func (g GitHub) Latest(ctx context.Context) (Release, error) {
	said, err := g.releases(ctx)
	if err != nil {
		return Release{}, err
	}
	var best Release
	for _, r := range said {
		if best.Version != "" && compare(r.Tag, best.Version) <= 0 {
			continue
		}
		assets := map[string]string{}
		for _, a := range r.Assets {
			assets[a.Name] = a.URL
		}
		sums, ok := assets["SHA256SUMS"]
		if !ok {
			continue
		}
		for _, name := range g.names(strings.TrimPrefix(r.Tag, "v")) {
			if at, ok := assets[name]; ok {
				best = Release{Version: r.Tag, Page: r.Page, Notes: r.Body, Name: name, Program: at, Sums: sums,
					Signature: assets["SHA256SUMS.sig"]}
				break
			}
		}
	}
	if best.Version == "" {
		return Release{}, fmt.Errorf("%s has no release with a program for %s/%s", g.Repo, runtime.GOOS, runtime.GOARCH)
	}
	return best, nil
}

// names are the names the program for this system may have in the
// release of version.
func (g GitHub) names(version string) []string {
	if g.Asset != nil {
		return []string{g.Asset(version, runtime.GOOS, runtime.GOARCH)}
	}
	base := path.Base(g.Repo) + "_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH
	return []string{base + exeSuffix, base + ".zip", base + ".tar.gz"}
}

// ReleaseNotes is what one release changed, as its notes say.
type ReleaseNotes struct {
	// Version is the release's version, as "v1.3.0", Page its page on
	// the web, and Notes what it says of the release, in Markdown.
	Version, Page, Notes string
}

// Changelog is a [Source] that can say what each of its releases
// changed, as [GitHub] can.
type Changelog interface {
	// ReleaseNotes are the notes of the releases, in any order.
	ReleaseNotes(ctx context.Context) ([]ReleaseNotes, error)
}

// WhatsNew is what the releases after version after changed, up to and
// with version upTo, newest first, for a program to show what an update
// brings, or brought. A source that is no [Changelog] tells only of the
// release it has newest, when that is upTo.
func WhatsNew(ctx context.Context, a App, after, upTo string) ([]ReleaseNotes, error) {
	if a.Updates == nil {
		return nil, errors.New("install: App.Updates is not set")
	}
	within := func(v string) bool {
		return IsRelease(v) && (!IsRelease(after) || Newer(v, after)) && (!IsRelease(upTo) || !Newer(v, upTo))
	}
	cl, ok := a.Updates.(Changelog)
	if !ok {
		r, err := a.Updates.Latest(ctx)
		if err != nil {
			return nil, err
		}
		if !within(r.Version) {
			return nil, nil
		}
		return []ReleaseNotes{{Version: r.Version, Page: r.Page, Notes: r.Notes}}, nil
	}
	all, err := keptNotes(ctx, cl)
	if err != nil {
		return nil, err
	}
	all = slices.DeleteFunc(all, func(c ReleaseNotes) bool { return !within(c.Version) })
	slices.SortFunc(all, func(x, y ReleaseNotes) int { return compare(y.Version, x.Version) })
	return all, nil
}

// notesFor is how long the releases' notes are kept once read, so a
// window opened again soon shows them without asking again: GitHub
// answers a program that is not signed in 60 times an hour.
var notesFor = 10 * time.Minute

// notesKept are the notes read last, from which changelog, and when.
var notesKept struct {
	sync.Mutex
	from  string
	at    time.Time
	notes []ReleaseNotes
}

// keptNotes are cl's notes, read again only once notesFor has passed.
// The caller may change the slice it gets.
func keptNotes(ctx context.Context, cl Changelog) ([]ReleaseNotes, error) {
	from := fmt.Sprintf("%T %+v", cl, cl)
	notesKept.Lock()
	if notesKept.from == from && time.Since(notesKept.at) < notesFor {
		out := slices.Clone(notesKept.notes)
		notesKept.Unlock()
		return out, nil
	}
	notesKept.Unlock()
	all, err := cl.ReleaseNotes(ctx)
	if err != nil {
		return nil, err
	}
	notesKept.Lock()
	notesKept.from, notesKept.at, notesKept.notes = from, time.Now(), slices.Clone(all)
	notesKept.Unlock()
	return all, nil
}

// Check asks a's source for its newest release, and reports whether it
// is newer than this build. A build that is no release has no order
// against one, and is never behind.
func Check(ctx context.Context, a App) (Release, bool, error) {
	if a.Updates == nil {
		return Release{}, false, errors.New("install: App.Updates is not set")
	}
	r, err := a.Updates.Latest(ctx)
	if err != nil {
		return Release{}, false, err
	}
	return r, IsRelease(a.Version) && Newer(r.Version, a.Version), nil
}

// stageMu keeps two stagings of a program from running at once.
var stageMu sync.Mutex

// Stage downloads release r, checks it against its SHA256SUMS and their
// signature, and puts
// it in place of the installed program, for the next time the program
// starts; the program running goes on as it is. The rest of r's files
// are put in place as it first starts. A copy installed already that is
// as new as r, or newer, stays, as when a copy started before it was
// installed still runs. The program r replaces is kept until r has run a
// while; a release that keeps ending as it starts gives way to it, and
// the updates pass that release over.
func Stage(ctx context.Context, a App, r Release) error {
	return stageReporting(ctx, a, r, nil)
}

// stageReporting is [Stage], telling report how the download goes.
func stageReporting(ctx context.Context, a App, r Release, report func(Progress)) error {
	if err := a.check(); err != nil {
		return err
	}
	// One at a time, as the updates and a window may put the same
	// release in place at once.
	stageMu.Lock()
	defer stageMu.Unlock()
	dir, err := a.dir()
	if err != nil {
		return err
	}
	m, err := readManifest(dir, a.id())
	if err != nil {
		return err
	}
	if m != nil && !newerThanInstalled(r, m.Version) {
		return fmt.Errorf("install: %s is installed already, and %s is no newer", m.Version, r.Version)
	}
	exe := filepath.Join(dir, a.exe())
	t, onTrial := readTrial(exe)
	if onTrial && t.Version == r.Version {
		// In place already, and on trial.
		return nil
	}
	body, err := download(ctx, a, r, report)
	if err != nil {
		// A download that fails leaves the program and what is kept of
		// the one before as they were.
		return err
	}
	// A release on trial has the last one that passed kept already: that
	// one stays kept, and the release on trial goes, so a release that
	// keeps ending as it starts gives way to one that ran. Otherwise the
	// program now is kept.
	kept := onTrial && keptOld(exe)
	if !onTrial {
		kept = keepOld(exe)
	}
	// The trial goes down first, so a start in between, of the program
	// being replaced, finds a newer release on trial and leaves all as
	// it is: see onTrial.
	if kept {
		if err := (trial{Version: r.Version}).write(exe); err != nil {
			return err
		}
	} else {
		_ = os.Remove(trialPath(exe))
	}
	// On trial, the program kept stays as it is, and the one on trial
	// goes aside; otherwise, where the program is moved aside, Replace
	// moves it to .old, which keeps it.
	keep := onTrial && kept
	place := func(part, to string) error { return replaceKeeping(part, to, keep) }
	if err := writeVia(exe, 0o755, place, func(f *os.File) error { _, err := f.Write(body); return err }); err != nil {
		if onTrial {
			_ = t.write(exe)
		} else {
			_ = os.Remove(trialPath(exe))
		}
		return err
	}
	return nil
}

// StageTo is [Stage] for the program at exe, as a copy that is not
// installed updates itself where it is.
func StageTo(ctx context.Context, a App, r Release, exe string) error {
	return stageToReporting(ctx, a, r, exe, nil)
}

// stageToReporting is [StageTo], telling report how the download goes.
func stageToReporting(ctx context.Context, a App, r Release, exe string, report func(Progress)) error {
	body, err := download(ctx, a, r, report)
	if err != nil {
		return err
	}
	return writeFile(exe, body, 0o755)
}

// download fetches r's program for a, checked, telling report how it
// goes, and returns it to put in place.
func download(ctx context.Context, a App, r Release, report func(Progress)) ([]byte, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	key, err := a.updateKey()
	if err != nil {
		return nil, err
	}
	body, err := fetch(ctx, a.exe(), key, r, report)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		// Stopped once it had come: nothing is put in place.
		return nil, err
	}
	if report != nil {
		report(Progress{Step: "Putting it in place", Done: 0.95})
	}
	return body, nil
}

// newerThanInstalled reports whether r is newer than the version
// installed: always, where the install kept no version that is a
// release.
func newerThanInstalled(r Release, installed string) bool {
	return !IsRelease(installed) || Newer(r.Version, installed)
}

// fetch downloads r's program, checked against its SHA256SUMS, whose
// signature key checks, taking the file named program out of an
// archive. report, when set, hears how the download goes: the program
// is most of the work, up to 0.9 of it.
func fetch(ctx context.Context, program string, key ed25519.PublicKey, r Release, report func(Progress)) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchPatience)
	defer cancel()
	if r.Sums == "" {
		return nil, fmt.Errorf("%s has no SHA256SUMS to check it against", r.Version)
	}
	if r.Signature == "" {
		return nil, fmt.Errorf("%s has no SHA256SUMS.sig, the signature that says who made it", r.Version)
	}
	sums, err := get(ctx, r.Sums, readLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	sig, err := get(ctx, r.Signature, readLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch SHA256SUMS.sig: %w", err)
	}
	if !verify(key, sums, sig) {
		return nil, fmt.Errorf("the SHA256SUMS of %s is not signed with App.UpdateKey", r.Version)
	}
	want, err := sumOf(sums, r.Name)
	if err != nil {
		return nil, err
	}
	var got func(done, total int64)
	if report != nil {
		report(Progress{Step: "Downloading"})
		got = func(done, total int64) {
			if total <= 0 {
				report(Progress{Step: "Downloading " + Bytes(done)})
				return
			}
			report(Progress{Step: "Downloading " + Bytes(done) + " of " + Bytes(total), Done: 0.9 * float32(done) / float32(total)})
		}
	}
	body, err := getting(ctx, r.Program, programLimit, got)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", r.Name, err)
	}
	if report != nil {
		report(Progress{Step: "Checking it", Done: 0.9})
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("%s is not what SHA256SUMS says it is", r.Name)
	}
	switch {
	case strings.HasSuffix(r.Name, ".zip"), strings.HasSuffix(r.Name, ".tar.gz"):
		body, err = unpack(body, r.Name, program)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

// verify reports whether sig, as gunimsign writes it, is key's
// signature of sums.
func verify(key ed25519.PublicKey, sums, sig []byte) bool {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	return err == nil && len(raw) == ed25519.SignatureSize && ed25519.Verify(key, sums, raw)
}

// client fetches over https only, redirects too, so no one on the way
// can change what comes; plain http only from this computer, as from a
// test's server, and a redirect to it only from it, so a server out
// there cannot send a request to a service of this computer's.
var client = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme == "https" {
		return nil
	}
	if first := via[0].URL; first.Scheme == "http" && loopback(first.Hostname()) {
		return secure(req.URL)
	}
	return fmt.Errorf("install: %s is not https", req.URL.Redacted())
}}

// secure says what is wrong with fetching u, or nothing.
func secure(u *url.URL) error {
	if u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("install: %s is not https", u.Redacted())
}

// loopback reports whether host is this computer.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// do sends req through client, once secure has passed its URL.
func do(req *http.Request) (*http.Response, error) {
	if err := secure(req.URL); err != nil {
		return nil, err
	}
	return client.Do(req)
}

// get fetches addr, at most limit bytes of it.
func get(ctx context.Context, addr string, limit int64) ([]byte, error) {
	return getting(ctx, addr, limit, nil)
}

// reportEvery is how often a download says how far it has got.
const reportEvery = 100 * time.Millisecond

// getting is [get], telling report, when set, how many bytes have come,
// and of how many, or 0 when the server does not say.
func getting(ctx context.Context, addr string, limit int64, report func(done, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gunim-install")
	resp, err := do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	from := io.LimitReader(resp.Body, limit+1)
	if report != nil {
		from = &counting{r: from, total: max(resp.ContentLength, 0), report: report}
	}
	body, err := io.ReadAll(from)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("it is larger than any program fetched")
	}
	if report != nil {
		report(int64(len(body)), max(resp.ContentLength, 0))
	}
	return body, nil
}

// counting tells report how much of a download has been read, now and
// then.
type counting struct {
	r           io.Reader
	done, total int64
	last        time.Time
	report      func(done, total int64)
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if now := time.Now(); now.Sub(c.last) >= reportEvery {
		c.last = now
		c.report(c.done, c.total)
	}
	return n, err
}

// sumOf is the checksum SHA256SUMS gives name.
func sumOf(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS says nothing of %s", name)
}

// unpack is the file called program in archive, at any depth.
func unpack(archive []byte, name, program string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) != program || !f.Mode().IsRegular() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			out, err := readProgram(rc)
			_ = rc.Close()
			return out, err
		}
		return nil, fmt.Errorf("%s holds no %s", name, program)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("%s holds no %s", name, program)
		}
		if path.Base(h.Name) == program && h.Typeflag == tar.TypeReg {
			return readProgram(tr)
		}
	}
}

// readProgram reads a program out of an archive, and fails on one
// larger than any program fetched, in place of cutting it short.
func readProgram(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, programLimit+1))
	if err == nil && int64(len(body)) > programLimit {
		err = errors.New("the program in the archive is larger than any program fetched")
	}
	return body, err
}

// Restart starts the installed program with args, for a program to call
// just before it ends, to come back as the release [Stage] put in place.
func Restart(a App, args ...string) error {
	dir, err := a.dir()
	if err != nil {
		return err
	}
	return launch(filepath.Join(dir, a.exe()), args)
}

// How soon after it starts an installed program first looks for a newer
// release, and how often after that: often enough that a program left
// open all day finds a release the same day.
var (
	updateFirst = time.Minute
	updateEvery = 4 * time.Hour
)

// keepUpToDate looks for newer releases of a while ctx lasts, as the
// install's update mode says at each look: it puts one in place for the
// next start, or tells the program of it, once a release.
func keepUpToDate(ctx context.Context, a App) {
	told := ""
	wait := updateFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateEvery
		in, err := Find(a)
		if err != nil || in.Updates == UpdatesOff || in.Updates == UpdatesNotify && a.Available == nil {
			continue
		}
		r, newer, err := Check(ctx, a)
		if err != nil || !newer || r.Version == told {
			continue
		}
		if !newerThanInstalled(r, in.Version) || r.Version == skipped(a) {
			// Installed since this copy started, as a newer version, or a
			// release that gave way.
			continue
		}
		if in.Updates == UpdatesNotify {
			told = r.Version
			a.Available(r)
			continue
		}
		if err := Stage(ctx, a, r); err != nil {
			continue
		}
		told = r.Version
		if a.Updated != nil {
			a.Updated(r)
		}
	}
}
