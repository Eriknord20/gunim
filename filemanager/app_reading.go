package filemanager

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/marrasen/gunim"
)

// mostReading is how much of a file the text viewer reads; a larger
// file shows its start, and says so.
const mostReading = 4 << 20

// readingState is the text viewer: whether it shows, and the file.
type readingState struct {
	open   bool
	opened int
	id     gunim.ID
	seq    int
}

// Reading is the text viewer's state. Data is the file's bytes, or its
// first mostReading with Cut set, once read; Loading is set until then.
type Reading struct {
	Seq               int
	Path, Name, Shown string
	Size              string
	Data              []byte
	Cut, Loading      bool
	Err               string
}

// Intents of the text viewer.
type (
	// ReadingClosed says the text viewer closed itself.
	ReadingClosed struct{ Seq int }
	// ReadingLink asks for a link clicked in a document to open.
	ReadingLink struct {
		Seq int
		URL string
	}
)

func init() {
	gunim.RegisterType[Reading]("files.reading")
	gunim.RegisterType[ReadingClosed]("files.reading-closed")
	gunim.RegisterType[ReadingLink]("files.reading-link")
}

// handleReading takes the intents of the text viewer.
func (a *app) handleReading(in gunim.Intent) bool {
	switch v := in.(type) {
	case ReadingClosed:
		if a.reading.open && v.Seq == a.reading.seq {
			a.reading.open = false
			a.patch(FocusListing{})
		}
	case ReadingLink:
		a.followLink(v.URL)
	default:
		return false
	}
	return true
}

// followLink opens a link clicked in a document in the browser: a web
// address alone. A document may point anywhere, as at a program a
// scheme would start, and nothing else is opened.
func (a *app) followLink(url string) {
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		a.patch(Notice{Title: "Only web links open from here", Body: url, Kind: "info"})
		return
	}
	if err := a.c.OpenLink(url); err != nil {
		a.fail("Opening " + url + ": " + err.Error())
	}
}

// openReading shows the file e of the folder showing in the text viewer,
// reading its start on a goroutine.
func (a *app) openReading(e entry) {
	n := &a.nav
	path := a.ps.Join(n.path, e.Name)
	r := &a.reading
	r.seq++
	st := Reading{Seq: r.seq, Path: path, Name: e.Name, Shown: a.ps.Show(path), Loading: true}
	if !e.Dir {
		st.Size = humanBytes(e.Size)
	}
	if r.open {
		a.send(a.c.Update(r.id, st))
	} else {
		r.open = true
		r.opened++
		r.id = a.ids.reading(r.opened)
		a.send(a.c.Mount(gunim.Root, r.id, "reading", st))
	}
	seq, fsys := r.seq, a.fs
	go func() {
		data, cut, err := readStart(fsys, path, mostReading)
		a.post(func() {
			if !r.open || seq != r.seq {
				return
			}
			st.Loading, st.Data, st.Cut = false, data, cut
			if err != nil {
				st.Err = err.Error()
				a.logLine("Viewing "+st.Shown, st.Err)
			}
			a.send(a.c.Update(r.id, st))
		})
	}()
}

// readStart reads up to most bytes of the file at path, and says
// whether there was more. Only a file is read: a device or a pipe could
// give without end, or never.
func readStart(fsys FS, path string, most int64) (data []byte, cut bool, err error) {
	ps := fsys.Paths()
	info, err := fsys.Stat(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", ps.Show(path), err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a file that can be read", ps.Show(path))
	}
	f, err := fsys.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", ps.Show(path), err)
	}
	data, err = io.ReadAll(io.LimitReader(f, most+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", ps.Show(path), err)
	}
	if int64(len(data)) > most {
		return data[:most], true, nil
	}
	return data, false, nil
}
