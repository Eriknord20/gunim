package vst3

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"unsafe"
)

// Roles are the parts a plugin instance bound to an ARA document plays.
type Roles int32

// The roles.
const (
	// PlaybackRenderer renders its regions in place of its input, for
	// playing and exporting.
	PlaybackRenderer Roles = 1 << 0
	// EditorRenderer adds the sound of editing to its input, as a note
	// played as it is dragged.
	EditorRenderer Roles = 1 << 1
	// EditorView shows the plugin's editor.
	EditorView Roles = 1 << 2
)

// allRoles are the roles a host of this package knows of.
const allRoles = PlaybackRenderer | EditorRenderer | EditorView

// An Extension is what a plugin instance bound to an ARA document
// adds: its regions as a renderer, and its view's selection. Its
// methods are safe from any goroutine; each runs its work on the
// plugins' thread, and waits.
type Extension struct {
	p     *Plugin
	d     *Document
	roles Roles
	// pr, er and ev are the playback renderer's, the editor renderer's
	// and the editor view's interfaces, where the instance plays them.
	pr, er, ev iface
	// regions are the playback renderer's, and editorRegions and
	// editorSequences the editor renderer's.
	regions         []*PlaybackRegion
	editorRegions   []*PlaybackRegion
	editorSequences []*RegionSequence
}

// NewPlugin makes a plugin of the factory's class, set up as cfg says,
// bound to the document in roles before it first processes, as ARA
// asks; its ARA method returns what binding added. A playback renderer
// renders its regions where Process and ProcessBlock are told the
// song is, in place of its input.
func (d *Document) NewPlugin(cfg Config, roles Roles) (*Plugin, error) {
	m := d.f.module
	switch {
	case m != nil:
		return d.newPluginWith(cfg, roles, func(p *Plugin) error { return p.create(m) })
	case d.f.build != nil:
		return d.newPluginWith(cfg, roles, d.f.build)
	}
	return nil, fmt.Errorf("vst3: %s: its factory came from no module", d.f.Name)
}

// newPluginWith makes a plugin of the factory's class with build, as
// NewPlugin does.
func (d *Document) newPluginWith(cfg Config, roles Roles, build func(p *Plugin) error) (*Plugin, error) {
	if roles&allRoles == 0 {
		return nil, errors.New("vst3: a plugin bound to an ARA document plays a role at least")
	}
	return start(d.f.Class, cfg, func(p *Plugin) error {
		if d.closed {
			return ErrClosed
		}
		p.bind = func(comp uintptr) error {
			x, err := d.bind(comp, roles)
			if err != nil {
				return err
			}
			x.p, p.ara = p, x
			d.mu.Lock()
			d.plugins = append(d.plugins, p)
			d.mu.Unlock()
			return nil
		}
		return build(p)
	})
}

// The plug-in extension instance's fields: its size, ARA 1's
// extension, and each role's reference and interface.
var extensionFields = []int{fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr}

// bind binds the component comp to the document in roles. It runs on
// the plugins' thread.
func (d *Document) bind(comp uintptr, roles Roles) (*Extension, error) {
	if entry := query(comp, iidARAPlugInEntry); entry != 0 {
		f := call(entry, mARAGetFactory)
		release(entry)
		if f != d.f.ptr {
			return nil, fmt.Errorf("vst3: %s: the instance's ARA factory is another than the document's", d.f.Name)
		}
	}
	entry2 := query(comp, iidARAPlugInEntry2)
	if entry2 == 0 {
		return nil, fmt.Errorf("%w: %s binds to no ARA 2 document", ErrNoARA, d.f.Name)
	}
	p := call(entry2, mARABindWithRoles, d.dc.ref, uintptr(allRoles), uintptr(roles))
	release(entry2)
	if p == 0 {
		return nil, fmt.Errorf("vst3: %s would not bind to its document", d.f.Name)
	}
	r := readAt(p, extensionFields...)
	x := &Extension{d: d, roles: roles}
	if roles&PlaybackRenderer != 0 && r.has(4) {
		x.pr = iface{ref: r.ptr(3), table: r.ptr(4)}
	}
	if roles&EditorRenderer != 0 && r.has(6) {
		x.er = iface{ref: r.ptr(5), table: r.ptr(6)}
	}
	if roles&EditorView != 0 && r.has(8) {
		x.ev = iface{ref: r.ptr(7), table: r.ptr(8)}
	}
	return x, nil
}

// ARA returns what binding to an ARA document added to the plugin, or
// nil for a plugin bound to none.
func (p *Plugin) ARA() *Extension { return p.ara }

// Roles returns the roles the instance plays.
func (x *Extension) Roles() Roles { return x.roles }

// Document returns the document the instance is bound to.
func (x *Extension) Document() *Document { return x.d }

// errNoRole is the error of a call of a role the instance plays none of.
var errNoRole = errors.New("vst3: the ARA instance plays no such role")

