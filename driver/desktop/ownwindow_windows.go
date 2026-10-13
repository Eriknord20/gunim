package desktop

// ownWindow makes ordinary window w belong to p: Windows keeps an owned window in front of its owner and off the
// taskbar. It runs on the main thread.
func ownWindow(w, p *Window) error { return w.gw.SetOwner(p.gw) }
