//go:build linux && (arm64 || amd64)

package kms

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// screen is the display the driver took: the card, one connector, the CRTC
// that drives it, and that CRTC's primary plane, with the properties an
// atomic commit sets on each.
type screen struct {
	card *card
	// dev takes the screen's commits: the card, or a test's stand-in.
	dev  dev
	conn connector
	mode modeInfo
	crtc uint32
	// crtcIndex is the CRTC's place in the card's list, for the planes'
	// possible CRTCs.
	crtcIndex int
	plane     uint32

	connProps, crtcProps, planeProps map[string]prop
	// rotations is what the plane's rotation property offers, by name, or
	// nil where it has none.
	rotations map[string]uint64
	modeBlob  uint32

	req atomicReq
}

// errNoScreen is returned where no card has a connected screen.
var errNoScreen = errors.New("no connected screen found under /dev/dri")

// findScreen opens the cards under /dev/dri and takes the screen the
// options name, or else a connected DSI panel, or else the first connected
// HDMI screen. A card with no connectors, as v3d's, only renders, and is
// passed over.
func findScreen(o Options) (*screen, error) {
	paths, _ := filepath.Glob("/dev/dri/card*")
	slices.Sort(paths)
	var (
		best     *screen
		bestRank = -1
		errs     []error
	)
	for _, path := range paths {
		c, err := openCard(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s, rank, err := takeScreen(c, o.Connector)
		if err != nil || rank <= bestRank {
			if err != nil {
				errs = append(errs, err)
			}
			c.close()
			continue
		}
		if best != nil {
			best.card.close()
		}
		best, bestRank = s, rank
	}
	if best == nil {
		if o.Connector != "" {
			errs = append(errs, fmt.Errorf("no connected connector %q", o.Connector))
		}
		return nil, errors.Join(append([]error{errNoScreen}, errs...)...)
	}
	return best, nil
}

// takeScreen picks the best connected connector on c, and ranks it: a
// named one first, then DSI, then HDMI, then the rest. A card with no
// connector to take ranks -1, with no error: one that only renders, as
// v3d's, refuses to list its resources.
func takeScreen(c *card, want string) (*screen, int, error) {
	crtcs, conns, err := c.resources()
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) || err == nil && len(conns) == 0 {
		return nil, -1, nil
	}
	if err != nil {
		return nil, -1, fmt.Errorf("%s: resources: %w", c.path, err)
	}
	var (
		pick connector
		rank = -1
	)
	for _, id := range conns {
		k, err := c.connector(id)
		if err != nil || !k.connected || len(k.modes) == 0 {
			continue
		}
		r := 1
		switch {
		case want != "" && !strings.EqualFold(k.name(), want):
			continue
		case want != "":
			r = 4
		case k.typ == connectorDSI:
			r = 3
		case k.typ == connectorHDMIA:
			r = 2
		}
		if r > rank {
			pick, rank = k, r
		}
	}
	if rank < 0 {
		return nil, -1, nil
	}
	if err := c.setClientCap(clientCapUniversalPlane, 1); err != nil {
		return nil, -1, fmt.Errorf("%s: universal planes: %w", c.path, err)
	}
	if err := c.setClientCap(clientCapAtomic, 1); err != nil {
		return nil, -1, fmt.Errorf("%s: no atomic modesetting: %w", c.path, err)
	}
	s := &screen{card: c, dev: c, conn: pick}
	s.mode, _ = pick.preferred()
	if err := s.findCrtc(crtcs); err != nil {
		return nil, -1, err
	}
	if err := s.findPlane(); err != nil {
		return nil, -1, err
	}
	if s.connProps, err = c.props(pick.id, objConnector); err == nil {
		if s.crtcProps, err = c.props(s.crtc, objCrtc); err == nil {
			s.planeProps, err = c.props(s.plane, objPlane)
		}
	}
	if err != nil {
		return nil, -1, fmt.Errorf("%s: properties: %w", c.path, err)
	}
	for _, need := range []struct {
		props map[string]prop
		name  string
	}{
		{s.connProps, "CRTC_ID"}, {s.crtcProps, "MODE_ID"}, {s.crtcProps, "ACTIVE"},
		{s.planeProps, "FB_ID"}, {s.planeProps, "CRTC_ID"}, {s.planeProps, "SRC_X"}, {s.planeProps, "SRC_Y"},
		{s.planeProps, "SRC_W"}, {s.planeProps, "SRC_H"}, {s.planeProps, "CRTC_X"}, {s.planeProps, "CRTC_Y"},
		{s.planeProps, "CRTC_W"}, {s.planeProps, "CRTC_H"},
	} {
		if _, ok := need.props[need.name]; !ok {
			return nil, -1, fmt.Errorf("%s: no %s property", c.path, need.name)
		}
	}
	if r, ok := s.planeProps["rotation"]; ok {
		s.rotations, _ = c.propEnums(r.id)
	}
	return s, rank, nil
}

// findCrtc takes the CRTC the connector's encoder drives now, or else the
// first one any of its encoders can drive.
func (s *screen) findCrtc(crtcs []uint32) error {
	c := s.card
	if s.conn.encoder != 0 {
		if e, err := c.encoder(s.conn.encoder); err == nil && e.crtcID != 0 {
			if i := slices.Index(crtcs, e.crtcID); i >= 0 {
				s.crtc, s.crtcIndex = e.crtcID, i
				return nil
			}
		}
	}
	for _, id := range s.conn.encoders {
		e, err := c.encoder(id)
		if err != nil {
			continue
		}
		for i, crtc := range crtcs {
			if e.possibleCrtcs&(1<<i) != 0 {
				s.crtc, s.crtcIndex = crtc, i
				return nil
			}
		}
	}
	return fmt.Errorf("%s: no CRTC for %s", c.path, s.conn.name())
}

