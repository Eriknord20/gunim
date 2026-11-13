//go:build linux && (arm64 || amd64)

package kms

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The structures below are Linux's, from include/uapi/drm/drm.h and
// drm_mode.h. Every pointer in them is a __u64, so they lie the same on
// 32 and 64 bits, apart from drmVersion's size_t fields.

type drmVersion struct {
	major, minor, patch int32
	nameLen             uintptr
	name                uintptr
	dateLen             uintptr
	date                uintptr
	descLen             uintptr
	desc                uintptr
}

type drmCap struct {
	capability uint64
	value      uint64
}

type modeCardRes struct {
	fbs, crtcs, connectors, encoders                     uint64
	countFbs, countCrtcs, countConnectors, countEncoders uint32
	minWidth, maxWidth, minHeight, maxHeight             uint32
}

// modeInfo is drm_mode_modeinfo: one display mode.
type modeInfo struct {
	Clock                                         uint32
	HDisplay, HSyncStart, HSyncEnd, HTotal, HSkew uint16
	VDisplay, VSyncStart, VSyncEnd, VTotal, VScan uint16
	VRefresh                                      uint32
	Flags                                         uint32
	Type                                          uint32
	Name                                          [32]byte
}

// rate returns the mode's refresh rate in Hz, from its timings.
func (m modeInfo) rate() float64 {
	if m.HTotal == 0 || m.VTotal == 0 {
		return float64(m.VRefresh)
	}
	r := float64(m.Clock) * 1000 / (float64(m.HTotal) * float64(m.VTotal))
	if m.Flags&modeFlagInterlace != 0 {
		r *= 2
	}
	if m.Flags&modeFlagDblScan != 0 {
		r /= 2
	}
	if m.VScan > 1 {
		r /= float64(m.VScan)
	}
	return r
}

// bytes returns the mode as the kernel lays it out, for a property blob.
func (m modeInfo) bytes() []byte {
	b := make([]byte, unsafe.Sizeof(m))
	binary.NativeEndian.PutUint32(b[0:], m.Clock)
	for i, v := range []uint16{m.HDisplay, m.HSyncStart, m.HSyncEnd, m.HTotal, m.HSkew, m.VDisplay, m.VSyncStart, m.VSyncEnd, m.VTotal, m.VScan} {
		binary.NativeEndian.PutUint16(b[4+2*i:], v)
	}
	binary.NativeEndian.PutUint32(b[24:], m.VRefresh)
	binary.NativeEndian.PutUint32(b[28:], m.Flags)
	binary.NativeEndian.PutUint32(b[32:], m.Type)
	copy(b[36:], m.Name[:])
	return b
}

const (
	modeFlagInterlace = 1 << 4
	modeFlagDblScan   = 1 << 5
	modeTypePreferred = 1 << 3
)

type modeGetConnector struct {
	encoders, modes, props, propValues            uint64
	countModes, countProps, countEncoders         uint32
	encoderID, connectorID, connType, connTypeID  uint32
	connection, mmWidth, mmHeight, subpixel, _pad uint32
}

type modeGetEncoder struct {
	encoderID, encoderType, crtcID, possibleCrtcs, possibleClones uint32
}

type modeGetPlaneRes struct {
	planes      uint64
	countPlanes uint32
	_           uint32
}

type modeGetPlane struct {
	planeID, crtcID, fbID, possibleCrtcs, gammaSize, countFormats uint32
	formats                                                       uint64
}

type modeObjGetProperties struct {
	props, values              uint64
	countProps, objID, objType uint32
	_                          uint32
}

type modeGetProperty struct {
	values, enumBlobs uint64
	propID, flags     uint32
	name              [32]byte
	countValues       uint32
	countEnumBlobs    uint32
}

type modePropertyEnum struct {
	value uint64
	name  [32]byte
}

type modeFBCmd2 struct {
	fbID, width, height, format, flags uint32
	handles                            [4]uint32
	pitches                            [4]uint32
	offsets                            [4]uint32
	_                                  uint32
	modifiers                          [4]uint64
}

type modeAtomic struct {
	flags, countObjs                uint32
	objs, countProps, props, values uint64
	reserved, userData              uint64
}

type modeCreateBlob struct {
	data   uint64
	length uint32
	blobID uint32
}

