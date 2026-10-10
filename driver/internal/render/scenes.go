//go:build linux || windows || darwin

package render

import (
	"cmp"
	"encoding/binary"
	"image/color"
	"log"
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// GL constants for drawing in depth, which the gl package leaves out.
const (
	glDepthTest        = 0x0B71
	glDepthBufferBit   = 0x0100
	glDepthComponent24 = 0x81A6
	glDepthAttachment  = 0x8D00
	glLequal           = 0x0203
	glReadFramebuffer  = 0x8CA8
	glDrawFramebuffer  = 0x8CA9
	glNearest          = 0x2600
	glMaxSamples       = 0x8D57
	glCullFace         = 0x0B44
	glFront            = 0x0404
	glBack             = 0x0405
)

const (
	// sceneSamples is how many samples a scene's pixels take, for
	// smooth edges, where the GPU offers as many, and sceneMost the
	// most device pixels a scene draws at, across or down.
	sceneSamples = 4
	sceneMost    = 4096
	// meshIdle is how long a mesh stays on the GPU after the last frame
	// that drew it, and meshVertexFloats the floats of each of its
	// vertices there: position, normal and colour.
	meshIdle         = 10 * time.Second
	meshVertexFloats = 10
	// glMaxRenderbufferSize and glFramebufferComplete are what the GPU
	// says of its render buffers.
	glMaxRenderbufferSize = 0x84E8
	glFramebufferComplete = 0x8CD5
)

// meshBudget is how many bytes of mesh buffers a window keeps on the
// GPU; past it, the meshes drawn longest ago go first, as when a shape
// that changes makes a new mesh each frame. A test lowers it.
var meshBudget = 128 << 20

// A scene draws by a program of its own, in depth, into a target of its
// own: a multisampled colour and depth buffer, resolved into a texture
// that is then laid into the frame as an image is, so a scene shows
// inside clips, ellipses and tilts like anything else. The program is
// the renderer's own, unlike the shared ones, as it takes the camera
// and the light as uniforms, which belong to the program.
type sceneState struct {
	prog   uint32
	failed bool
	u      struct{ mvp, model, normal, light, lightColor, ambient, eye, tint, shine, mirror int32 }
	// ms draws the scene, with its colour and depth buffers, and out is
	// the texture it resolves into, both outW by outH, of which a scene
	// takes the lower left part its size.
	ms                  uint32
	msColor, msDepth    uint32
	out                 target
	outW, outH, samples int
	// most is the largest side the target may have: sceneMost, or less
	// where the GPU's render buffers are smaller. broken says the GPU
	// would not make the target, which it said once.
	most   int
	broken bool
	// peakW and peakH are the most the scenes since peakSince took, and
	// drawn when one last drew, for the target to shrink or go when the
	// scenes no longer need it.
	peakW, peakH     int
	peakSince, drawn time.Time
	meshes           map[*paint.Mesh]*meshBuffers
	// meshBytes is how much the meshes take on the GPU, and frame counts
	// the frames, for the budget to spare the meshes of the one drawing.
	meshBytes int
	frame     uint64
	// order is scratch: the see-through items of the scene drawing.
	order []seen
	// v holds the values a scene hands its uniforms, here rather than
	// on the stack, where passing them to the GL through its interface
	// would move them to the heap, an allocation for each one each item.
	v sceneValues
}

// sceneValues is what a scene and each of its items hand the program's
// uniforms.
type sceneValues struct {
	mvp, model geom.Mat4
	normal     [9]float32
	tint       [4]float32
	shine      [1]float32
	mirror     [1]float32
	vec3       [3]float32
}

// meshBuffers is a mesh uploaded to the GPU.
type meshBuffers struct {
	vao, vbo, ibo uint32
	n             int32
	used          time.Time
	// frame is the last frame that drew it, and bytes what it takes.
	frame uint64
	bytes int
}

const sceneVS = `
in vec3 a_pos;
in vec3 a_normal;
in vec4 a_color;
uniform mat4 u_mvp;
uniform mat4 u_model;
uniform mat3 u_normal;
out vec3 v_normal;
out vec3 v_world;
out vec4 v_color;

void main() {
	v_normal = u_normal * a_normal;
	v_world = (u_model * vec4(a_pos, 1.0)).xyz;
	v_color = a_color;
	gl_Position = u_mvp * vec4(a_pos, 1.0);
}
`

// sceneFS lights a surface with the even light and the far light's
// diffuse and, for a glossy one, its highlight. A surface seen from
// behind is lit as its front would be. u_mirror is -1 for an item whose
// model mirrors it, which turns its triangles' winding round on the
// screen, so its front faces show as back faces there.
const sceneFS = `
in vec3 v_normal;
in vec3 v_world;
in vec4 v_color;
uniform vec3 u_light;
uniform vec3 u_lightColor;
uniform vec3 u_ambient;
uniform vec3 u_eye;
uniform vec4 u_tint;
uniform float u_shine;
uniform float u_mirror;
out vec4 fragColor;

void main() {
	vec3 n = normalize(v_normal);
	if (gl_FrontFacing != (u_mirror > 0.0)) {
		n = -n;
	}
	vec3 l = -u_light;
	float diff = max(dot(n, l), 0.0);
	vec4 base = v_color * u_tint;
	vec3 col = base.rgb * (u_ambient + u_lightColor * diff);
	if (u_shine > 0.0 && diff > 0.0) {
		vec3 h = normalize(l + normalize(u_eye - v_world));
		col += u_lightColor * pow(max(dot(n, h), 0.0), u_shine) * 0.3;
	}
	fragColor = vec4(min(col, vec3(1.0)) * base.a, base.a);
}
`

// sceneReady builds the scene program on first use, and reports whether it
// can draw.
func (r *Renderer) sceneReady() bool {
	st := &r.scenes
	if st.prog != 0 || st.failed {
		return !st.failed
	}
	header := "#version 150\n"
	if r.isES {
		header = "#version 300 es\nprecision highp float;\n"
	}
	g := r.GL
	vs, err := compile(g, gl.VERTEX_SHADER, header+sceneVS)
	if err != nil {
		st.failed = true
		log.Printf("gunim: render: scene: %v", err)
		return false
	}
	defer g.DeleteShader(vs)
	fs, err := compile(g, gl.FRAGMENT_SHADER, header+sceneFS)
	if err != nil {
		st.failed = true
		log.Printf("gunim: render: scene: %v", err)
		return false
	}
	defer g.DeleteShader(fs)
	p := g.CreateProgram()
	g.AttachShader(p, vs)
	g.AttachShader(p, fs)
	for i, name := range []string{"a_pos", "a_normal", "a_color"} {
		g.BindAttribLocation(p, uint32(i), name)
	}
	g.LinkProgram(p)
	if g.GetProgrami(p, gl.LINK_STATUS) == gl.FALSE {
		log.Printf("gunim: render: scene: link: %s", g.GetProgramInfoLog(p))
		g.DeleteProgram(p)
		st.failed = true
		return false
	}
	st.prog = p
	loc := func(name string) int32 { return g.GetUniformLocation(p, name) }
	st.u.mvp, st.u.model, st.u.normal = loc("u_mvp"), loc("u_model"), loc("u_normal")
	st.u.light, st.u.lightColor, st.u.ambient = loc("u_light"), loc("u_lightColor"), loc("u_ambient")
	st.u.eye, st.u.tint, st.u.shine, st.u.mirror = loc("u_eye"), loc("u_tint"), loc("u_shine"), loc("u_mirror")
	st.samples = min(sceneSamples, g.GetInteger(glMaxSamples))
	st.most = sceneMost
	if n := g.GetInteger(glMaxRenderbufferSize); n > 0 {
		st.most = min(st.most, n)
	}
	st.meshes = map[*paint.Mesh]*meshBuffers{}
	return true
}

// scene draws a scene into its own target and queues it into the
// frame, at its rectangle, as an image.
func (r *Renderer) scene(op *paint.SceneOp) {
	t := op.Transform
	size := op.Rect.Size()
	// The scene draws at the size it shows, its transform's scale
	// included, up to sceneMost device pixels a side.
	sx := float32(math.Hypot(float64(t.A), float64(t.D))) * r.scale
	sy := float32(math.Hypot(float64(t.B), float64(t.E))) * r.scale
	w := int(math.Round(float64(size.W * sx)))
	h := int(math.Round(float64(size.H * sy)))
	if w <= 0 || h <= 0 || !r.sceneReady() {
		return
	}
	st := &r.scenes
	w, h = min(w, st.most), min(h, st.most)
	r.flush()
	g := r.GL
	if !r.fitScene(w, h) {
		return
	}

	g.BindFramebuffer(gl.FRAMEBUFFER, st.ms)
	g.Viewport(0, 0, int32(w), int32(h))
	g.Disable(gl.SCISSOR_TEST)
	g.Disable(gl.BLEND)
	g.Enable(glDepthTest)
	g.DepthFunc(glLequal)
	g.DepthMask(true)
	bg := rgba(op.Scene.Background)
	g.ClearColor(bg[0]*bg[3], bg[1]*bg[3], bg[2]*bg[3], bg[3])
	g.Clear(glColorBufferBit | glDepthBufferBit)
	g.ClearColor(0, 0, 0, 0)
	g.UseProgram(st.prog)

	cam := op.Scene.Camera
	vp := cam.Matrix(size.W / size.H)
	dir, lc, amb := op.Scene.Lighting()
	r.uniform3(st.u.light, dir.X, dir.Y, dir.Z)
	r.uniformRGB(st.u.lightColor, lc)
	r.uniformRGB(st.u.ambient, amb)
	r.uniform3(st.u.eye, cam.Eye.X, cam.Eye.Y, cam.Eye.Z)

	// The solid items first, hiding what they stand in front of; then
	// the see-through ones, furthest first, each blended over what is
	// behind it and hiding nothing, its back faces before its front, so
	// a glass ball shows its far side through its near one.
	now := time.Now()
	st.order = st.order[:0]
	for i, it := range op.Scene.Items {
		if it.Mesh == nil || len(it.Mesh.Indices()) == 0 {
			continue
		}
		if !it.SeeThrough() {
			r.drawItem(it, vp, now)
			continue
		}
		c, _ := it.Mesh.Bounds()
		st.order = append(st.order, seen{i, it.Matrix().Apply(c).Sub(cam.Eye).Len()})
	}
	if len(st.order) > 0 {
		slices.SortStableFunc(st.order, func(a, b seen) int { return cmp.Compare(b.dist, a.dist) })
		g.Enable(gl.BLEND)
		g.BlendFuncSeparate(gl.ONE, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
		g.DepthMask(false)
		g.Enable(glCullFace)
		for _, o := range st.order {
			// The far side first, then the near: its back faces, which a
			// mirrored item's winding shows as front faces.
			it := op.Scene.Items[o.item]
			far, near := uint32(glFront), uint32(glBack)
			if mirrored(it.Matrix()) {
				far, near = near, far
			}
			g.CullFace(far)
			r.drawItem(it, vp, now)
			g.CullFace(near)
			r.drawItem(it, vp, now)
		}
		g.Disable(glCullFace)
		g.DepthMask(true)
		g.Disable(gl.BLEND)
	}

	// Resolve the samples into the texture the frame reads.
	g.BindFramebuffer(glReadFramebuffer, st.ms)
	g.BindFramebuffer(glDrawFramebuffer, st.out.fbo)
	g.BlitFramebuffer(0, 0, int32(w), int32(h), 0, 0, int32(w), int32(h), glColorBufferBit, glNearest)

	g.Disable(glDepthTest)
	g.BindFramebuffer(gl.FRAMEBUFFER, r.fbo(r.depth))
	g.Viewport(0, 0, int32(r.fbW), int32(r.fbH))
	r.applyClip()
	r.Rebind()
	r.bindDraw()

	// The texture's rows run from the bottom, and the scene takes its
	// lower left corner.
	r.uses(st.out.tex)
	uv := geom.Rect{Min: geom.Pt(0, float32(h)/float32(st.outH)), Max: geom.Pt(float32(w)/float32(st.outW), 0)}
	r.quad(corners(op.Rect, uv), op.Transform, r.scale, &look{
		rect: op.Rect, kind: kindImage, color0: [4]float32{0, 0, 0, 1},
	})
	// The next scene draws into the same texture.
	r.flush()
}

// seen is a see-through item of a scene, by its index, and how far its
// middle is from the eye.
type seen struct {
	item int
	dist float32
}

// drawItem draws one item of a scene, through vp, the camera's matrix.
func (r *Renderer) drawItem(it paint.SceneItem, vp geom.Mat4, now time.Time) {
	g, st := r.GL, &r.scenes
	mb := r.meshBuffers(it.Mesh, now)
	v := &st.v
	v.model = it.Matrix()
	v.mvp = vp.Mul(v.model)
	v.normal = v.model.NormalMatrix()
	v.tint = rgba(it.Color())
	v.shine[0] = it.Shine
	v.mirror[0] = 1
	if mirrored(v.model) {
		v.mirror[0] = -1
	}
	g.UniformMatrix4fv(st.u.mvp, v.mvp[:])
	g.UniformMatrix4fv(st.u.model, v.model[:])
	g.UniformMatrix3fv(st.u.normal, v.normal[:])
	g.Uniform4fv(st.u.tint, v.tint[:])
	g.Uniform1fv(st.u.shine, v.shine[:])
	g.Uniform1fv(st.u.mirror, v.mirror[:])
	g.BindVertexArray(mb.vao)
	g.DrawElements(gl.TRIANGLES, mb.n, gl.UNSIGNED_INT, 0)
}

// uniform3 sets the scene program's vec3 uniform at loc.
func (r *Renderer) uniform3(loc int32, x, y, z float32) {
	v := &r.scenes.v.vec3
	*v = [3]float32{x, y, z}
	r.GL.Uniform3fv(loc, v[:])
}

// uniformRGB sets the scene program's vec3 uniform at loc to c,
// premultiplied.
func (r *Renderer) uniformRGB(loc int32, c color.NRGBA) {
	v := rgba(c)
	r.uniform3(loc, v[0]*v[3], v[1]*v[3], v[2]*v[3])
}

// mirrored reports whether m mirrors what it moves, as a scale by a
// negative amount on one axis does: its upper left 3 by 3 turns space
// inside out.
func mirrored(m geom.Mat4) bool {
	det := m[0]*(m[5]*m[10]-m[9]*m[6]) - m[4]*(m[1]*m[10]-m[9]*m[2]) + m[8]*(m[1]*m[6]-m[5]*m[2])
	return det < 0
}

// fitScene makes the scene's target at least w by h, growing it to the
// size asked for, so a scene that grows as it animates in reallocates
// now and then, never every frame.
func (r *Renderer) fitScene(w, h int) bool {
	st := &r.scenes
	if st.broken {
		return false
	}
	now := time.Now()
	st.drawn = now
	if st.peakSince.IsZero() {
		st.peakSince = now
	}
	st.peakW, st.peakH = max(st.peakW, w), max(st.peakH, h)
	if st.ms != 0 && w <= st.outW && h <= st.outH {
		return true
	}
	g := r.GL
	w, h = max(w, st.outW), max(h, st.outH)
	if st.ms == 0 {
		st.ms = g.CreateFramebuffer()
		st.msColor = g.CreateRenderbuffer()
		st.msDepth = g.CreateRenderbuffer()
		st.out.tex = g.CreateTexture()
		st.out.fbo = g.CreateFramebuffer()
	}
	st.outW, st.outH = w, h
	g.BindRenderbuffer(gl.RENDERBUFFER, st.msColor)
	g.RenderbufferStorageMultisample(gl.RENDERBUFFER, int32(st.samples), glRGBA8, int32(w), int32(h))
	g.BindRenderbuffer(gl.RENDERBUFFER, st.msDepth)
	g.RenderbufferStorageMultisample(gl.RENDERBUFFER, int32(st.samples), glDepthComponent24, int32(w), int32(h))
	g.BindRenderbuffer(gl.RENDERBUFFER, 0)
	g.BindFramebuffer(gl.FRAMEBUFFER, st.ms)
	g.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.RENDERBUFFER, st.msColor)
	g.FramebufferRenderbuffer(gl.FRAMEBUFFER, glDepthAttachment, gl.RENDERBUFFER, st.msDepth)

	g.ActiveTexture(glTexture1)
	g.BindTexture(gl.TEXTURE_2D, st.out.tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.TexImage2D(gl.TEXTURE_2D, 0, glRGBA8, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE, nil)
	g.ActiveTexture(gl.TEXTURE0)
	g.BindFramebuffer(gl.FRAMEBUFFER, st.out.fbo)
	g.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, st.out.tex, 0)
	out := g.CheckFramebufferStatus(gl.FRAMEBUFFER)
	g.BindFramebuffer(gl.FRAMEBUFFER, st.ms)
	ms := g.CheckFramebufferStatus(gl.FRAMEBUFFER)
	if out != glFramebufferComplete || ms != glFramebufferComplete {
		log.Printf("gunim: render: scene: the GPU would not make a %d by %d target (%#x, %#x); scenes are not drawn", w, h, ms, out)
		r.freeSceneTarget()
		st.broken = true
		g.BindFramebuffer(gl.FRAMEBUFFER, r.fbo(r.depth))
		return false
	}
	return true
}

// freeSceneTarget lets go of the scene's target, which the next scene
// makes again at its own size.
func (r *Renderer) freeSceneTarget() {
	st := &r.scenes
	if st.ms == 0 {
		return
	}
	g := r.GL
	g.DeleteFramebuffer(st.ms)
	g.DeleteRenderbuffer(st.msColor)
	g.DeleteRenderbuffer(st.msDepth)
	g.DeleteFramebuffer(st.out.fbo)
	g.DeleteTexture(st.out.tex)
	st.ms, st.msColor, st.msDepth, st.out, st.outW, st.outH = 0, 0, 0, target{}, 0, 0
}

// meshBuffers returns m's buffers on the GPU, uploading them on first
// use.
func (r *Renderer) meshBuffers(m *paint.Mesh, now time.Time) *meshBuffers {
	st := &r.scenes
	if mb, ok := st.meshes[m]; ok {
		mb.used, mb.frame = now, st.frame
		return mb
	}
	g := r.GL
	mb := &meshBuffers{vao: g.CreateVertexArray(), vbo: g.CreateBuffer(), ibo: g.CreateBuffer(), used: now, frame: st.frame}
	g.BindVertexArray(mb.vao)
	g.BindBuffer(gl.ARRAY_BUFFER, mb.vbo)
	verts := m.Vertices()
	data := make([]byte, 0, len(verts)*meshVertexFloats*4)
	put := func(vs ...float32) {
		for _, v := range vs {
			data = binary.LittleEndian.AppendUint32(data, math.Float32bits(v))
		}
	}
	for _, v := range verts {
		c := rgba(v.Color)
		put(v.Pos.X, v.Pos.Y, v.Pos.Z, v.Normal.X, v.Normal.Y, v.Normal.Z, c[0], c[1], c[2], c[3])
	}
	g.BufferInit(gl.ARRAY_BUFFER, len(data), glStaticDraw)
	g.BufferSubData(gl.ARRAY_BUFFER, 0, data)
	stride := int32(meshVertexFloats * 4)
	for i, a := range [3]struct {
		size int32
		off  int
	}{{3, 0}, {3, 12}, {4, 24}} {
		g.EnableVertexAttribArray(uint32(i))
		g.VertexAttribPointer(uint32(i), a.size, gl.FLOAT, false, stride, a.off)
	}
	idx := m.Indices()
	ib := make([]byte, 0, len(idx)*4)
	for _, i := range idx {
		ib = binary.LittleEndian.AppendUint32(ib, i)
	}
	g.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, mb.ibo)
	g.BufferInit(gl.ELEMENT_ARRAY_BUFFER, len(ib), glStaticDraw)
	g.BufferSubData(gl.ELEMENT_ARRAY_BUFFER, 0, ib)
	mb.n = int32(len(idx))
	mb.bytes = len(data) + len(ib)
	st.meshBytes += mb.bytes
	st.meshes[m] = mb
	return mb
}

