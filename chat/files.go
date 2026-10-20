package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/marrasen/gunim/icon"
)

// FileIcon returns the icon for a file by its name: a picture, a text, or any other file.
func FileIcon(name string) *icon.Icon {
	switch ext := strings.ToLower(name[strings.LastIndexByte(name, '.')+1:]); ext {
	case "png", "jpg", "jpeg", "gif", "svg", "webp":
		return icon.FileImage
	case "md", "txt", "pdf":
		return icon.FileText
	}
	return icon.File
}

// SizeText writes a size in bytes the way people read it.
func SizeText(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1000*1000:
		return fmt.Sprintf("%.0f kB", float64(n)/1000)
	case n < 1000*1000*1000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%.1f GB", float64(n)/1e9)
}
