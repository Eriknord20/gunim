//go:build !windows

package desktop

// HDRStatus says what the HDR spike found. The spike is Windows' alone,
// so it is empty here.
func HDRStatus() string { return "" }
