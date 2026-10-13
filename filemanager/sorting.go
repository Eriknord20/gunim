package filemanager

import (
	"cmp"
	"slices"
	"strings"
)

// SortBy is the column a folder is sorted by.
type SortBy uint8

// The columns a folder sorts by.
const (
	SortName SortBy = iota
	SortSize
	SortModified
	SortType
	// SortFolder sorts by the folder an item is in, for the items a
	// filter found in the folders under the one showing.
	SortFolder
)

// sortEntries sorts es by column, folders first, and by name within ties.
func sortEntries(es []entry, by SortBy, desc bool) {
	// A folder holds few types: each is ranked once, by its name in lower case, and the sort compares the ranks.
	var types map[string]int
	if by == SortType {
		types = typeRanks(es)
	}
	slices.SortStableFunc(es, func(a, b entry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		c := 0
		switch by {
		case SortSize:
			c = cmp.Compare(a.Size, b.Size)
		case SortModified:
			c = a.Mod.Compare(b.Mod)
		case SortType:
			c = cmp.Compare(types[a.Type], types[b.Type])
		case SortFolder:
			c = naturalCompare(strings.ToLower(a.Name[:a.at]), strings.ToLower(b.Name[:b.at]))
		case SortName:
		}
		if c == 0 {
			c = naturalCompare(a.lower, b.lower)
		}
		if c == 0 {
			c = strings.Compare(a.Name, b.Name)
		}
		if desc {
			return -c
		}
		return c
	})
}

// typeRanks ranks the types of es by their names in lower case, types
// alike but for case ranked the same.
func typeRanks(es []entry) map[string]int {
	lower := map[string]string{}
	for _, e := range es {
		if _, ok := lower[e.Type]; !ok {
			lower[e.Type] = strings.ToLower(e.Type)
		}
	}
	names := make([]string, 0, len(lower))
	for _, l := range lower {
		names = append(names, l)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	ranks := make(map[string]int, len(lower))
	for t, l := range lower {
		ranks[t], _ = slices.BinarySearch(names, l)
	}
	return ranks
}

// naturalCompare compares a and b with runs of digits taken as numbers, so
// "file2" comes before "file10".
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			na, ra := digits(a)
			nb, rb := digits(b)
			// Leading zeros do not make a number larger.
			ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if c := cmp.Compare(len(ta), len(tb)); c != 0 {
				return c
			}
			if c := strings.Compare(ta, tb); c != 0 {
				return c
			}
			if c := cmp.Compare(len(na), len(nb)); c != 0 {
				return c
			}
			a, b = ra, rb
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// digits splits the leading run of digits off s.
func digits(s string) (run, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

// filterEntries returns the entries whose names hold text, ignoring case,
// leaving out hidden ones unless showHidden is set.
func filterEntries(es []entry, text string, showHidden bool) []entry {
	text = strings.ToLower(text)
	if text == "" && showHidden {
		return es
	}
	out := make([]entry, 0, len(es))
	for _, e := range es {
		if e.Hidden && !showHidden {
			continue
		}
		if text != "" && !strings.Contains(e.lower, text) {
			continue
		}
		out = append(out, e)
	}
	return out
}
