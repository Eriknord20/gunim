package desktop

// ownWindow makes ordinary window w belong to p, as a transient window of p's, which the window manager keeps in
// front of p. It runs on the main thread.
func ownWindow(w, p *Window) error { return w.gw.SetOwner(p.gw) }