// The ioctls, from drm.h.
var (
	ioctlVersion      = drmIOWR(0x00, unsafe.Sizeof(drmVersion{}))
	ioctlGetCap       = drmIOWR(0x0c, unsafe.Sizeof(drmCap{}))
	ioctlSetClientCap = iow('d', 0x0d, unsafe.Sizeof(drmCap{}))
	ioctlSetMaster    = ion('d', 0x1e)
	ioctlDropMaster   = ion('d', 0x1f)
	ioctlGetResources = drmIOWR(0xa0, unsafe.Sizeof(modeCardRes{}))
	ioctlGetEncoder   = drmIOWR(0xa6, unsafe.Sizeof(modeGetEncoder{}))
	ioctlGetConnector = drmIOWR(0xa7, unsafe.Sizeof(modeGetConnector{}))
	ioctlGetProperty  = drmIOWR(0xaa, unsafe.Sizeof(modeGetProperty{}))
	ioctlRmFB         = drmIOWR(0xaf, 4)
	ioctlGetPlaneRes  = drmIOWR(0xb5, unsafe.Sizeof(modeGetPlaneRes{}))
	ioctlGetPlane     = drmIOWR(0xb6, unsafe.Sizeof(modeGetPlane{}))
	ioctlAddFB2       = drmIOWR(0xb8, unsafe.Sizeof(modeFBCmd2{}))
	ioctlObjGetProps  = drmIOWR(0xb9, unsafe.Sizeof(modeObjGetProperties{}))
	ioctlAtomic       = drmIOWR(0xbc, unsafe.Sizeof(modeAtomic{}))
	ioctlCreateBlob   = drmIOWR(0xbd, unsafe.Sizeof(modeCreateBlob{}))
	ioctlDestroyBlob  = drmIOWR(0xbe, 4)
)

const (
	capTimestampMonotonic   = 0x6
	clientCapUniversalPlane = 2
	clientCapAtomic         = 3

	objCrtc      = 0xcccccccc
	objConnector = 0xc0c0c0c0
	objPlane     = 0xeeeeeeee

	planeTypePrimary = 1

	connected = 1

	atomicPageFlipEvent = 0x01
	atomicTestOnly      = 0x0100
	atomicNonblock      = 0x0200
	atomicAllowModeset  = 0x0400

	eventFlipComplete = 0x02

	formatXRGB8888 = 'X' | 'R'<<8 | '2'<<16 | '4'<<24
)

// connectorTypes names a connector's type as the kernel does
// (drm_connector.c), for names such as "DSI-2" and "HDMI-A-1".
var connectorTypes = map[uint32]string{
	0: "Unknown", 1: "VGA", 2: "DVI-I", 3: "DVI-D", 4: "DVI-A", 5: "Composite", 6: "SVIDEO", 7: "LVDS",
	8: "Component", 9: "DIN", 10: "DP", 11: "HDMI-A", 12: "HDMI-B", 13: "TV", 14: "eDP", 15: "Virtual",
	16: "DSI", 17: "DPI", 18: "Writeback", 19: "SPI", 20: "USB",
}

const (
	connectorDSI   = 16
	connectorHDMIA = 11
)

// A dev is the calls the driver makes on a DRM file to set what the screen
// shows. The real one makes them on the kernel, and a test stands in for
// it.
type dev interface {
	atomicer
	createBlob(b []byte) (uint32, error)
}

// card is an open DRM primary node.
type card struct {
	fd   int
	path string
	name string // the kernel driver's, as "drm-rp1-dsi" or "vc4"
}

func (c *card) atomic(req *modeAtomic) error {
	return ioctl(c.fd, ioctlAtomic, unsafe.Pointer(req))
}

func openCard(path string) (*card, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	c := &card{fd: fd, path: path}
	name := make([]byte, 64)
	var p pins
	v := drmVersion{nameLen: uintptr(len(name)), name: uintptr(p.bytes(name))}
	err = ioctl(fd, ioctlVersion, unsafe.Pointer(&v))
	p.Unpin()
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%s: version: %w", path, err)
	}
	c.name = cstr(name[:min(int(v.nameLen), len(name))])
	return c, nil
}

func (c *card) close() { _ = unix.Close(c.fd) }

func (c *card) cap(which uint64) (uint64, error) {
	v := drmCap{capability: which}
	err := ioctl(c.fd, ioctlGetCap, unsafe.Pointer(&v))
	return v.value, err
}

