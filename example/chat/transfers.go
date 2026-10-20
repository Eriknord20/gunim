package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/marrasen/gunim/chat"
)

// transfer is a file on its way up to a project's files: its name, the folder it goes to, how many of its bytes
// have gone, and how it stands.
type transfer struct {
	id, name, folder string
	size, done       int64
	state            chat.TransferState
	reason           string
	// seq changes with each start, so a tick from an attempt given up does nothing.
	seq int
}

// Upload speeds and steps, as a simulated connection manages them.
const (
	uploadTick    = 100 * time.Millisecond
	encryptFor    = 400 * time.Millisecond
	uploadedShown = 4 * time.Second
)

// upload starts sending the files at paths into the folder at path of p. A file that cannot be read fails at once,
// with the reason, and the rest go on.
func (a *app) upload(p *project, folder string, paths []string) {
	for _, path := range paths {
		a.nextID++
		t := &transfer{id: "t" + strconv.Itoa(a.nextID), name: filepath.Base(path), folder: folder}
		p.transfers = append(p.transfers, t)
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			t.state, t.reason = chat.TransferFailed, "the file is not there"
			continue
		case err != nil:
			t.state, t.reason = chat.TransferFailed, err.Error()
			continue
		case info.IsDir():
			t.state, t.reason = chat.TransferFailed, "folders cannot be uploaded yet"
			continue
		}
		t.size = info.Size()
		a.startTransfer(p, t)
	}
	a.publish()
}

// startTransfer encrypts t and sends it, or has it wait while offline.
func (a *app) startTransfer(p *project, t *transfer) {
	t.seq++
	t.done, t.reason = 0, ""
	if a.link != chat.Online {
		t.state = chat.TransferWaiting
		return
	}
	t.state = chat.TransferEncrypting
	seq := t.seq
	a.after(encryptFor, func() {
		if t.seq != seq || t.state != chat.TransferEncrypting {
			return
		}
		if a.link != chat.Online {
			t.state = chat.TransferWaiting
			a.publish()
			return
		}
		t.state = chat.TransferUploading
		a.tickTransfer(p, t, seq)
		a.publish()
	})
}

// tickTransfer sends the next part of t, and when all of it has gone, puts the file in its folder.
func (a *app) tickTransfer(p *project, t *transfer, seq int) {
	a.after(uploadTick, func() {
		if t.seq != seq || t.state != chat.TransferUploading {
			return
		}
		switch {
		case a.link != chat.Online:
			t.state = chat.TransferWaiting
		case a.rng.Float64() < a.failRate/20:
			t.state, t.reason = chat.TransferFailed, "the connection dropped"
		default:
			// Somewhere from 2 to 6 MB a second.
			t.done = min(t.size, t.done+int64(200_000+a.rng.IntN(400_000)))
			if t.done < t.size {
				a.tickTransfer(p, t, seq)
				break
			}
			t.state = chat.TransferUploaded
			a.store(p, t)
			a.after(uploadedShown, func() {
				if t.seq == seq {
					p.transfers = slices.DeleteFunc(p.transfers, func(o *transfer) bool { return o == t })
					a.publish()
				}
			})
		}
		a.publish()
	})
}

// store puts the file t sent into its folder, in place of one of the same name.
func (a *app) store(p *project, t *transfer) {
	f, ok := p.folderAt(t.folder)
	if !ok {
		// The folder went while the file was on its way: the file goes to the top.
		f = p.files
	}
	nf := &file{name: t.name, size: t.size, at: time.Now().Round(0), by: me}
	if i := slices.IndexFunc(f.files, func(x *file) bool { return x.name == t.name }); i >= 0 {
		f.files[i] = nf
		return
	}
	f.files = append(f.files, nf)
}

// resumeTransfers starts again the transfers that waited for the connection.
func (a *app) resumeTransfers() {
	for _, p := range a.projects {
		for _, t := range p.transfers {
			if t.state == chat.TransferWaiting {
				a.startTransfer(p, t)
			}
		}
	}
}

// transfersOf returns p's transfers as the view shows them.
func transfersOf(p *project) []chat.Transfer {
	out := make([]chat.Transfer, 0, len(p.transfers))
	for _, t := range p.transfers {
		out = append(out, chat.Transfer{ID: t.id, Name: t.name, Folder: t.folder, Size: t.size, Done: t.done,
			State: t.state, Reason: t.reason})
	}
	return out
}