// findPlane takes the CRTC's primary plane.
func (s *screen) findPlane() error {
	c := s.card
	ids, err := c.planes()
	if err != nil {
		return fmt.Errorf("%s: planes: %w", c.path, err)
	}
	for _, id := range ids {
		p, err := c.plane(id)
		if err != nil || p.possibleCrtcs&(1<<s.crtcIndex) == 0 {
			continue
		}
		props, err := c.props(id, objPlane)
		if err != nil {
			continue
		}
		if t, ok := props["type"]; ok && t.value == planeTypePrimary {
			s.plane = id
			return nil
		}
	}
	return fmt.Errorf("%s: no primary plane for its CRTC", c.path)
}

// width and height are the mode's, in device pixels of the panel.
func (s *screen) width() int  { return int(s.mode.HDisplay) }
func (s *screen) height() int { return int(s.mode.VDisplay) }

// planeRotation returns the plane's rotation property value for a turn of
// deg clockwise, and whether the plane can do it.
func (s *screen) planeRotation(deg int) (uint64, bool) {
	if s.rotations == nil {
		return 0, false
	}
	// The kernel's rotation turns counter-clockwise; 180 is the same both
	// ways.
	name := map[int]string{0: "rotate-0", 180: "rotate-180"}[deg]
	v, ok := s.rotations[name]
	if !ok {
		return 0, false
	}
	return 1 << v, true
}

// setPlane adds the plane showing fb, whole, to the request.
func (s *screen) setPlane(fb uint32, rotation uint64, rotate bool) {
	r, p := &s.req, s.planeProps
	w, h := uint64(s.width()), uint64(s.height())
	r.add(s.plane, p["FB_ID"].id, uint64(fb))
	r.add(s.plane, p["CRTC_ID"].id, uint64(s.crtc))
	r.add(s.plane, p["SRC_X"].id, 0)
	r.add(s.plane, p["SRC_Y"].id, 0)
	r.add(s.plane, p["SRC_W"].id, w<<16)
	r.add(s.plane, p["SRC_H"].id, h<<16)
	r.add(s.plane, p["CRTC_X"].id, 0)
	r.add(s.plane, p["CRTC_Y"].id, 0)
	r.add(s.plane, p["CRTC_W"].id, w)
	r.add(s.plane, p["CRTC_H"].id, h)
	if rotate {
		r.add(s.plane, p["rotation"].id, rotation)
	}
}

// modeset turns the screen on showing fb: the mode, the connector on the
// CRTC, and the plane. It tests the commit first, then commits it with a
// flip event, and blocks until the kernel has taken it.
func (s *screen) modeset(fb uint32, rotation uint64, rotate bool, userData uint64) error {
	c := s.dev
	if s.modeBlob == 0 {
		id, err := c.createBlob(s.mode.bytes())
		if err != nil {
			return fmt.Errorf("mode blob: %w", err)
		}
		s.modeBlob = id
	}
	build := func() {
		s.req.reset()
		s.req.add(s.conn.id, s.connProps["CRTC_ID"].id, uint64(s.crtc))
		s.req.add(s.crtc, s.crtcProps["MODE_ID"].id, uint64(s.modeBlob))
		s.req.add(s.crtc, s.crtcProps["ACTIVE"].id, 1)
		s.setPlane(fb, rotation, rotate)
	}
	build()
	if err := s.req.commit(c, atomicTestOnly|atomicAllowModeset, 0); err != nil {
		return s.masterErr(fmt.Errorf("test commit: %w", err))
	}
	build()
	if err := s.req.commit(c, atomicAllowModeset|atomicPageFlipEvent, userData); err != nil {
		return s.masterErr(fmt.Errorf("modeset: %w", err))
	}
	return nil
}

// flip shows fb from the next vblank on, without blocking, and asks for a
// flip event.
func (s *screen) flip(fb uint32, userData uint64) error {
	s.req.reset()
	s.req.add(s.plane, s.planeProps["FB_ID"].id, uint64(fb))
	return s.masterErr(s.req.commit(s.dev, atomicNonblock|atomicPageFlipEvent, userData))
}

// off turns the display off: the CRTC inactive, which is how KMS does
// DPMS, with its mode and plane kept, so a modeset turns it on again. It
// blocks until the kernel has done it.
func (s *screen) off() error {
	s.req.reset()
	s.req.add(s.crtc, s.crtcProps["ACTIVE"].id, 0)
	return s.masterErr(s.req.commit(s.dev, atomicAllowModeset, 0))
}

// masterErr explains a refused commit: another program has the screen.
func (s *screen) masterErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errEACCES) || errors.Is(err, errEPERM) {
		return fmt.Errorf("%w: %w", errNotMaster, err)
	}
	return err
}

func (s *screen) close() {
	if s.modeBlob != 0 {
		_ = s.card.destroyBlob(s.modeBlob) // freed as the card closes, if not
		s.modeBlob = 0
	}
	s.card.close()
}