func (c *card) setClientCap(which, value uint64) error {
	v := drmCap{capability: which, value: value}
	return ioctl(c.fd, ioctlSetClientCap, unsafe.Pointer(&v))
}

func (c *card) setMaster() error  { return ioctlInt(c.fd, ioctlSetMaster, 0) }
func (c *card) dropMaster() error { return ioctlInt(c.fd, ioctlDropMaster, 0) }

// resources lists the card's CRTCs and connectors.
func (c *card) resources() (crtcs, connectors []uint32, err error) {
	for {
		var r modeCardRes
		if err := ioctl(c.fd, ioctlGetResources, unsafe.Pointer(&r)); err != nil {
			return nil, nil, err
		}
		crtcs, connectors = make([]uint32, r.countCrtcs), make([]uint32, r.countConnectors)
		want := r
		var p pins
		r.crtcs, r.connectors = p.u32s(crtcs), p.u32s(connectors)
		r.countFbs, r.countEncoders = 0, 0
		err := ioctl(c.fd, ioctlGetResources, unsafe.Pointer(&r))
		p.Unpin()
		if err != nil {
			return nil, nil, err
		}
		// A connector that came between the two calls has the count grow:
		// ask again.
		if r.countCrtcs == want.countCrtcs && r.countConnectors == want.countConnectors {
			return crtcs, connectors, nil
		}
	}
}

// connector is what the driver keeps of a connector.
type connector struct {
	id, encoder       uint32
	typ, typeID       uint32
	connected         bool
	mmWidth, mmHeight uint32
	modes             []modeInfo
	encoders          []uint32
}

func (k connector) name() string {
	t, ok := connectorTypes[k.typ]
	if !ok {
		t = "Unknown"
	}
	return fmt.Sprintf("%s-%d", t, k.typeID)
}

// preferred returns the connector's preferred mode, or its first.
func (k connector) preferred() (modeInfo, bool) {
	for _, m := range k.modes {
		if m.Type&modeTypePreferred != 0 {
			return m, true
		}
	}
	if len(k.modes) > 0 {
		return k.modes[0], true
	}
	return modeInfo{}, false
}

func (c *card) connector(id uint32) (connector, error) {
	for {
		g := modeGetConnector{connectorID: id}
		if err := ioctl(c.fd, ioctlGetConnector, unsafe.Pointer(&g)); err != nil {
			return connector{}, err
		}
		modes := make([]modeInfo, g.countModes)
		encs := make([]uint32, g.countEncoders)
		want := g
		var p pins
		g.modes, g.encoders = p.modes(modes), p.u32s(encs)
		g.countProps, g.props, g.propValues = 0, 0, 0
		err := ioctl(c.fd, ioctlGetConnector, unsafe.Pointer(&g))
		p.Unpin()
		if err != nil {
			return connector{}, err
		}
		if g.countModes != want.countModes || g.countEncoders != want.countEncoders {
			continue
		}
		return connector{
			id: id, encoder: g.encoderID, typ: g.connType, typeID: g.connTypeID,
			connected: g.connection == connected, mmWidth: g.mmWidth, mmHeight: g.mmHeight,
			modes: modes, encoders: encs,
		}, nil
	}
}

func (c *card) encoder(id uint32) (modeGetEncoder, error) {
	e := modeGetEncoder{encoderID: id}
	err := ioctl(c.fd, ioctlGetEncoder, unsafe.Pointer(&e))
	return e, err
}

func (c *card) planes() ([]uint32, error) {
	var r modeGetPlaneRes
	if err := ioctl(c.fd, ioctlGetPlaneRes, unsafe.Pointer(&r)); err != nil {
		return nil, err
	}
	ids := make([]uint32, r.countPlanes)
	var p pins
	r.planes = p.u32s(ids)
	err := ioctl(c.fd, ioctlGetPlaneRes, unsafe.Pointer(&r))
	p.Unpin()
	return ids[:min(int(r.countPlanes), len(ids))], err
}

func (c *card) plane(id uint32) (modeGetPlane, error) {
	g := modeGetPlane{planeID: id}
	err := ioctl(c.fd, ioctlGetPlane, unsafe.Pointer(&g))
	g.countFormats, g.formats = 0, 0
	return g, err
}

