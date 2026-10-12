//go:build unix

package filemanager

import (
	"os"
	"path/filepath"
	"testing"
)

// A copy that fails offers Retry, which copies what did not arrive once
// what stopped it is put right; the error is said in words, with the
// system's own under it.
func TestAFailedCopyCanBeRetried(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	h := newHarness(t, "a.txt", "ro/")
	ro := filepath.Join(h.dir, "ro")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	h.a.post(func() {
		h.a.startOp(job{kind: OpCopy, srcs: []string{filepath.Join(h.dir, "a.txt")}, dest: ro}, "Copying a.txt")
	})
	h.until("the copy fails", func() bool { return len(h.a.ops.dialogs) == 1 })
	e, ok := h.a.ops.dialogs[0].state.(ErrorBox)
	if !ok || !e.Retry || e.Detail == "" || e.Body == e.Detail {
		t.Fatalf("the copy's failure says %+v", h.a.ops.dialogs[0].state)
	}
	if err := os.Chmod(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	h.answer(DialogClosed{Retry: true})
	h.until("the retry copies it", func() bool {
		_, err := os.Stat(filepath.Join(ro, "a.txt"))
		return err == nil
	})
}
