package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// XDGTrash returns the trash in dir on fsys, laid out as the
// freedesktop.org trash specification has it: the items in files, and a
// .trashinfo record of each in info. A program gives a file system of its
// own a trash this way, as a server's over SFTP, whose trash is in
// ~/.local/share/Trash. Moving an item to it renames the item, so an item
// on another volume than dir cannot go.
func XDGTrash(fsys FS, dir string) TrashCan { return xdgTrash{fs: fsys, dir: dir} }

// xdgTrash is a trash laid out as the freedesktop.org trash specification
// has it: the items in files, and a .trashinfo record of each in info.
type xdgTrash struct {
	// fs is the file system the trash is on, and the computer's own where
	// nil.
	fs  FS
	dir string
}

// on is the file system the trash is on, and how it writes paths.
func (t xdgTrash) on() (FS, PathStyle) {
	if t.fs == nil {
		return LocalFS(), SystemPaths
	}
	return t.fs, t.fs.Paths()
}

// infoExt ends the name of each record in info.
const infoExt = ".trashinfo"

// trashDate is how a record writes when its item was deleted, in local
// time.
const trashDate = "2006-01-02T15:04:05"

// Trash implements [Trasher].
func (t xdgTrash) Trash(path string) (string, error) {
	fsys, ps := t.on()
	abs, err := ps.Abs(path)
	if err != nil {
		return "", fmt.Errorf("moving %s to the trash: %w", path, err)
	}
	if _, err := fsys.Lstat(abs); err != nil {
		return "", fmt.Errorf("moving %s to the trash: %w", abs, err)
	}
	for _, d := range []string{ps.Join(t.dir, "files"), ps.Join(t.dir, "info")} {
		err := mkdirAll(fsys, d, 0o700)
		if errors.Is(err, fs.ErrPermission) {
			return "", cannotTrash{why: "the trash in " + t.dir + " cannot be made, as permission is denied", err: err}
		}
		if err != nil {
			return "", fmt.Errorf("making the trash: %w", err)
		}
	}
	base := ps.Base(abs)
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = numbered(base, n)
		}
		dest, ok, err := t.claim(abs, name)
		if err != nil {
			return "", err
		}
		if ok {
			return dest, nil
		}
	}
}

// claim moves abs into the trash as name, and reports false when name is
// taken there.
func (t xdgTrash) claim(abs, name string) (dest string, ok bool, err error) {
	fsys, ps := t.on()
	record := ps.Join(t.dir, "info", name+infoExt)
	dest = ps.Join(t.dir, "files", name)
	// The record is made first and alone, which claims the name.
	f, err := fsys.Create(record)
	if errors.Is(err, fs.ErrExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("writing the trash record for %s: %w", abs, err)
	}
	_, taken := fsys.Lstat(dest)
	if taken == nil {
		if err := errors.Join(f.Close(), fsys.Remove(record)); err != nil {
			return "", false, fmt.Errorf("writing the trash record for %s: %w", abs, err)
		}
		return "", false, nil
	}
	if !errors.Is(taken, fs.ErrNotExist) {
		return "", false, errors.Join(fmt.Errorf("looking in the trash: %w", taken), f.Close(), fsys.Remove(record))
	}
	body := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: t.slashed(abs)}).EscapedPath(), time.Now().Format(trashDate))
	_, werr := f.Write([]byte(body))
	if err := errors.Join(werr, f.Close()); err != nil {
		return "", false, errors.Join(fmt.Errorf("writing the trash record for %s: %w", abs, err), fsys.Remove(record))
	}
	if err := fsys.Rename(abs, dest); err != nil {
		// Only the computer's own file system says for sure that a rename
		// crossed volumes. Another, as a server's over SFTP, may say so of
		// any failure, as of a mount point that is busy, and its item is
		// not offered to be deleted for good.
		if errors.Is(err, ErrCrossDevice) && fsys.ID() == "" {
			err = cannotTrash{why: abs + " is on another volume than the trash", err: err}
		} else {
			err = fmt.Errorf("moving %s to the trash: %w", abs, err)
		}
		if rerr := fsys.Remove(record); rerr != nil {
			return "", false, errors.Join(err, rerr)
		}
		return "", false, err
	}
	return dest, true, nil
}

// Restore implements [Trasher]: it checks the item's .trashinfo record
// says it came from original, moves it to to, and removes the record.
func (t xdgTrash) Restore(original, trashed string, _ time.Time, to string) error {
	if trashed == "" {
		return errNoRestore
	}
	fsys, ps := t.on()
	record := t.recordOf(trashed)
	from, _, err := t.read(record)
	if err != nil {
		return err
	}
	if !ps.Same(from, original) {
		return fmt.Errorf("the trash record %s says the item came from %s, not %s", record, from, original)
	}
	if _, err := fsys.Lstat(to); err == nil {
		return fmt.Errorf("%s exists again; move it away to restore the one in the trash", to)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("restoring %s: %w", to, err)
	}
	if err := mkdirAll(fsys, ps.Dir(to), 0o755); err != nil {
		return fmt.Errorf("restoring %s: %w", to, err)
	}
	if err := t.renameAbsent(trashed, to); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s exists again; move it away to restore the one in the trash", to)
		}
		return fmt.Errorf("restoring %s: %w", to, err)
	}
	// A record gone already went with a purge of the item in the moment
	// between, which the restore was quicker than.
	if err := fsys.Remove(record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("restored %s, but removing its trash record failed: %w", to, err)
	}
	return nil
}

