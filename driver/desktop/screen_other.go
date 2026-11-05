//go:build !linux && !js

package desktop

// watchScreen does nothing here: the system tells each window itself
// as the screen goes off or locks, where it tells at all.
func watchScreen(*Driver) (stop func()) { return func() {} }
