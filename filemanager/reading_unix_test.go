//go:build unix

package filemanager

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The text viewer reads no pipe, which could make the read wait for
// ever.
func TestTheTextViewerReadsNoPipe(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skip("no pipes here:", err)
	}
	if _, _, err := readStart(LocalFS(), pipe, 40); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("a pipe was read: %v", err)
	}
}
