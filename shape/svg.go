package shape

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/vecpath"
	"github.com/marrasen/gunim/paint"
)

// ParseSVG reads an SVG file into a [Figure], as a vector editor such as Inkscape or Figma saves one, for art
// drawn by hand rather than in code.
//
// It reads the shapes: path, rect (with rounded corners), circle, ellipse, line, polyline and polygon, in groups
// and nested svg elements, each with its transform. It reads their fill, fill-opacity, fill-rule, stroke,
// stroke-width, stroke-opacity, opacity and display, as attributes or in a style attribute, inherited down
// groups; colours as #rgb, #rrggbb, rgb() or the basic named colours; and linear and radial gradients, in either
// gradientUnits, with their stops, transforms and the stops of a gradient they link to. A radial gradient is drawn
// out from its centre, its focus left out, in ellipses where the box or a transform stretches it one way more than
// the other; one skewed is drawn with its axes set square.
//
// It leaves out text, images, filters, masks, clip paths, markers and patterns, and draws every stroke solid,
// with round caps and joins. Opacity on a group is given to each shape in it, so shapes in a faded group that
// overlap show through each other.
//
// A <style> sheet with rules in it, and a <use> element, are errors that name the element: the reader takes
// presentation from attributes and style attributes, and draws each shape where it is written. An editor saves a
// file this reads when told to keep styles on the elements and to unlink clones and symbols, as Illustrator does
// with its "Presentation Attributes" styling.
//
// Numbers past float32, and paths whose bounds reach past it, are errors too.
func ParseSVG(src []byte) (*Figure, error) {
	var root node
	if err := xml.NewDecoder(bytes.NewReader(src)).Decode(&root); err != nil {
		return nil, fmt.Errorf("shape: svg: %w", err)
	}
	if root.XMLName.Local != "svg" {
		return nil, fmt.Errorf("shape: svg: the root is <%s>, not <svg>", root.XMLName.Local)
	}
	r := &reader{grads: map[string]*node{}}
	if err := r.collect(&root); err != nil {
		return nil, err
	}
	f := &Figure{}
	if err := r.draw(&root, rootStyle(), vecpath.Identity, f); err != nil {
		return nil, err
	}
	f.ViewBox = viewBox(&root, f)
	return f, nil
}

// node is an XML element with its attributes and children.
type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []node     `xml:",any"`
	// Text is the text inside the element, as a style sheet holds its rules.
	Text string `xml:",chardata"`
}

// attr returns the attribute named name, in any namespace, and whether there is one.
func (n *node) attr(name string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return strings.TrimSpace(a.Value), true
		}
	}
	return "", false
}

// prop returns a presentation property: from the style attribute, or else the attribute of its name.
func (n *node) prop(name string) (string, bool) {
	if st, ok := n.attr("style"); ok {
		for decl := range strings.SplitSeq(st, ";") {
			k, v, ok := strings.Cut(decl, ":")
			if ok && strings.TrimSpace(k) == name {
				return strings.TrimSpace(v), true
			}
		}
	}
	return n.attr(name)
}

// has reports whether n has the attribute named name, with a value other than auto.
func (n *node) has(name string) bool {
	v, ok := n.attr(name)
	return ok && v != "" && v != "auto"
}

// num reads a numeric attribute, with a unit such as px left off, or def where there is none or it is auto.
func (n *node) num(name string, def float32) (float32, error) {
	if !n.has(name) {
		return def, nil
	}
	v, _ := n.attr(name)
	return length(v)
}

// length reads a number with a unit such as px or pt left off.
func length(v string) (float32, error) {
	f, err := strconv.ParseFloat(strings.TrimRight(strings.TrimSpace(v), "abcdefghijklmnopqrstuvwxyz%"), 32)
	if err != nil || !finite(float32(f)) {
		return 0, fmt.Errorf("shape: svg: %q is not a finite number", v)
	}
	return float32(f), nil
}

// paintSpec is a fill or a stroke as SVG says it: none, a colour, or a gradient by its id.
type paintSpec struct {
	none bool
	col  color.NRGBA
	grad string
}

