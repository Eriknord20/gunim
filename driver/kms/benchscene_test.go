//go:build (kmspi || kmsvkms) && linux && (arm64 || amd64)

package kms_test

import (
	"testing"

	"github.com/marrasen/gunim/driver/kms/kmsbench"
	"github.com/marrasen/gunim/internal/kmsdemo"
)

// benchScene is one of the bench's own scenes, "demo" or "widgets".
func benchScene(t *testing.T, screen string) kmsbench.Scene {
	t.Helper()
	s, err := kmsdemo.BenchScene(screen, false, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	return s
}