// renameAbsent moves an item to a path where nothing is. Where the file
// system renames the item so in one step, an item that turns up at the
// path meanwhile is never replaced; elsewhere it looks at the path just
// before, which leaves that moment.
func (t xdgTrash) renameAbsent(from, to string) error {
	fsys, _ := t.on()
	if r, ok := fsys.(NoReplaceRenamer); ok {
		err := r.RenameNoReplace(from, to)
		if !errors.Is(err, errors.ErrUnsupported) {
			return err
		}
	}
	if _, err := fsys.Lstat(to); err == nil {
		return &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return fsys.Rename(from, to)
}

// sameItem checks that the trash keeps at it.ID the item it was listed as:
// its record still says it came from the same place at the same time. A
// name in the trash freed by a restore is taken by the next item of that
// name deleted.
func (t xdgTrash) sameItem(it TrashItem) error {
	_, ps := t.on()
	if !ps.Same(ps.Dir(it.ID), ps.Join(t.dir, "files")) {
		return fmt.Errorf("%s is not in the trash", it.ID)
	}
	from, deleted, err := t.read(t.recordOf(it.ID))
	if err != nil {
		return err
	}
	if !ps.Same(from, it.Original) || !deleted.Equal(it.Deleted) {
		return fmt.Errorf("the trash no longer holds the %s listed; read it again", it.Name)
	}
	return nil
}

// recordOf is the path of the record of the item at trashed.
func (t xdgTrash) recordOf(trashed string) string {
	_, ps := t.on()
	return ps.Join(t.dir, "info", ps.Base(trashed)+infoExt)
}

// read reads the .trashinfo record at path: where its item came from, and
// when it was deleted, zero where the record does not say.
func (t xdgTrash) read(path string) (from string, deleted time.Time, err error) {
	fsys, ps := t.on()
	b, err := readFile(fsys, path)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading the trash record: %w", err)
	}
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "DeletionDate="); ok {
			deleted, _ = time.ParseInLocation(trashDate, v, time.Local)
			continue
		}
		v, ok := strings.CutPrefix(line, "Path=")
		if !ok || from != "" {
			continue
		}
		p, err := url.PathUnescape(v)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("reading the trash record %s: %w: %w", path, errBadRecord, err)
		}
		if ps == SystemPaths {
			p = filepath.FromSlash(p)
		}
		if !t.absolute(p) {
			// A trash on another volume keeps paths from the volume's top,
			// and a path that climbs out of it is no record's.
			top := ps.Dir(t.dir)
			p = ps.Join(top, p)
			if !ps.inside(p, top) || ps.Same(p, top) {
				return "", time.Time{}, fmt.Errorf("the trash record %s says the item came from outside its volume: %w", path, errBadRecord)
			}
		}
		from = p
	}
	if from == "" {
		return "", time.Time{}, fmt.Errorf("the trash record %s says nothing of where the item came from: %w", path, errBadRecord)
	}
	return from, deleted, nil
}

// errBadRecord says a .trashinfo record cannot be read as one.
var errBadRecord = errors.New("the record is not one the trash specification describes")

// slashed is p written with '/', as a record keeps it.
func (t xdgTrash) slashed(p string) string {
	if _, ps := t.on(); ps == SystemPaths {
		return filepath.ToSlash(p)
	}
	return p
}

// absolute reports whether p starts at the top of the file system.
func (t xdgTrash) absolute(p string) bool {
	if _, ps := t.on(); ps == SystemPaths {
		return filepath.IsAbs(p)
	}
	return strings.HasPrefix(p, "/")
}

// Describe implements [Trasher].
func (t xdgTrash) Describe(trashed string) string {
	fsys, _ := t.on()
	return "in the trash, " + describe(fsys, trashed)
}

