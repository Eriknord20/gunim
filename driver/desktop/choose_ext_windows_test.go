package desktop

import (
	"testing"

	"github.com/marrasen/gunim/driver"
)

func TestASaveDialogsDefaultExtension(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		filters []driver.FileFilter
		want    string
	}{
		{nil, ""},
		{[]driver.FileFilter{{Name: "WAV", Patterns: []string{"*.wav"}}}, "wav"},
		{[]driver.FileFilter{{Name: "Songs", Patterns: []string{"*.hej", "*.json"}}, {Name: "All", Patterns: []string{"*"}}}, "hej"},
		{[]driver.FileFilter{{Name: "All", Patterns: []string{"*"}}}, ""},
		{[]driver.FileFilter{{Name: "None"}}, ""},
		{[]driver.FileFilter{{Name: "Odd", Patterns: []string{"*.*"}}}, ""},
	} {
		if got := defaultExt(c.filters); got != c.want {
			t.Errorf("defaultExt(%v) = %q, want %q", c.filters, got, c.want)
		}
	}
}
