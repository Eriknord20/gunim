package vst3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/marrasen/gunim/audio/plug"
)

// Presets in VST3's own files, .vstpreset, as every VST3 host reads and
// writes them: a header naming the class, the component's and the
// controller's states, and a list of the chunks. A class's presets lie
// in folders named by its vendor and its name, in the places VST3 sets
// for each system: the user's, and those every user shares.

// The banks presets are filed under: the user's own, and those every
// user shares, as a plugin's installer puts its factory presets.
const (
	UserBank    = "User"
	FactoryBank = "Factory"
)

// PresetExt is a preset file's extension.
const PresetExt = ".vstpreset"

// presetHeader is how long a preset file's header is: "VST3", the
// version, the class's ID in 32 characters, and where the list of
// chunks starts.
const presetHeader = 4 + 4 + 32 + 8

// FUID is the class's ID as VST3 writes it in text, as a preset file
// names its class: the four numbers of the ID, in hexadecimal, on every
// system.
func (c Class) FUID() string {
	u := c.ID
	if comCompatible {
		// COM lays the first three parts out little end first.
		var b [16]byte
		binary.BigEndian.PutUint32(b[0:], binary.LittleEndian.Uint32(u[0:]))
		binary.BigEndian.PutUint16(b[4:], binary.LittleEndian.Uint16(u[4:]))
		binary.BigEndian.PutUint16(b[6:], binary.LittleEndian.Uint16(u[6:]))
		copy(b[8:], u[8:])
		u = b
	}
	return fmt.Sprintf("%X", u[:])
}

// ReadPreset reads the preset file at path: the class it is of, by its
// FUID, and its state, as [Plugin.State] gives one.
func ReadPreset(path string) (class string, state []byte, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	class, chunks, err := presetChunks(b)
	if err != nil {
		return "", nil, fmt.Errorf("vst3: preset %s: %w", path, err)
	}
	comp, ok := chunks["Comp"]
	if !ok {
		return "", nil, fmt.Errorf("vst3: preset %s holds no state", path)
	}
	out := []byte(stateMagic)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(comp)))
	out = append(out, comp...)
	ctrl := chunks["Cont"]
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctrl)))
	return class, append(out, ctrl...), nil
}

// presetClass reads only the class a preset file is of.
func presetClass(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var h [presetHeader]byte
	if _, err := io.ReadFull(f, h[:]); err != nil {
		return "", err
	}
	if string(h[:4]) != "VST3" {
		return "", errors.New("no preset")
	}
	return string(h[8:40]), nil
}

// presetChunks reads a preset file's class and chunks.
func presetChunks(b []byte) (class string, chunks map[string][]byte, err error) {
	bad := errors.New("no preset of VST3's")
	if len(b) < presetHeader || string(b[:4]) != "VST3" {
		return "", nil, bad
	}
	class = string(b[8:40])
	at := binary.LittleEndian.Uint64(b[40:])
	if at > uint64(len(b))-8 || string(b[at:at+4]) != "List" {
		return "", nil, bad
	}
	n := int(binary.LittleEndian.Uint32(b[at+4:]))
	list := b[at+8:]
	if n < 0 || len(list) < n*20 {
		return "", nil, bad
	}
	chunks = map[string][]byte{}
	for i := range n {
		e := list[i*20:]
		off, size := binary.LittleEndian.Uint64(e[4:]), binary.LittleEndian.Uint64(e[12:])
		if off > uint64(len(b)) || size > uint64(len(b))-off {
			return "", nil, bad
		}
		chunks[string(e[:4])] = b[off : off+size]
	}
	return class, chunks, nil
}