// SetRegions makes regions the ones the playback renderer renders. As
// ARA asks, the plugin stops processing while they change, and starts
// again after, from silence: Process and ProcessBlock must not run
// meanwhile.
func (x *Extension) SetRegions(regions ...*PlaybackRegion) error {
	if x.pr.table == 0 {
		return errNoRole
	}
	var err error
	onUI(func() {
		p := x.p
		if x.d.closed || p.closed {
			err = ErrClosed
			return
		}
		was := p.active
		if was {
			p.deactivate()
		}
		for _, r := range slices.Clone(x.regions) {
			if !slices.Contains(regions, r) {
				x.pr.call(1, r.ref)
				x.regions = slices.DeleteFunc(x.regions, func(q *PlaybackRegion) bool { return q == r })
			}
		}
		for _, r := range regions {
			if r == nil || r.removed || slices.Contains(x.regions, r) {
				continue
			}
			x.pr.call(0, r.ref)
			x.regions = append(x.regions, r)
		}
		if was {
			err = p.activate()
		}
	})
	return err
}

// Regions returns the regions the playback renderer renders.
func (x *Extension) Regions() []*PlaybackRegion {
	var out []*PlaybackRegion
	onUI(func() { out = slices.Clone(x.regions) })
	return out
}

// forget takes region r out of the instance's, as the region goes in
// edit e, which stops the renderer until it ends. It runs on the
// plugins' thread.
func (x *Extension) forget(r *PlaybackRegion, e *Edit) {
	if slices.Contains(x.regions, r) {
		e.pause(x.p)
		x.pr.call(1, r.ref)
		x.regions = slices.DeleteFunc(x.regions, func(q *PlaybackRegion) bool { return q == r })
	}
	if slices.Contains(x.editorRegions, r) {
		x.er.call(1, r.ref)
		x.editorRegions = slices.DeleteFunc(x.editorRegions, func(q *PlaybackRegion) bool { return q == r })
	}
}

// SetEditorRegions makes regions and sequences the ones the editor
// renderer plays the sound of editing for. It may run while the
// plugin processes.
func (x *Extension) SetEditorRegions(regions []*PlaybackRegion, sequences []*RegionSequence) error {
	if x.er.table == 0 {
		return errNoRole
	}
	onUI(func() {
		for _, r := range slices.Clone(x.editorRegions) {
			if !slices.Contains(regions, r) {
				x.er.call(1, r.ref)
			}
		}
		for _, r := range regions {
			if r != nil && !r.removed && !slices.Contains(x.editorRegions, r) {
				x.er.call(0, r.ref)
			}
		}
		for _, s := range slices.Clone(x.editorSequences) {
			if !slices.Contains(sequences, s) {
				x.er.call(3, s.ref)
			}
		}
		for _, s := range sequences {
			if s != nil && !s.removed && !slices.Contains(x.editorSequences, s) {
				x.er.call(2, s.ref)
			}
		}
		x.editorRegions = slices.DeleteFunc(slices.Clone(regions), func(r *PlaybackRegion) bool { return r == nil || r.removed })
		x.editorSequences = slices.DeleteFunc(slices.Clone(sequences), func(s *RegionSequence) bool { return s == nil || s.removed })
	})
	return nil
}

// Selection is what the host has picked, for an editor view to show:
// regions, sequences, and a span of time, each optional.
type Selection struct {
	Regions   []*PlaybackRegion
	Sequences []*RegionSequence
	Range     *TimeRange
}

// Select tells the editor view what the host has picked.
func (x *Extension) Select(s Selection) error {
	if x.ev.table == 0 {
		return errNoRole
	}
	onUI(func() {
		sel := newStruct(fPtr, fPtr, fPtr, fPtr, fPtr, fPtr)
		var regions []uintptr
		for _, r := range s.Regions {
			if r != nil && !r.removed {
				regions = append(regions, r.ref)
			}
		}
		if len(regions) > 0 {
			sel.put(1, uint64(len(regions)))
			sel.putPtr(2, uintptr(unsafe.Pointer(&regions[0])), regions)
		}
		var seqs []uintptr
		for _, q := range s.Sequences {
			if q != nil && !q.removed {
				seqs = append(seqs, q.ref)
			}
		}
		if len(seqs) > 0 {
			sel.put(3, uint64(len(seqs)))
			sel.putPtr(4, uintptr(unsafe.Pointer(&seqs[0])), seqs)
		}
		if s.Range != nil {
			rng := newPlain(fF64, fF64)
			rng.putF64(0, s.Range.Start)
			rng.putF64(1, s.Range.Duration)
			sel.putPtr(5, rng.addr(), rng)
		}
		x.ev.call(0, sel.addr())
		runtime.KeepAlive(sel)
	})
	return nil
}

// Hide tells the editor view to hide sequences, as a host does those
// of tracks folded away; none shows every one.
func (x *Extension) Hide(sequences ...*RegionSequence) error {
	if x.ev.table == 0 {
		return errNoRole
	}
	onUI(func() {
		var refs []uintptr
		for _, q := range sequences {
			if q != nil && !q.removed {
				refs = append(refs, q.ref)
			}
		}
		// Pinned, so on the heap: on the stack, a callback of the
		// plugin's could move it before the plugin reads it.
		var at uintptr
		if len(refs) > 0 {
			var pin runtime.Pinner
			pin.Pin(&refs[0])
			defer pin.Unpin()
			at = uintptr(unsafe.Pointer(&refs[0]))
		}
		x.ev.call(1, uintptr(len(refs)), at)
	})
	return nil
}

// unbind lets the document forget the plugin, as it closes.
func (x *Extension) unbind() {
	d := x.d
	d.mu.Lock()
	d.plugins = slices.DeleteFunc(d.plugins, func(q *Plugin) bool { return q == x.p })
	d.mu.Unlock()
}
