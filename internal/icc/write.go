package icc

import (
	"encoding/binary"
	"math"
	"unicode/utf16"
)

// Write encodes a matrix/TRC display profile: its description, its
// white, its colorants and its curves, as version 4 when p.Version is 4
// and version 2 otherwise. It is for tests, and for trying a profile on
// a display: it writes no lookup tables.
func Write(p *Profile) []byte {
	type tag struct {
		sig  string
		data []byte
	}
	var tags []tag
	if p.Version == 4 {
		tags = append(tags, tag{"desc", mluc(p.Description)})
	} else {
		tags = append(tags, tag{"desc", textDesc(p.Description)})
	}
	white := p.White
	if white == (XYZ{}) {
		white = D50
	}
	tags = append(tags, tag{"wtpt", xyzTag(white)})
	for i, nm := range [3]string{"r", "g", "b"} {
		tags = append(tags, tag{nm + "XYZ", xyzTag(XYZ{p.Matrix[0][i], p.Matrix[1][i], p.Matrix[2][i]})})
	}
	for i, nm := range [3]string{"r", "g", "b"} {
		tags = append(tags, tag{nm + "TRC", curveTag(p.TRC[i])})
	}

	out := make([]byte, 132+12*len(tags))
	for _, t := range tags {
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		out = append(out, t.data...)
	}
	// The tag table, filled in now the offsets are known.
	off := 132 + 12*len(tags)
	for i, t := range tags {
		for off%4 != 0 {
			off++
		}
		e := out[132+12*i:]
		copy(e[0:4], t.sig)
		binary.BigEndian.PutUint32(e[4:8], uint32(off))
		binary.BigEndian.PutUint32(e[8:12], uint32(len(t.data)))
		off += len(t.data)
	}
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	copy(out[4:8], "gnim")
	if p.Version == 4 {
		out[8] = 4
		out[9] = 0x30
	} else {
		out[8] = 2
		out[9] = 0x10
	}
	class, space, pcs := p.Class, p.ColorSpace, p.PCS
	if class == "" {
		class = "mntr"
	}
	if space == "" {
		space = "RGB "
	}
	if pcs == "" {
		pcs = "XYZ "
	}
	copy(out[12:16], class)
	copy(out[16:20], space)
	copy(out[20:24], pcs)
	copy(out[36:40], "acsp")
	// The illuminant: D50.
	putS15(out[68:], D50[0])
	putS15(out[72:], D50[1])
	putS15(out[76:], D50[2])
	binary.BigEndian.PutUint32(out[128:132], uint32(len(tags)))
	return out
}

func putS15(b []byte, v float64) {
	binary.BigEndian.PutUint32(b, uint32(int32(math.Round(v*65536))))
}

func xyzTag(v XYZ) []byte {
	b := make([]byte, 20)
	copy(b, "XYZ ")
	putS15(b[8:], v[0])
	putS15(b[12:], v[1])
	putS15(b[16:], v[2])
	return b
}

func curveTag(c Curve) []byte {
	if len(c.Table) > 0 {
		b := make([]byte, 12+2*len(c.Table))
		copy(b, "curv")
		binary.BigEndian.PutUint32(b[8:], uint32(len(c.Table)))
		for i, v := range c.Table {
			binary.BigEndian.PutUint16(b[12+2*i:], uint16(math.Round(clamp01(v)*65535)))
		}
		return b
	}
	if c.Fn == 0 && c.Params[0] == 1 {
		b := make([]byte, 12)
		copy(b, "curv")
		return b
	}
	counts := [...]int{1, 3, 4, 5, 7}
	b := make([]byte, 12+4*counts[c.Fn])
	copy(b, "para")
	binary.BigEndian.PutUint16(b[8:], uint16(c.Fn))
	for i := range counts[c.Fn] {
		putS15(b[12+4*i:], c.Params[i])
	}
	return b
}

func textDesc(s string) []byte {
	// textDescriptionType: the ASCII name with its count, then empty
	// Unicode and ScriptCode parts.
	b := make([]byte, 12, 12+len(s)+1+8+3+67)
	copy(b, "desc")
	binary.BigEndian.PutUint32(b[8:], uint32(len(s)+1))
	b = append(b, s...)
	b = append(b, 0)
	b = append(b, make([]byte, 8+3+67)...)
	return b
}

func mluc(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 28, 28+2*len(u))
	copy(b, "mluc")
	binary.BigEndian.PutUint32(b[8:], 1)
	binary.BigEndian.PutUint32(b[12:], 12)
	copy(b[16:], "enUS")
	binary.BigEndian.PutUint32(b[20:], uint32(2*len(u)))
	binary.BigEndian.PutUint32(b[24:], 28)
	for _, c := range u {
		b = binary.BigEndian.AppendUint16(b, c)
	}
	return b
}