// props returns an object's properties, by name, with their ids and
// current values.
func (c *card) props(obj, typ uint32) (map[string]prop, error) {
	g := modeObjGetProperties{objID: obj, objType: typ}
	if err := ioctl(c.fd, ioctlObjGetProps, unsafe.Pointer(&g)); err != nil {
		return nil, err
	}
	ids := make([]uint32, g.countProps)
	vals := make([]uint64, g.countProps)
	var p pins
	g.props, g.values = p.u32s(ids), p.u64s(vals)
	err := ioctl(c.fd, ioctlObjGetProps, unsafe.Pointer(&g))
	p.Unpin()
	if err != nil {
		return nil, err
	}
	n := min(int(g.countProps), len(ids))
	out := make(map[string]prop, n)
	for i := range n {
		gp := modeGetProperty{propID: ids[i]}
		if err := ioctl(c.fd, ioctlGetProperty, unsafe.Pointer(&gp)); err != nil {
			return nil, err
		}
		out[cstr(gp.name[:])] = prop{id: ids[i], value: vals[i], enums: gp.countEnumBlobs, flags: gp.flags}
	}
	return out, nil
}

// propEnums returns an enum property's names by value.
func (c *card) propEnums(id uint32) (map[string]uint64, error) {
	gp := modeGetProperty{propID: id}
	if err := ioctl(c.fd, ioctlGetProperty, unsafe.Pointer(&gp)); err != nil {
		return nil, err
	}
	enums := make([]modePropertyEnum, gp.countEnumBlobs)
	vals := make([]uint64, gp.countValues)
	if len(enums) > 0 {
		var p pins
		p.Pin(&enums[0])
		gp.enumBlobs = uint64(uintptr(unsafe.Pointer(&enums[0])))
		gp.values = p.u64s(vals)
		err := ioctl(c.fd, ioctlGetProperty, unsafe.Pointer(&gp))
		p.Unpin()
		if err != nil {
			return nil, err
		}
	}
	out := map[string]uint64{}
	for _, e := range enums {
		out[cstr(e.name[:])] = e.value
	}
	return out, nil
}

// prop is one property of an object.
type prop struct {
	id    uint32
	value uint64
	enums uint32
	flags uint32
}

func (c *card) createBlob(b []byte) (uint32, error) {
	var p pins
	cb := modeCreateBlob{data: p.bytes(b), length: uint32(len(b))}
	err := ioctl(c.fd, ioctlCreateBlob, unsafe.Pointer(&cb))
	p.Unpin()
	return cb.blobID, err
}

func (c *card) destroyBlob(id uint32) error {
	return ioctl(c.fd, ioctlDestroyBlob, unsafe.Pointer(&id))
}

// addFB makes a framebuffer of one buffer object.
func (c *card) addFB(w, h, format, handle, pitch uint32) (uint32, error) {
	f := modeFBCmd2{width: w, height: h, format: format}
	f.handles[0], f.pitches[0] = handle, pitch
	err := ioctl(c.fd, ioctlAddFB2, unsafe.Pointer(&f))
	return f.fbID, err
}

func (c *card) rmFB(id uint32) error {
	return ioctl(c.fd, ioctlRmFB, unsafe.Pointer(&id))
}

// flipEvent is a page flip the kernel reports done.
type flipEvent struct {
	userData  uint64
	sec, usec uint32
	sequence  uint32
	crtc      uint32
}

// readEvents reads the events waiting on the card, and calls fn for each
// page flip. It returns at once where none wait.
func (c *card) readEvents(buf []byte, fn func(flipEvent)) error {
	for {
		n, err := unix.Read(c.fd, buf)
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN {
			return nil
		}
		if err != nil {
			return err
		}
		parseEvents(buf[:n], fn)
		if n < len(buf)/2 {
			return nil
		}
	}
}

// parseEvents calls fn for each page flip in b, events as the kernel
// writes them to a DRM file.
func parseEvents(b []byte, fn func(flipEvent)) {
	for len(b) >= 8 {
		typ := binary.NativeEndian.Uint32(b)
		length := int(binary.NativeEndian.Uint32(b[4:]))
		if length < 8 || length > len(b) {
			return
		}
		if typ == eventFlipComplete && length >= 32 {
			fn(flipEvent{
				userData: binary.NativeEndian.Uint64(b[8:]),
				sec:      binary.NativeEndian.Uint32(b[16:]),
				usec:     binary.NativeEndian.Uint32(b[20:]),
				sequence: binary.NativeEndian.Uint32(b[24:]),
				crtc:     binary.NativeEndian.Uint32(b[28:]),
			})
		}
		b = b[length:]
	}
}
