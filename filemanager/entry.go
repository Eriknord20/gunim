package filemanager

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// Kind is what a folder entry is.
type Kind uint8

// The kinds of entry.
const (
	KindFile Kind = iota
	KindFolder
	KindLink
)

// entry is one item of a folder, as the app half keeps it.
type entry struct {
	// Name is the item's name, or, for an item of a folder under the one
	// showing, its path under that folder, whose name starts at at.
	Name string
	at   int
	Kind Kind
	// Dir is set for a folder, and for a link to one.
	Dir    bool
	Size   int64
	Mod    time.Time
	Hidden bool
	// Online says the file keeps its contents online only, with a cloud provider, so reading them downloads them,
	// and Cloud how a cloud provider keeps the item.
	Online bool
	Cloud  CloudState
	// Broken is set for a link whose target is missing, and for an item
	// that cannot be read.
	Broken bool
	Type   string
	// Err says why the item, or the target of the link it is, cannot be
	// read.
	Err string
	// iconKey names the icon Windows shows for it, while the window shows
	// those.
	iconKey string
	// lower is the name in lower case, for sorting and filtering.
	lower string
}

// base is the item's own name, with no folder before it.
func (e entry) base() string { return e.Name[e.at:] }

// listDir reads the entries of dir on fsys. An entry it cannot read
// lists with its name and why, and an entry deleted while it lists is
// left out, so the errors it returns are about dir itself.
func listDir(ctx context.Context, fsys FS, dir string) ([]entry, error) {
	ds, err := fsys.ReadDir(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]entry, 0, len(ds))
	for _, d := range ds {
		if e, ok := readEntry(fsys, dir, d); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// readEntry turns a directory entry of dir on fsys into an entry. ok is
// false for an entry deleted since dir was read.
func readEntry(fsys FS, dir string, d fs.DirEntry) (e entry, ok bool) {
	info, err := d.Info()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return entry{}, false
	case err != nil:
		name := d.Name()
		return entry{Name: name, Kind: KindFile, Hidden: strings.HasPrefix(name, "."), Broken: true,
			Type: "Cannot be read", Err: err.Error(), lower: strings.ToLower(name)}, true
	}
	return makeEntry(fsys, dir, info), true
}

// makeEntry turns what Lstat says about an item of dir on fsys into an
// entry.
func makeEntry(fsys FS, dir string, info fs.FileInfo) entry {
	name := info.Name()
	e := entry{Name: name, Size: info.Size(), Mod: info.ModTime(), lower: strings.ToLower(name)}
	e.Hidden = strings.HasPrefix(name, ".")
	if h, ok := fsys.(HiddenReporter); ok && !e.Hidden {
		e.Hidden = h.Hidden(info)
	}
	switch {
	case info.IsDir():
		e.Kind, e.Dir, e.Size = KindFolder, true, 0
		e.Type = "Folder"
		if c, ok := fsys.(CloudReporter); ok {
			e.Cloud = c.Cloud(dir, info)
		}
	case isLink(info):
		e.Kind = KindLink
		target, err := fsys.Stat(fsys.Paths().Join(dir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			e.Broken = true
			e.Type = "Broken link"
		case err != nil:
			// A link to itself, or into a folder that cannot be read.
			e.Broken = true
			e.Type = "Link that cannot be followed"
			e.Err = err.Error()
		case target.IsDir():
			e.Dir, e.Size = true, 0
			e.Type = "Link to folder"
		default:
			e.Size = target.Size()
			e.Type = "Link to " + typeLabel(name)
		}
	default:
		e.Type = typeLabel(name)
		switch r := fsys.(type) {
		case CloudReporter:
			e.Cloud = r.Cloud(dir, info)
			e.Online = e.Cloud == CloudOnline
		case OnlineReporter:
			e.Online = r.OnlineOnly(info)
			if e.Online {
				e.Cloud = CloudOnline
			}
		}
	}
	return e
}

// isLink reports whether info is a symbolic link, or a junction on Windows.
func isLink(info fs.FileInfo) bool {
	return info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

// statEntry reads one entry of fsys by its path.
func statEntry(fsys FS, path string) (entry, error) {
	info, err := fsys.Lstat(path)
	if err != nil {
		return entry{}, err
	}
	return makeEntry(fsys, fsys.Paths().Dir(path), info), nil
}

// typeLabel names the type of a file from its extension.
func typeLabel(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" || ext == strings.ToLower(strings.TrimPrefix(name, ".")) {
		return "File"
	}
	if t, ok := typeNames[ext]; ok {
		return t
	}
	return strings.ToUpper(ext) + " file"
}

// typeNames are the labels of the extensions that have a better name than
// their letters.
var typeNames = map[string]string{
	"txt": "Text", "md": "Markdown", "log": "Log", "csv": "CSV table", "tsv": "TSV table",
	"json": "JSON", "xml": "XML", "yaml": "YAML", "yml": "YAML", "toml": "TOML", "ini": "Settings",
	"go": "Go source", "c": "C source", "h": "C header", "cpp": "C++ source", "rs": "Rust source",
	"py": "Python script", "js": "JavaScript", "ts": "TypeScript", "html": "Web page", "htm": "Web page",
	"css": "Stylesheet", "sh": "Shell script", "bat": "Batch file", "ps1": "PowerShell script",
	"png": "PNG image", "jpg": "JPEG image", "jpeg": "JPEG image", "gif": "GIF image", "bmp": "Bitmap image",
	"webp": "WebP image", "tif": "TIFF image", "tiff": "TIFF image", "svg": "SVG image", "ico": "Icon",
	"pdf": "PDF document", "doc": "Word document", "docx": "Word document", "xls": "Excel sheet",
	"xlsx": "Excel sheet", "ppt": "Presentation", "pptx": "Presentation", "odt": "Document",
	"zip": "Zip archive", "7z": "7-Zip archive", "rar": "RAR archive", "tar": "Tar archive",
	"gz": "Gzip archive", "xz": "XZ archive", "bz2": "Bzip2 archive", "zst": "Zstandard archive",
	"exe": "Program", "msi": "Installer", "dll": "Library", "so": "Library", "lnk": "Shortcut",
	"mp3": "MP3 audio", "wav": "WAV audio", "flac": "FLAC audio", "ogg": "Ogg audio",
	"mp4": "MP4 video", "mkv": "Matroska video", "mov": "QuickTime video", "avi": "AVI video",
	"iso": "Disc image", "ttf": "Font", "otf": "Font",
}