// style is the presentation inherited down the tree.
type style struct {
	fill, stroke          paintSpec
	fillOpacity, strokeOp float32
	// opacity is the product of the element's and its groups' opacities, which each shape takes.
	opacity      float32
	strokeWidth  float32
	evenOdd      bool
	currentColor color.NRGBA
	shown        bool
}

// rootStyle is SVG's initial style: filled black, with a stroke of none.
func rootStyle() style {
	black := color.NRGBA{A: 0xff}
	return style{
		fill: paintSpec{col: black}, stroke: paintSpec{none: true}, fillOpacity: 1, strokeOp: 1, opacity: 1,
		strokeWidth: 1, currentColor: black, shown: true,
	}
}

// reader reads an SVG's shapes, knowing its gradients by id.
type reader struct {
	grads map[string]*node
}

// errStyleSheet is the error for an SVG with a style sheet, whose rules the reader would leave out.
var errStyleSheet = errors.New("shape: svg: a <style> sheet's rules are not read; " +
	"give fill and stroke as attributes or in style attributes, as an editor does when told to")

// collect finds the gradients anywhere in the tree. A style sheet with rules in it is an error.
func (r *reader) collect(n *node) error {
	switch n.XMLName.Local {
	case "linearGradient", "radialGradient":
		if id, ok := n.attr("id"); ok {
			r.grads[id] = n
		}
	case "style":
		if strings.TrimSpace(cssComments.ReplaceAllString(n.Text, "")) != "" {
			return errStyleSheet
		}
	}
	for i := range n.Children {
		if err := r.collect(&n.Children[i]); err != nil {
			return err
		}
	}
	return nil
}

// cssComments matches the comments in a style sheet.
var cssComments = regexp.MustCompile(`(?s)/\*.*?\*/`)

// skipped are the elements the reader passes over with their children: definitions, drawn only where something
// refers to them, and what it leaves out.
var skipped = map[string]bool{
	"defs": true, "symbol": true, "clipPath": true, "mask": true, "pattern": true, "marker": true, "filter": true,
	"linearGradient": true, "radialGradient": true, "text": true, "image": true, "title": true, "desc": true,
	"metadata": true, "style": true, "script": true, "foreignObject": true, "switch": true,
}

