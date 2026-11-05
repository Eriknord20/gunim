//go:build !windows && !linux

package filemanager

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// volumeOf names the volume the folder at path is on, by its device.
func volumeOf(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("reading the volume of %s: the system does not say", path)
	}
	return strconv.FormatInt(int64(st.Dev), 10), nil //nolint:unconvert
}
