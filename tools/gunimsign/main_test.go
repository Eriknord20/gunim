package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A key gunimsign makes signs a SHA256SUMS so its public key checks it,
// as install checks an update.
func TestSignWithANewKey(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "release.key")
	public, err := makeKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permissions to check; there it is enough that
	// the file is there.
	if fi, serr := os.Stat(keyFile); serr != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Fatalf("the private key's file: %v, %v", fi, serr)
	}
	if _, again := makeKey(keyFile); again == nil {
		t.Error("-keygen wrote over a key there already")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	key, err := readKey(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(dir, "SHA256SUMS")
	body := []byte("0123  studio_1.0.0_linux_amd64\n")
	if err = os.WriteFile(sums, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = signFile(key, sums); err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(sums + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := base64.StdEncoding.DecodeString(public)
	got, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if !ed25519.Verify(pub, body, got) {
		t.Error("the public key -keygen printed does not check the signature")
	}
	if _, err := readKey("not a key"); err == nil {
		t.Error("read a key from nonsense")
	}
}