// WritePreset writes state, as [Plugin.State] gives it, to a preset file
// of class c at path, making its folder where it is missing.
func WritePreset(path string, c Class, state []byte) error {
	comp, ctrl, err := splitState(c.Name, state)
	if err != nil {
		return err
	}
	out := make([]byte, presetHeader, presetHeader+len(comp)+len(ctrl)+64)
	copy(out, "VST3")
	binary.LittleEndian.PutUint32(out[4:], 1)
	copy(out[8:40], c.FUID())
	type entry struct {
		id        string
		off, size int
	}
	var list []entry
	for _, ch := range []struct {
		id string
		b  []byte
	}{{"Comp", comp}, {"Cont", ctrl}} {
		if ch.id == "Cont" && len(ch.b) == 0 {
			continue
		}
		list = append(list, entry{ch.id, len(out), len(ch.b)})
		out = append(out, ch.b...)
	}
	binary.LittleEndian.PutUint64(out[40:], uint64(len(out)))
	out = append(out, "List"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(list)))
	for _, e := range list {
		out = append(out, e.id...)
		out = binary.LittleEndian.AppendUint64(out, uint64(e.off))
		out = binary.LittleEndian.AppendUint64(out, uint64(e.size))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PresetDirs returns the folders the presets of class c lie in, by
// VST3's places for the system: the user's first, where a preset kept
// goes, then those every user shares.
func PresetDirs(c Class) (user string, shared []string) {
	sub := filepath.Join(presetName(c.Vendor), presetName(c.Name))
	home, _ := os.UserHomeDir()
	root := string(filepath.Separator)
	switch runtime.GOOS {
	case "windows":
		if home != "" {
			user = filepath.Join(home, "Documents", "VST3 Presets", sub)
		}
		if all := os.Getenv("ALLUSERSPROFILE"); all != "" {
			shared = append(shared, filepath.Join(all, "VST3 Presets", sub))
		}
	case "darwin":
		if home != "" {
			user = filepath.Join(home, "Library", "Audio", "Presets", sub)
		}
		shared = append(shared, filepath.Join(root, "Library", "Audio", "Presets", sub))
	default:
		if home != "" {
			user = filepath.Join(home, ".vst3", "presets", sub)
		}
		shared = append(shared, filepath.Join(root, "usr", "share", "vst3", "presets", sub),
			filepath.Join(root, "usr", "local", "share", "vst3", "presets", sub))
	}
	return user, shared
}

// presetName is name as a folder's or a file's name: without the
// characters a system keeps out of names.
func presetName(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	return name
}

// PresetPath is where a preset of class c named name is kept, in the
// user's folder.
func PresetPath(c Class, name string) string {
	user, _ := PresetDirs(c)
	return filepath.Join(user, presetName(name)+PresetExt)
}

// ClassPresets returns the presets of class c in its folders: the
// user's, then those every user shares, each filed under its folder's
// path below the class's, or under the class's name where it lies in
// the class's folder itself. A file of another class is passed over.
func ClassPresets(c Class) []plug.Preset {
	user, shared := PresetDirs(c)
	fuid := c.FUID()
	var out []plug.Preset
	// add adds the presets in dirs, as one bank: a group in two of them
	// lists once.
	add := func(bank string, dirs []string) {
		var found []plug.Preset
		for _, dir := range dirs {
			if dir != "" {
				found = append(found, presetsIn(bank, dir, c, fuid)...)
			}
		}
		sortPresets(found, c.Name)
		out = append(out, found...)
	}
	add(UserBank, []string{user})
	add(FactoryBank, shared)
	return out
}

// presetsIn returns the presets of class c, whose FUID is fuid, in dir
// and the folders in it, filed under bank.
func presetsIn(bank, dir string, c Class, fuid string) []plug.Preset {
	var found []plug.Preset
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A folder that cannot be read is passed over.
			return fs.SkipDir
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), PresetExt) {
			return nil
		}
		cl, cerr := presetClass(path)
		if theirs := cerr != nil || !strings.EqualFold(cl, fuid); theirs {
			return nil
		}
		group := c.Name
		if rel, err := filepath.Rel(dir, filepath.Dir(path)); err == nil && rel != "." {
			group = filepath.ToSlash(rel)
		}
		found = append(found, plug.Preset{Bank: bank, Group: group, Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())),
			State: func() ([]byte, error) {
				_, st, err := ReadPreset(path)
				return st, err
			}})
		return nil
	})
	return found
}

// sortPresets sorts presets by group, the class's own folder, named
// home, first, then by name.
func sortPresets(ps []plug.Preset, home string) {
	slices.SortStableFunc(ps, func(a, b plug.Preset) int {
		if a.Group != b.Group {
			switch {
			case a.Group == home:
				return -1
			case b.Group == home:
				return 1
			}
			return strings.Compare(strings.ToLower(a.Group), strings.ToLower(b.Group))
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}