// draw adds n's shapes, and its children's, to f, in st as inherited and ctm the transform from n's parent's
// units to the figure's.
func (r *reader) draw(n *node, st style, ctm vecpath.Affine, f *Figure) error {
	if skipped[n.XMLName.Local] {
		return nil
	}
	if n.XMLName.Local == "use" {
		return errors.New("shape: svg: <use> is not read; the reader draws each shape where it is written, " +
			"so unlink clones and symbols in the editor before saving")
	}
	st, err := r.inherit(n, st)
	if err != nil {
		return err
	}
	if !st.shown {
		return nil
	}
	if t, ok := n.attr("transform"); ok {
		m, terr := transform(t)
		if terr != nil {
			return terr
		}
		ctm = m.Then(ctm)
	}
	subs, err := outline(n)
	if err != nil {
		return err
	}
	if subs != nil {
		return r.part(n, subs, st, ctm, f)
	}
	if n.XMLName.Local == "g" || n.XMLName.Local == "svg" || n.XMLName.Local == "a" {
		for i := range n.Children {
			if err := r.draw(&n.Children[i], st, ctm, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// inherit is st with n's own presentation properties set.
func (r *reader) inherit(n *node, st style) (style, error) {
	var err error
	if v, ok := n.prop("color"); ok {
		if c, ok, cerr := colour(v, st.currentColor); cerr == nil && ok {
			st.currentColor = c
		}
	}
	for _, p := range []struct {
		name string
		to   *paintSpec
	}{{"fill", &st.fill}, {"stroke", &st.stroke}} {
		v, ok := n.prop(p.name)
		if !ok || v == "inherit" {
			continue
		}
		spec, perr := r.paint(v, st.currentColor)
		if perr != nil {
			return st, perr
		}
		*p.to = spec
	}
	for _, p := range []struct {
		name string
		to   *float32
	}{{"fill-opacity", &st.fillOpacity}, {"stroke-opacity", &st.strokeOp}} {
		if v, ok := n.prop(p.name); ok {
			if *p.to, err = opacity(v); err != nil {
				return st, err
			}
		}
	}
	if v, ok := n.prop("opacity"); ok {
		o, oerr := opacity(v)
		if oerr != nil {
			return st, oerr
		}
		st.opacity *= o
	}
	if v, ok := n.prop("stroke-width"); ok {
		if st.strokeWidth, err = length(v); err != nil {
			return st, err
		}
	}
	if v, ok := n.prop("fill-rule"); ok {
		st.evenOdd = v == "evenodd"
	}
	if v, ok := n.prop("display"); ok && v == "none" {
		st.shown = false
	}
	if v, ok := n.prop("visibility"); ok && (v == "hidden" || v == "collapse") {
		st.shown = false
	}
	return st, nil
}

// paint reads a fill or a stroke: none, a colour, or url(#id) of a gradient. A colour after the url is the fallback,
// taken in place of a gradient the file does not hold.
func (r *reader) paint(v string, current color.NRGBA) (paintSpec, error) {
	var spec paintSpec
	if rest, ok := strings.CutPrefix(v, "url("); ok {
		ref, fallback, ok := strings.Cut(rest, ")")
		if !ok {
			return spec, fmt.Errorf("shape: svg: paint %q has no closing bracket", v)
		}
		id := strings.TrimPrefix(strings.Trim(strings.TrimSpace(ref), `"'`), "#")
		if fallback = strings.TrimSpace(fallback); fallback == "" {
			spec.grad = id
			return spec, nil
		}
		if _, ok := r.grads[id]; ok {
			spec.grad = id
			return spec, nil
		}
		v = fallback
	}
	c, ok, err := colour(v, current)
	if err != nil {
		return spec, err
	}
	spec.none, spec.col = !ok, c
	return spec, nil
}

// part adds a shape to f, its outline subs in its own units.
func (r *reader) part(n *node, subs []vecpath.Subpath, st style, ctm vecpath.Affine, f *Figure) error {
	if len(subs) == 0 {
		return nil
	}
	own, err := pathOf(subs)
	if err != nil {
		return fmt.Errorf("shape: svg: <%s>: %w", n.XMLName.Local, err)
	}
	path, err := pathOf(vecpath.Transform(subs, ctm))
	if err != nil {
		return fmt.Errorf("shape: svg: <%s> as transformed: %w", n.XMLName.Local, err)
	}
	pt := Part{Path: path, EvenOdd: st.evenOdd}
	// A line has no inside; a polyline does, closed back to its first point, as SVG fills one.
	if !st.fill.none && n.XMLName.Local != "line" {
		if st.fill.grad != "" {
			pt.FillGradient = r.gradient(st.fill.grad, own.Bounds(), ctm, st.fillOpacity*st.opacity)
			if pt.FillGradient == nil {
				pt.Fill = color.NRGBA{A: 0xff}
			}
		} else {
			pt.Fill = paint.Faded(st.fill.col, st.fillOpacity*st.opacity)
		}
	}
	if !st.stroke.none && st.strokeWidth > 0 {
		c := st.stroke.col
		if st.stroke.grad != "" {
			// A stroke takes its gradient's first colour.
			if g := r.gradient(st.stroke.grad, own.Bounds(), ctm, 1); g != nil {
				c = g.Start
			}
		}
		pt.Stroke = paint.Faded(c, st.strokeOp*st.opacity)
		// The stroke grows with the transform, as the mean of its two scales.
		pt.Width = st.strokeWidth * float32(math.Sqrt(math.Abs(float64(ctm.A*ctm.D-ctm.B*ctm.C))))
		if !finite(pt.Width) {
			return fmt.Errorf("shape: svg: <%s>: its stroke, as transformed, is wider than float32 reaches", n.XMLName.Local)
		}
	}
	if pt.Fill.A > 0 || pt.FillGradient != nil || (pt.Stroke.A > 0 && pt.Width > 0) {
		f.Parts = append(f.Parts, pt)
	}
	return nil
}

// gradient is the gradient id, for a shape whose own bounds are box, mapped by ctm into the figure's units, its
// colours faded by op; nil where there is no such gradient or it has no stops.
func (r *reader) gradient(id string, box geom.Rect, ctm vecpath.Affine, op float32) *paint.Gradient {
	n, ok := r.grads[id]
	if !ok {
		return nil
	}
	// Attributes and stops come from the gradient, or else from the one it links to.
	chain := []*node{n}
	for range 8 {
		href, ok := chain[len(chain)-1].attr("href")
		if !ok {
			break
		}
		next, ok := r.grads[strings.TrimPrefix(href, "#")]
		if !ok {
			break
		}
		chain = append(chain, next)
	}
	get := func(name, def string) string {
		for _, c := range chain {
			if v, ok := c.attr(name); ok {
				return v
			}
		}
		return def
	}
	var stops []paint.Stop
	for _, c := range chain {
		for i := range c.Children {
			s := &c.Children[i]
			if s.XMLName.Local != "stop" {
				continue
			}
			at, _ := s.attr("offset")
			off := float32(0)
			if strings.HasSuffix(at, "%") {
				v, _ := length(at)
				off = v / 100
			} else if v, err := length(at); err == nil {
				off = v
			}
			col := color.NRGBA{A: 0xff}
			if v, ok := s.prop("stop-color"); ok {
				if cc, ok, err := colour(v, col); err == nil && ok {
					col = cc
				}
			}
			o := float32(1)
			if v, ok := s.prop("stop-opacity"); ok {
				o, _ = opacity(v)
			}
			off = min(max(off, 0), 1)
			if len(stops) > 0 {
				off = max(off, stops[len(stops)-1].At)
			}
			stops = append(stops, paint.Stop{At: off, Color: paint.Faded(col, o*op)})
		}
		if len(stops) > 0 {
			break
		}
	}
	if len(stops) == 0 {
		return nil
	}
	radial := n.XMLName.Local == "radialGradient"
	bbox := get("gradientUnits", "objectBoundingBox") != "userSpaceOnUse"
	coord := func(name, def string) float32 {
		v := get(name, def)
		if strings.HasSuffix(v, "%") {
			f, _ := length(v)
			return f / 100
		}
		f, _ := length(v)
		return f
	}
	var a, b, c vecpath.Pt
	if radial {
		cx, cy, rr := coord("cx", "50%"), coord("cy", "50%"), coord("r", "50%")
		a, b, c = vecpath.Pt{X: cx, Y: cy}, vecpath.Pt{X: cx + rr, Y: cy}, vecpath.Pt{X: cx, Y: cy + rr}
	} else {
		a = vecpath.Pt{X: coord("x1", "0%"), Y: coord("y1", "0%")}
		b = vecpath.Pt{X: coord("x2", "100%"), Y: coord("y2", "0%")}
	}
	m := vecpath.Identity
	if t := get("gradientTransform", ""); t != "" {
		if gt, err := transform(t); err == nil {
			m = gt
		}
	}
	if bbox {
		m = m.Then(vecpath.Affine{A: box.Size().W, D: box.Size().H, E: box.Min.X, F: box.Min.Y})
	}
	m = m.Then(ctm)
	a, b, c = m.Apply(a), m.Apply(b), m.Apply(c)
	if !finite(a.X, a.Y, b.X, b.Y, c.X, c.Y) {
		return nil
	}
	g := &paint.Gradient{From: geom.Pt(a.X, a.Y), To: geom.Pt(b.X, b.Y), Radial: radial}
	if radial {
		// The circle's radius down, against its radius across, as the
		// box and the transforms stretch them.
		along := math.Hypot(float64(b.X-a.X), float64(b.Y-a.Y))
		down := math.Hypot(float64(c.X-a.X), float64(c.Y-a.Y))
		if along > 0 && math.Abs(down/along-1) > 1e-4 {
			g.Aspect = float32(down / along)
		}
	}
	g.Start, g.End = stops[0].Color, stops[len(stops)-1].Color
	if stops[0].At > 0 || stops[len(stops)-1].At < 1 || len(stops) > 2 {
		g.Stops = stops
	}
	return g
}

// outline is the outline of the shape element n, in its own units. For any other element, such as a group, it is
// nil, with a nil error.
func outline(n *node) ([]vecpath.Subpath, error) {
	var d string
	switch n.XMLName.Local {
	case "path":
		d, _ = n.attr("d")
		subs, err := vecpath.Parse(d)
		if err != nil {
			return nil, fmt.Errorf("shape: svg: %w", err)
		}
		if subs == nil {
			subs = []vecpath.Subpath{}
		}
		return subs, nil
	case "rect":
		var v [6]float32
		for i, name := range []string{"x", "y", "width", "height", "rx", "ry"} {
			var err error
			if v[i], err = n.num(name, 0); err != nil {
				return nil, err
			}
		}
		x, y, w, h, rx, ry := v[0], v[1], v[2], v[3], v[4], v[5]
		if w <= 0 || h <= 0 {
			return []vecpath.Subpath{}, nil
		}
		// A corner's radius that is missing, or negative, takes the other one's.
		rxSet, rySet := n.has("rx") && rx >= 0, n.has("ry") && ry >= 0
		switch {
		case rxSet && !rySet:
			ry = rx
		case rySet && !rxSet:
			rx = ry
		case !rxSet && !rySet:
			rx, ry = 0, 0
		}
		rx, ry = min(max(rx, 0), w/2), min(max(ry, 0), h/2)
		if rx == 0 || ry == 0 {
			d = fmt.Sprintf("M%g %gh%gv%gh%gz", x, y, w, h, -w)
		} else {
			d = fmt.Sprintf("M%g %gh%ga%g %g 0 0 1 %g %gv%ga%g %g 0 0 1 %g %gh%ga%g %g 0 0 1 %g %gv%ga%g %g 0 0 1 %g %gz",
				x+rx, y, w-2*rx, rx, ry, rx, ry, h-2*ry, rx, ry, -rx, ry, -(w - 2*rx), rx, ry, -rx, -ry, -(h - 2*ry), rx, ry, rx, -ry)
		}
	case "circle", "ellipse":
		cx, err1 := n.num("cx", 0)
		cy, err2 := n.num("cy", 0)
		rx, err3 := n.num("r", 0)
		ry := rx
		var err4 error
		if n.XMLName.Local == "ellipse" {
			rx, err3 = n.num("rx", 0)
			ry, err4 = n.num("ry", rx)
		}
		if err := errors.Join(err1, err2, err3, err4); err != nil {
			return nil, err
		}
		if rx <= 0 || ry <= 0 {
			return []vecpath.Subpath{}, nil
		}
		d = fmt.Sprintf("M%g %ga%g %g 0 1 0 %g 0a%g %g 0 1 0 %g 0z", cx-rx, cy, rx, ry, 2*rx, rx, ry, -2*rx)
	case "line":
		var v [4]float32
		for i, name := range []string{"x1", "y1", "x2", "y2"} {
			var err error
			if v[i], err = n.num(name, 0); err != nil {
				return nil, err
			}
		}
		d = fmt.Sprintf("M%g %gL%g %g", v[0], v[1], v[2], v[3])
	case "polyline", "polygon":
		pts, _ := n.attr("points")
		fs := strings.FieldsFunc(pts, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
		if len(fs) < 4 {
			return []vecpath.Subpath{}, nil
		}
		d = "M" + strings.Join(fs[:len(fs)/2*2], " ")
		if n.XMLName.Local == "polygon" {
			d += "z"
		}
	default:
		return nil, nil
	}
	subs, err := vecpath.Parse(d)
	if err != nil {
		return nil, fmt.Errorf("shape: svg: <%s>: %w", n.XMLName.Local, err)
	}
	return subs, nil
}

// viewBox is the root's viewBox, or else its width and height, or else the box all the figure's parts lie in.
func viewBox(root *node, f *Figure) geom.Rect {
	if v, ok := root.attr("viewBox"); ok {
		fs := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
		if len(fs) == 4 {
			var n [4]float32
			ok := true
			for i, s := range fs {
				x, err := strconv.ParseFloat(s, 32)
				ok = ok && err == nil && finite(float32(x))
				n[i] = float32(x)
			}
			if ok && n[2] > 0 && n[3] > 0 {
				return geom.Rc(n[0], n[1], n[2], n[3])
			}
		}
	}
	// A width or height in percent is a share of a place the file does not know, and no size of its own.
	wv, _ := root.attr("width")
	hv, _ := root.attr("height")
	w, err1 := root.num("width", 0)
	h, err2 := root.num("height", 0)
	if err1 == nil && err2 == nil && w > 0 && h > 0 && !strings.HasSuffix(wv, "%") && !strings.HasSuffix(hv, "%") {
		return geom.Rc(0, 0, w, h)
	}
	var b geom.Rect
	for i, p := range f.Parts {
		pb := p.Path.Bounds()
		if i == 0 {
			b = pb
			continue
		}
		b = geom.Rect{Min: geom.Pt(min(b.Min.X, pb.Min.X), min(b.Min.Y, pb.Min.Y)), Max: geom.Pt(max(b.Max.X, pb.Max.X), max(b.Max.Y, pb.Max.Y))}
	}
	return b
}

// transform reads an SVG transform list, such as "translate(10 20) rotate(45)", into the one transform it is.
func transform(s string) (vecpath.Affine, error) {
	m := vecpath.Identity
	rest := strings.TrimSpace(s)
	for rest != "" {
		name, args, ok := strings.Cut(rest, "(")
		if !ok {
			return m, fmt.Errorf("shape: svg: transform %q", s)
		}
		args, after, ok := strings.Cut(args, ")")
		if !ok {
			return m, fmt.Errorf("shape: svg: transform %q", s)
		}
		rest = strings.TrimLeft(after, " ,\t\n\r")
		var v []float32
		for f := range strings.FieldsFuncSeq(args, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
			x, err := strconv.ParseFloat(f, 32)
			if err != nil {
				return m, fmt.Errorf("shape: svg: transform %q: %w", s, err)
			}
			if !finite(float32(x)) {
				return m, fmt.Errorf("shape: svg: transform %q: %s is not a finite number", s, f)
			}
			v = append(v, float32(x))
		}
		arg := func(i int, def float32) float32 {
			if i < len(v) {
				return v[i]
			}
			return def
		}
		var t vecpath.Affine
		switch strings.TrimSpace(name) {
		case "matrix":
			if len(v) != 6 {
				return m, fmt.Errorf("shape: svg: transform %q: matrix takes six numbers", s)
			}
			t = vecpath.Affine{A: v[0], B: v[1], C: v[2], D: v[3], E: v[4], F: v[5]}
		case "translate":
			t = vecpath.Affine{A: 1, D: 1, E: arg(0, 0), F: arg(1, 0)}
		case "scale":
			t = vecpath.Affine{A: arg(0, 1), D: arg(1, arg(0, 1))}
		case "rotate":
			sin, cos := math.Sincos(float64(arg(0, 0)) * math.Pi / 180)
			c, sn := float32(cos), float32(sin)
			cx, cy := arg(1, 0), arg(2, 0)
			t = vecpath.Affine{A: 1, D: 1, E: -cx, F: -cy}.
				Then(vecpath.Affine{A: c, B: sn, C: -sn, D: c}).
				Then(vecpath.Affine{A: 1, D: 1, E: cx, F: cy})
		case "skewX":
			t = vecpath.Affine{A: 1, C: float32(math.Tan(float64(arg(0, 0)) * math.Pi / 180)), D: 1}
		case "skewY":
			t = vecpath.Affine{A: 1, B: float32(math.Tan(float64(arg(0, 0)) * math.Pi / 180)), D: 1}
		default:
			return m, fmt.Errorf("shape: svg: transform %q: no %q", s, name)
		}
		// Listed left to right, the rightmost applies first.
		m = t.Then(m)
	}
	return m, nil
}

// opacity reads an opacity, a number or a percentage, held between 0 and 1.
func opacity(v string) (float32, error) {
	f, err := length(v)
	if err != nil {
		return 1, err
	}
	if strings.HasSuffix(strings.TrimSpace(v), "%") {
		f /= 100
	}
	return min(max(f, 0), 1), nil
}

// named are the colours an SVG may name that drawings use most.
var named = map[string]color.NRGBA{
	"black": {0, 0, 0, 255}, "silver": {192, 192, 192, 255}, "gray": {128, 128, 128, 255},
	"grey": {128, 128, 128, 255}, "white": {255, 255, 255, 255}, "maroon": {128, 0, 0, 255}, "red": {255, 0, 0, 255},
	"purple": {128, 0, 128, 255}, "fuchsia": {255, 0, 255, 255}, "magenta": {255, 0, 255, 255},
	"green": {0, 128, 0, 255}, "lime": {0, 255, 0, 255}, "olive": {128, 128, 0, 255}, "yellow": {255, 255, 0, 255},
	"navy": {0, 0, 128, 255}, "blue": {0, 0, 255, 255}, "teal": {0, 128, 128, 255}, "aqua": {0, 255, 255, 255},
	"cyan": {0, 255, 255, 255}, "orange": {255, 165, 0, 255}, "pink": {255, 192, 203, 255},
	"brown": {165, 42, 42, 255}, "gold": {255, 215, 0, 255},
}

// colour reads an SVG colour, and whether it is one: false for none.
func colour(v string, current color.NRGBA) (color.NRGBA, bool, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case v == "none" || v == "transparent":
		return color.NRGBA{}, false, nil
	case v == "currentcolor":
		return current, true, nil
	case strings.HasPrefix(v, "#"):
		h := v[1:]
		if len(h) == 3 || len(h) == 4 {
			var b strings.Builder
			for _, r := range h {
				b.WriteRune(r)
				b.WriteRune(r)
			}
			h = b.String()
		}
		if len(h) != 6 && len(h) != 8 {
			return color.NRGBA{}, false, fmt.Errorf("shape: svg: colour %q", v)
		}
		x, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return color.NRGBA{}, false, fmt.Errorf("shape: svg: colour %q", v)
		}
		if len(h) == 6 {
			x = x<<8 | 0xff
		}
		return color.NRGBA{R: uint8(x >> 24), G: uint8(x >> 16), B: uint8(x >> 8), A: uint8(x)}, true, nil
	case strings.HasPrefix(v, "rgb"):
		_, args, _ := strings.Cut(v, "(")
		args = strings.TrimSuffix(args, ")")
		fs := strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(fs) < 3 {
			return color.NRGBA{}, false, fmt.Errorf("shape: svg: colour %q", v)
		}
		var c [4]uint8
		c[3] = 255
		for i, f := range fs[:min(len(fs), 4)] {
			x, err := length(f)
			if err != nil {
				return color.NRGBA{}, false, fmt.Errorf("shape: svg: colour %q", v)
			}
			switch {
			case strings.HasSuffix(f, "%"):
				x = x / 100 * 255
			case i == 3:
				x *= 255
			}
			c[i] = uint8(min(max(x, 0), 255) + 0.5)
		}
		return color.NRGBA{R: c[0], G: c[1], B: c[2], A: c[3]}, true, nil
	}
	if c, ok := named[v]; ok {
		return c, true, nil
	}
	return color.NRGBA{}, false, fmt.Errorf("shape: svg: colour %q is not one this reads", v)
}