// TrashItems implements [TrashCan]: an item for each record whose item
// is in files. An item with no record, a record with no item, and a
// record that is not one are passed by, as the specification has it; any
// other failure to read one fails the whole, as a list with items left
// out would be taken for all the trash holds.
func (t xdgTrash) TrashItems(ctx context.Context) ([]TrashItem, error) {
	fsys, ps := t.on()
	records, err := fsys.ReadDir(ctx, ps.Join(t.dir, "info"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the trash: %w", err)
	}
	// The items are read in one go, as a server's are a round trip each.
	kept, err := fsys.ReadDir(ctx, ps.Join(t.dir, "files"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the trash: %w", err)
	}
	items := make(map[string]fs.DirEntry, len(kept))
	for _, k := range kept {
		items[k.Name()] = k
	}
	var out []TrashItem
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name, ok := strings.CutSuffix(r.Name(), infoExt)
		if !ok || r.IsDir() || name == "" || name == "." || name == ".." {
			continue
		}
		k, ok := items[name]
		if !ok {
			continue
		}
		at := ps.Join(t.dir, "files", name)
		info, err := k.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading the trash: %w", err)
		}
		from, deleted, err := t.read(ps.Join(t.dir, "info", r.Name()))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errBadRecord) {
			continue
		}
		if err != nil {
			return nil, err
		}
		it := TrashItem{ID: at, Name: ps.Base(from), Original: from, Deleted: deleted, Size: info.Size(), Dir: info.IsDir()}
		if it.Dir {
			it.Size = -1
		}
		out = append(out, it)
	}
	return out, nil
}

// RestoreItem implements [TrashCan].
func (t xdgTrash) RestoreItem(it TrashItem) error {
	if err := t.sameItem(it); err != nil {
		return err
	}
	return t.Restore(it.Original, it.ID, it.Deleted, it.Original)
}

// PurgeItem implements [TrashCan]: the item goes, and then its record, so
// an item that goes only in part is still listed.
func (t xdgTrash) PurgeItem(ctx context.Context, it TrashItem) error {
	fsys, _ := t.on()
	if err := t.sameItem(it); err != nil {
		return err
	}
	if err := removeAll(ctx, fsys, it.ID); err != nil {
		return fmt.Errorf("deleting %s: %w", it.Name, err)
	}
	if err := fsys.Remove(t.recordOf(it.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("deleted %s, but removing its trash record failed: %w", it.Name, err)
	}
	return nil
}

// EmptyTrash implements [TrashCan]: every item the trash holds as it
// starts goes, each with its record, and the sizes the trash noted of its
// folders. An item that comes to the trash meanwhile, under a name the
// trash did not hold, stays with its record. A record with no item stays
// too: it may be of an item on its way in, whose record is made first,
// and one left over costs only its name.
func (t xdgTrash) EmptyTrash(ctx context.Context) error {
	fsys, ps := t.on()
	list := func(sub string) ([]fs.DirEntry, error) {
		kids, err := fsys.ReadDir(ctx, ps.Join(t.dir, sub))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("emptying the trash: %w", err)
		}
		return kids, nil
	}
	// The records are listed before the items, as an item on its way in
	// has its record first: an item listed here with no record listed may
	// be one that came between the two.
	records, err := list("info")
	if err != nil {
		return err
	}
	kept, err := list("files")
	if err != nil {
		return err
	}
	// folded tells a record listed under other letters' case, as a file
	// system that ignores case keeps one another program wrote, from a
	// record made since.
	recorded, folded := make(map[string]bool, len(records)), make(map[string]bool, len(records))
	for _, r := range records {
		recorded[r.Name()], folded[strings.ToLower(r.Name())] = true, true
	}
	for _, k := range kept {
		name := k.Name()
		record := ps.Join(t.dir, "info", name+infoExt)
		has := recorded[name+infoExt]
		if !has {
			switch _, err := fsys.Lstat(record); {
			case err == nil && folded[strings.ToLower(name+infoExt)]:
				has = true
			case err == nil:
				// A record made since the listing: the item came with it,
				// and both stay.
				continue
			case !errors.Is(err, fs.ErrNotExist):
				return fmt.Errorf("emptying the trash: %w", err)
			}
		}
		if err := removeAll(ctx, fsys, ps.Join(t.dir, "files", name)); err != nil {
			return fmt.Errorf("emptying the trash: %w", err)
		}
		// An item with no record has none to remove: one there now is of
		// an item that took the name since.
		if !has {
			continue
		}
		if err := removeAll(ctx, fsys, record); err != nil {
			return fmt.Errorf("emptying the trash: %w", err)
		}
	}
	for _, r := range records {
		// What is in info and is no record goes, as nothing claims a name
		// with it. A name of dots is no item's.
		name, isRecord := strings.CutSuffix(r.Name(), infoExt)
		if isRecord && name != "" && name != "." && name != ".." {
			continue
		}
		if err := removeAll(ctx, fsys, ps.Join(t.dir, "info", r.Name())); err != nil {
			return fmt.Errorf("emptying the trash: %w", err)
		}
	}
	if err := fsys.Remove(ps.Join(t.dir, "directorysizes")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("emptying the trash: %w", err)
	}
	return nil
}

// numbered returns name with " (n)" put before its extension.
func numbered(name string, n int) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	return fmt.Sprintf("%s (%d)%s", stem, n, ext)
}
