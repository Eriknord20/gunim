package filemanager

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
)

// readDirSorted reads the items of the folder dir on fsys, sorted by
// name, as os.ReadDir returns them.
func readDirSorted(ctx context.Context, fsys FS, dir string) ([]fs.DirEntry, error) {
	es, err := fsys.ReadDir(ctx, dir)
	slices.SortFunc(es, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return es, err
}

// walkTree walks the tree under root on fsys as filepath.WalkDir does: root
// first, then each folder's items by name, calling fn for each. It does
// not follow links.
func walkTree(ctx context.Context, fsys FS, root string, fn fs.WalkDirFunc) error {
	info, err := fsys.Lstat(root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walkDir(ctx, fsys, root, fs.FileInfoToDirEntry(info), fn)
	}
	if errors.Is(err, fs.SkipDir) || errors.Is(err, fs.SkipAll) {
		return nil
	}
	return err
}

func walkDir(ctx context.Context, fsys FS, path string, d fs.DirEntry, fn fs.WalkDirFunc) error {
	if err := fn(path, d, nil); err != nil || !d.IsDir() {
		if errors.Is(err, fs.SkipDir) && d.IsDir() {
			err = nil
		}
		return err
	}
	es, err := readDirSorted(ctx, fsys, path)
	if err != nil {
		if err = fn(path, d, err); err != nil {
			if errors.Is(err, fs.SkipDir) {
				err = nil
			}
			return err
		}
	}
	ps := fsys.Paths()
	for _, e := range es {
		if err := walkDir(ctx, fsys, ps.Join(path, e.Name()), e, fn); err != nil {
			if errors.Is(err, fs.SkipDir) {
				break
			}
			return err
		}
	}
	return nil
}

// mkdirAll makes the folder at path on fsys, and the folders it is in
// that are missing, as os.MkdirAll does.
func mkdirAll(fsys FS, path string, perm fs.FileMode) error {
	if info, err := fsys.Stat(path); err == nil {
		if info.IsDir() {
			return nil
		}
		return &fs.PathError{Op: "mkdir", Path: path, Err: fs.ErrExist}
	}
	ps := fsys.Paths()
	if parent := ps.Dir(path); parent != path {
		if err := mkdirAll(fsys, parent, perm); err != nil {
			return err
		}
	}
	if err := fsys.Mkdir(path, perm); err != nil {
		// It may have been made meanwhile.
		if info, serr := fsys.Lstat(path); serr == nil && info.IsDir() {
			return nil
		}
		return err
	}
	return nil
}

// removeAll removes path on fsys and all it holds, as os.RemoveAll does.
// A path with nothing there is removed already.
func removeAll(ctx context.Context, fsys FS, path string) error {
	info, err := fsys.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if realDir(info) {
		kids, err := fsys.ReadDir(ctx, path)
		if err != nil {
			return err
		}
		ps := fsys.Paths()
		for _, k := range kids {
			if err := removeAll(ctx, fsys, ps.Join(path, k.Name())); err != nil {
				return err
			}
		}
	}
	if err := fsys.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// createTemp makes a new file in dir on fsys, named from pattern with
// its last * turned into a random number, as os.CreateTemp does, and
// returns it with its path.
func createTemp(fsys FS, dir, pattern string) (io.WriteCloser, string, error) {
	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}
	ps := fsys.Paths()
	for try := 0; ; try++ {
		name := ps.Join(dir, prefix+strconv.FormatUint(uint64(rand.Uint32()), 10)+suffix)
		f, err := fsys.Create(name)
		if errors.Is(err, fs.ErrExist) && try < 10000 {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return f, name, nil
	}
}

// sameFile reports whether a and b describe the same item on fsys, and
// false where fsys cannot tell.
func sameFile(fsys FS, a, b fs.FileInfo) bool {
	if s, ok := optional[SameFiler](fsys); ok {
		return s.SameFile(a, b)
	}
	return false
}

// realPath returns path on fsys with the links along it followed, or
// path as it is where they cannot be.
func realPath(fsys FS, path string) string {
	if l, ok := optional[Linker](fsys); ok {
		if p, err := l.EvalSymlinks(path); err == nil {
			return p
		}
	}
	return path
}

// readFile reads the whole of the file at path on fsys.
func readFile(fsys FS, path string) (b []byte, err error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			b, err = nil, cerr
		}
	}()
	return io.ReadAll(f)
}