// evictMeshes lets go, at the end of a frame, of the meshes the window
// has stopped drawing, and of those drawn longest ago while the meshes
// fill the budget, sparing the frame's own. A scene's target goes once
// no scene has drawn for a while, and shrinks when the scenes of a
// while took less than half of it.
func (r *Renderer) evictMeshes() {
	st := &r.scenes
	now := time.Now()
	for m, mb := range st.meshes {
		if now.Sub(mb.used) > meshIdle {
			r.dropMesh(m, mb)
		}
	}
	if st.meshBytes > meshBudget {
		type aged struct {
			m  *paint.Mesh
			mb *meshBuffers
		}
		var old []aged
		for m, mb := range st.meshes {
			if mb.frame != st.frame {
				old = append(old, aged{m, mb})
			}
		}
		slices.SortFunc(old, func(a, b aged) int { return a.mb.used.Compare(b.mb.used) })
		for _, o := range old {
			if st.meshBytes <= meshBudget {
				break
			}
			r.dropMesh(o.m, o.mb)
		}
	}
	st.frame++
	if st.ms != 0 && now.Sub(st.peakSince) > meshIdle {
		if now.Sub(st.drawn) > meshIdle || 2*st.peakW*st.peakH < st.outW*st.outH {
			r.freeSceneTarget()
		}
		st.peakW, st.peakH, st.peakSince = 0, 0, now
	}
}

func (r *Renderer) dropMesh(m *paint.Mesh, mb *meshBuffers) {
	g := r.GL
	g.DeleteVertexArray(mb.vao)
	g.DeleteBuffer(mb.vbo)
	g.DeleteBuffer(mb.ibo)
	r.scenes.meshBytes -= mb.bytes
	delete(r.scenes.meshes, m)
}

// releaseScenes frees what scenes kept on the GPU.
func (r *Renderer) releaseScenes() {
	st := &r.scenes
	for m, mb := range st.meshes {
		r.dropMesh(m, mb)
	}
	g := r.GL
	r.freeSceneTarget()
	if st.prog != 0 {
		g.DeleteProgram(st.prog)
	}
	*st = sceneState{}
}
