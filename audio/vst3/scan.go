package vst3

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// A Bundle is a plugin found on disk, not yet loaded.
type Bundle struct {
	// Path is the .vst3 bundle or file, and Name its name.
	Path, Name string
	// Effects and Instruments are what it makes, as its bundle lists
	// them, read without loading it. Both are nil where it lists
	// nothing, for Open to say; Listed says it lists what it makes.
	Effects, Instruments []Class
	Listed               bool
}

// Dirs returns where this platform keeps VST3 plugins.
func Dirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		var dirs []string
		for _, env := range []string{"CommonProgramFiles", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
				dirs = append(dirs, filepath.Join(d, map[string]string{"CommonProgramFiles": "VST3",
					"LOCALAPPDATA": filepath.Join("Programs", "Common", "VST3")}[env]))
			}
		}
		return dirs
	case "darwin":
		return []string{filepath.Join(home, "Library", "Audio", "Plug-Ins", "VST3"), "/Library/Audio/Plug-Ins/VST3"}
	default:
		return []string{filepath.Join(home, ".vst3"), "/usr/lib/vst3", "/usr/local/lib/vst3"}
	}
}

// Scan finds the plugins within dirs, as Dirs gives them.
func Scan(dirs ...string) []Bundle {
	var out []Bundle
	seen := map[string]bool{}
	for _, d := range dirs {
		_ = filepath.WalkDir(d, func(path string, de os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a folder unread is passed over
			}
			if !strings.EqualFold(filepath.Ext(path), ".vst3") {
				return nil
			}
			if seen[path] {
				return filepath.SkipDir
			}
			seen[path] = true
			b := Bundle{Path: path, Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
			classes, listed := moduleInfo(path)
			b.Listed = listed
			for _, c := range classes {
				if c.Instrument() {
					b.Instruments = append(b.Instruments, c)
				} else {
					b.Effects = append(b.Effects, c)
				}
			}
			out = append(out, b)
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		})
	}
	return out
}

// trailingCommas are what JSON5 allows and JSON does not.
var trailingCommas = regexp.MustCompile(`,(\s*[}\]])`)

// moduleInfo reads the audio components, effects and instruments, a
// bundle lists in its moduleinfo.json, and whether it has one to read.
func moduleInfo(path string) ([]Class, bool) {
	b, err := os.ReadFile(filepath.Join(path, "Contents", "Resources", "moduleinfo.json"))
	if err != nil {
		return nil, false
	}
	var info struct {
		Factory struct{ Vendor string } `json:"Factory Info"`
		Classes []struct {
			CID, Category, Name, Vendor, Version string
			SubCategories                        []string `json:"Sub Categories"`
		}
	}
	if json.Unmarshal(trailingCommas.ReplaceAll(b, []byte("$1")), &info) != nil {
		return nil, false
	}
	var out []Class
	for _, c := range info.Classes {
		raw, err := hex.DecodeString(c.CID)
		if err != nil || len(raw) != 16 {
			continue
		}
		be := binary.BigEndian
		cl := Class{ID: iid(be.Uint32(raw), be.Uint32(raw[4:]), be.Uint32(raw[8:]), be.Uint32(raw[12:])), Name: c.Name,
			Category: c.Category, SubCategories: strings.Join(c.SubCategories, "|"), Vendor: c.Vendor, Version: c.Version}
		if cl.Vendor == "" {
			cl.Vendor = info.Factory.Vendor
		}
		if cl.Audio() {
			out = append(out, cl)
		}
	}
	return out, true
}
