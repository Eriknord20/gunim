//go:build js && wasm

package gl

import (
	"errors"
	"syscall/js"
	"unsafe"
)

// webContext is a [Context] over a browser's WebGL2 context, which is
// OpenGL ES 3.0 with JavaScript objects for names. gunim change.
//
// WebGL hands out objects where GL hands out numbers, so the context
// keeps each object in a table and gives its index out as the name;
// 0 stays the null object. Data crosses into JavaScript through one
// scratch ArrayBuffer with a view of each element type over it, passed
// with WebGL2's offset and length arguments so a call makes no new
// JavaScript objects.
type webContext struct {
	gl       js.Value
	objects  []js.Value
	free     []uint32
	uniforms []js.Value

	// WebGL refuses a draw while a texture the draw framebuffer renders
	// into is bound to any unit the program could sample, where desktop
	// GL leaves a binding nothing samples alone. So the context tracks
	// the 2D texture on each unit, the draw framebuffer and each
	// framebuffer's colour texture, and unbinds the target from every
	// unit before a draw.
	unit     uint32
	bound    [32]uint32
	drawFB   uint32
	fbColour map[uint32]uint32

	scratch js.Value // ArrayBuffer
	size    int
	u8      js.Value
	u16     js.Value
	i32     js.Value
	f32     js.Value
}

// NewWebGLContext wraps gl, a WebGL2RenderingContext.
func NewWebGLContext(gl js.Value) (Context, error) {
	if gl.IsNull() || gl.IsUndefined() {
		return nil, errors.New("gl: no WebGL2 context")
	}
	c := &webContext{gl: gl, objects: []js.Value{js.Null()}, uniforms: []js.Value{js.Null()}, fbColour: map[uint32]uint32{}}
	c.grow(1 << 16)
	return c, nil
}

func (c *webContext) grow(n int) {
	if n <= c.size {
		return
	}
	size := max(n, 2*c.size)
	size = (size + 7) &^ 7
	c.scratch = js.Global().Get("ArrayBuffer").New(size)
	c.size = size
	c.u8 = js.Global().Get("Uint8Array").New(c.scratch)
	c.u16 = js.Global().Get("Uint16Array").New(c.scratch)
	c.i32 = js.Global().Get("Int32Array").New(c.scratch)
	c.f32 = js.Global().Get("Float32Array").New(c.scratch)
}

// put copies b into the scratch buffer.
func (c *webContext) put(b []byte) {
	c.grow(len(b))
	js.CopyBytesToJS(c.u8, b)
}

func bytesOf[T any](s []T) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*int(unsafe.Sizeof(s[0])))
}

func (c *webContext) add(v js.Value) uint32 {
	if v.IsNull() || v.IsUndefined() {
		return 0
	}
	if n := len(c.free); n > 0 {
		id := c.free[n-1]
		c.free = c.free[:n-1]
		c.objects[id] = v
		return id
	}
	c.objects = append(c.objects, v)
	return uint32(len(c.objects) - 1)
}

func (c *webContext) obj(id uint32) js.Value {
	if id == 0 || int(id) >= len(c.objects) {
		return js.Null()
	}
	return c.objects[id]
}

func (c *webContext) drop(id uint32) js.Value {
	v := c.obj(id)
	if id != 0 && int(id) < len(c.objects) {
		c.objects[id] = js.Null()
		c.free = append(c.free, id)
	}
	return v
}

func (c *webContext) loc(l int32) js.Value {
	if l <= 0 || int(l) >= len(c.uniforms) {
		return js.Null()
	}
	return c.uniforms[l]
}

func (c *webContext) LoadFunctions() error { return nil }
func (c *webContext) IsES() bool           { return true }

const (
	texture0        = 0x84C0
	framebufferAll  = 0x8D40
	drawFramebuffer = 0x8CA9
	colour0         = 0x8CE0
)

func (c *webContext) ActiveTexture(texture uint32) {
	c.unit = texture - texture0
	c.gl.Call("activeTexture", texture)
}

// unloop unbinds the draw framebuffer's colour texture from every unit.
func (c *webContext) unloop() {
	t := c.fbColour[c.drawFB]
	if t == 0 {
		return
	}
	for u, b := range c.bound {
		if b != t {
			continue
		}
		c.gl.Call("activeTexture", texture0+u)
		c.gl.Call("bindTexture", TEXTURE_2D, js.Null())
		c.bound[u] = 0
		c.gl.Call("activeTexture", texture0+c.unit)
	}
}
func (c *webContext) AttachShader(program, shader uint32) {
	c.gl.Call("attachShader", c.obj(program), c.obj(shader))
}
func (c *webContext) BindAttribLocation(program, index uint32, name string) {
	c.gl.Call("bindAttribLocation", c.obj(program), index, name)
}
func (c *webContext) BindBuffer(target, buffer uint32) {
	c.gl.Call("bindBuffer", target, c.obj(buffer))
}
func (c *webContext) BindFramebuffer(target, framebuffer uint32) {
	if target == framebufferAll || target == drawFramebuffer {
		c.drawFB = framebuffer
	}
	c.gl.Call("bindFramebuffer", target, c.obj(framebuffer))
}
func (c *webContext) BindRenderbuffer(target, renderbuffer uint32) {
	c.gl.Call("bindRenderbuffer", target, c.obj(renderbuffer))
}
func (c *webContext) BindTexture(target, texture uint32) {
	if target == TEXTURE_2D && int(c.unit) < len(c.bound) {
		c.bound[c.unit] = texture
	}
	c.gl.Call("bindTexture", target, c.obj(texture))
}
func (c *webContext) BindVertexArray(array uint32) { c.gl.Call("bindVertexArray", c.obj(array)) }
func (c *webContext) BlitFramebuffer(srcX0, srcY0, srcX1, srcY1, dstX0, dstY0, dstX1, dstY1 int32, mask, filter uint32) {
	c.gl.Call("blitFramebuffer", srcX0, srcY0, srcX1, srcY1, dstX0, dstY0, dstX1, dstY1, mask, filter)
}
func (c *webContext) CullFace(mode uint32) { c.gl.Call("cullFace", mode) }
func (c *webContext) DepthFunc(fn uint32)  { c.gl.Call("depthFunc", fn) }
func (c *webContext) DepthMask(flag bool)  { c.gl.Call("depthMask", flag) }
func (c *webContext) RenderbufferStorageMultisample(target uint32, samples int32, internalFormat uint32, width, height int32) {
	c.gl.Call("renderbufferStorageMultisample", target, samples, internalFormat, width, height)
}
func (c *webContext) BlendEquationSeparate(modeRGB, modeAlpha uint32) {
	c.gl.Call("blendEquationSeparate", modeRGB, modeAlpha)
}
func (c *webContext) BlendFuncSeparate(srcRGB, dstRGB, srcAlpha, dstAlpha uint32) {
	c.gl.Call("blendFuncSeparate", srcRGB, dstRGB, srcAlpha, dstAlpha)
}
func (c *webContext) BufferInit(target uint32, size int, usage uint32) {
	c.gl.Call("bufferData", target, size, usage)
}

// BufferData passes the length, as the scratch buffer is usually longer than data.
func (c *webContext) BufferData(target uint32, data []byte, usage uint32) {
	c.put(data)
	c.gl.Call("bufferData", target, c.u8, usage, 0, len(data))
}
func (c *webContext) BufferSubData(target uint32, offset int, data []byte) {
	if len(data) == 0 {
		return
	}
	c.put(data)
	c.gl.Call("bufferSubData", target, offset, c.u8, 0, len(data))
}
func (c *webContext) CheckFramebufferStatus(target uint32) uint32 {
	return uint32(c.gl.Call("checkFramebufferStatus", target).Int())
}
func (c *webContext) Clear(mask uint32) { c.gl.Call("clear", mask) }
func (c *webContext) ClearColor(red, green, blue, alpha float32) {
	c.gl.Call("clearColor", red, green, blue, alpha)
}
func (c *webContext) ColorMask(red, green, blue, alpha bool) {
	c.gl.Call("colorMask", red, green, blue, alpha)
}
func (c *webContext) CompileShader(shader uint32) { c.gl.Call("compileShader", c.obj(shader)) }
func (c *webContext) CreateBuffer() uint32        { return c.add(c.gl.Call("createBuffer")) }
func (c *webContext) CreateFramebuffer() uint32   { return c.add(c.gl.Call("createFramebuffer")) }
func (c *webContext) CreateProgram() uint32       { return c.add(c.gl.Call("createProgram")) }
func (c *webContext) CreateRenderbuffer() uint32  { return c.add(c.gl.Call("createRenderbuffer")) }
func (c *webContext) CreateShader(xtype uint32) uint32 {
	return c.add(c.gl.Call("createShader", xtype))
}
func (c *webContext) CreateTexture() uint32     { return c.add(c.gl.Call("createTexture")) }
func (c *webContext) CreateVertexArray() uint32 { return c.add(c.gl.Call("createVertexArray")) }
func (c *webContext) DeleteBuffer(buffer uint32) {
	c.gl.Call("deleteBuffer", c.drop(buffer))
}
func (c *webContext) DeleteFramebuffer(framebuffer uint32) {
	delete(c.fbColour, framebuffer)
	c.gl.Call("deleteFramebuffer", c.drop(framebuffer))
}
func (c *webContext) DeleteProgram(program uint32) {
	c.gl.Call("deleteProgram", c.drop(program))
}
func (c *webContext) DeleteRenderbuffer(renderbuffer uint32) {
	c.gl.Call("deleteRenderbuffer", c.drop(renderbuffer))
}
func (c *webContext) DeleteShader(shader uint32) { c.gl.Call("deleteShader", c.drop(shader)) }
func (c *webContext) DeleteTexture(texture uint32) {
	for u, b := range c.bound {
		if b == texture {
			c.bound[u] = 0
		}
	}
	c.gl.Call("deleteTexture", c.drop(texture))
}
func (c *webContext) DeleteVertexArray(array uint32) {
	c.gl.Call("deleteVertexArray", c.drop(array))
}
func (c *webContext) Disable(cap uint32) { c.gl.Call("disable", cap) }
func (c *webContext) DisableVertexAttribArray(index uint32) {
	c.gl.Call("disableVertexAttribArray", index)
}
func (c *webContext) DrawElements(mode uint32, count int32, xtype uint32, offset int) {
	c.unloop()
	c.gl.Call("drawElements", mode, count, xtype, offset)
}
func (c *webContext) DrawElementsInstanced(mode uint32, count int32, xtype uint32, offset int, instances int32) {
	c.unloop()
	c.gl.Call("drawElementsInstanced", mode, count, xtype, offset, instances)
}
func (c *webContext) Enable(cap uint32) { c.gl.Call("enable", cap) }
func (c *webContext) EnableVertexAttribArray(index uint32) {
	c.gl.Call("enableVertexAttribArray", index)
}
func (c *webContext) Finish() { c.gl.Call("finish") }
func (c *webContext) Flush()  { c.gl.Call("flush") }
func (c *webContext) FramebufferRenderbuffer(target, attachment, renderbuffertarget, renderbuffer uint32) {
	if attachment == colour0 && (target == framebufferAll || target == drawFramebuffer) {
		delete(c.fbColour, c.drawFB)
	}
	c.gl.Call("framebufferRenderbuffer", target, attachment, renderbuffertarget, c.obj(renderbuffer))
}
func (c *webContext) GenerateMipmap(target uint32) { c.gl.Call("generateMipmap", target) }
func (c *webContext) FramebufferTexture2D(target, attachment, textarget, texture uint32, level int32) {
	if attachment == colour0 && (target == framebufferAll || target == drawFramebuffer) {
		c.fbColour[c.drawFB] = texture
	}
	c.gl.Call("framebufferTexture2D", target, attachment, textarget, c.obj(texture), level)
}
func (c *webContext) GetError() uint32 { return uint32(c.gl.Call("getError").Int()) }
func (c *webContext) GetExtension(name string) any {
	v := c.gl.Call("getExtension", name)
	if v.IsNull() {
		return nil
	}
	return v
}

// number reads a parameter WebGL may answer with a number, a boolean or
// null.
func number(v js.Value) int {
	switch v.Type() {
	case js.TypeNumber:
		return v.Int()
	case js.TypeBoolean:
		if v.Bool() {
			return 1
		}
	}
	return 0
}

func (c *webContext) GetInteger(pname uint32) int {
	return number(c.gl.Call("getParameter", pname))
}
func (c *webContext) GetProgramInfoLog(program uint32) string {
	return c.gl.Call("getProgramInfoLog", c.obj(program)).String()
}
func (c *webContext) GetProgrami(program, pname uint32) int {
	return number(c.gl.Call("getProgramParameter", c.obj(program), pname))
}
func (c *webContext) GetShaderInfoLog(shader uint32) string {
	return c.gl.Call("getShaderInfoLog", c.obj(shader)).String()
}
func (c *webContext) GetShaderi(shader, pname uint32) int {
	return number(c.gl.Call("getShaderParameter", c.obj(shader), pname))
}
func (c *webContext) GetUniformLocation(program uint32, name string) int32 {
	v := c.gl.Call("getUniformLocation", c.obj(program), name)
	if v.IsNull() {
		return -1
	}
	c.uniforms = append(c.uniforms, v)
	return int32(len(c.uniforms) - 1)
}
func (c *webContext) IsProgram(program uint32) bool {
	return c.gl.Call("isProgram", c.obj(program)).Bool()
}
func (c *webContext) LinkProgram(program uint32) { c.gl.Call("linkProgram", c.obj(program)) }
func (c *webContext) PixelStorei(pname uint32, param int32) {
	c.gl.Call("pixelStorei", pname, param)
}
func (c *webContext) ReadPixels(dst []byte, x, y, width, height int32, format, xtype uint32) {
	c.grow(len(dst))
	c.gl.Call("readPixels", x, y, width, height, format, xtype, c.u8, 0)
	js.CopyBytesToGo(dst, c.u8.Call("subarray", 0, len(dst)))
}
func (c *webContext) RenderbufferStorage(target, internalFormat uint32, width, height int32) {
	c.gl.Call("renderbufferStorage", target, internalFormat, width, height)
}
func (c *webContext) Scissor(x, y, width, height int32) { c.gl.Call("scissor", x, y, width, height) }
func (c *webContext) ShaderSource(shader uint32, xstring string) {
	c.gl.Call("shaderSource", c.obj(shader), xstring)
}
func (c *webContext) StencilFunc(func_ uint32, ref int32, mask uint32) {
	c.gl.Call("stencilFunc", func_, ref, mask)
}
func (c *webContext) StencilOpSeparate(face, sfail, dpfail, dppass uint32) {
	c.gl.Call("stencilOpSeparate", face, sfail, dpfail, dppass)
}

const (
	halfFloat     = 0x140B
	unsignedShort = 0x1403
)

// pixelView puts pixels in the scratch buffer and returns the view of
// it WebGL takes for xtype.
func (c *webContext) pixelView(pixels []byte, xtype uint32) js.Value {
	c.put(pixels)
	switch xtype {
	case halfFloat, unsignedShort:
		return c.u16
	case FLOAT:
		return c.f32
	}
	return c.u8
}

func (c *webContext) TexImage2D(target uint32, level, internalformat, width, height int32, format, xtype uint32, pixels []byte) {
	if pixels == nil {
		c.gl.Call("texImage2D", target, level, internalformat, width, height, 0, format, xtype, js.Null())
		return
	}
	c.gl.Call("texImage2D", target, level, internalformat, width, height, 0, format, xtype, c.pixelView(pixels, xtype), 0)
}
func (c *webContext) TexParameteri(target, pname uint32, param int32) {
	c.gl.Call("texParameteri", target, pname, param)
}
func (c *webContext) TexSubImage2D(target uint32, level, xoffset, yoffset, width, height int32, format, xtype uint32, pixels []byte) {
	c.gl.Call("texSubImage2D", target, level, xoffset, yoffset, width, height, format, xtype, c.pixelView(pixels, xtype), 0)
}

func (c *webContext) uniformf(fn string, location int32, value []float32) {
	if len(value) == 0 {
		return
	}
	c.put(bytesOf(value))
	c.gl.Call(fn, c.loc(location), c.f32, 0, len(value))
}
func (c *webContext) uniformi(fn string, location int32, value []int32) {
	if len(value) == 0 {
		return
	}
	c.put(bytesOf(value))
	c.gl.Call(fn, c.loc(location), c.i32, 0, len(value))
}
func (c *webContext) matrix(fn string, location int32, value []float32) {
	if len(value) == 0 {
		return
	}
	c.put(bytesOf(value))
	c.gl.Call(fn, c.loc(location), false, c.f32, 0, len(value))
}

func (c *webContext) Uniform1fv(location int32, value []float32) {
	c.uniformf("uniform1fv", location, value)
}
func (c *webContext) Uniform1i(location, v0 int32) { c.gl.Call("uniform1i", c.loc(location), v0) }
func (c *webContext) Uniform1iv(location int32, value []int32) {
	c.uniformi("uniform1iv", location, value)
}
func (c *webContext) Uniform2fv(location int32, value []float32) {
	c.uniformf("uniform2fv", location, value)
}
func (c *webContext) Uniform2iv(location int32, value []int32) {
	c.uniformi("uniform2iv", location, value)
}
func (c *webContext) Uniform3fv(location int32, value []float32) {
	c.uniformf("uniform3fv", location, value)
}
func (c *webContext) Uniform3iv(location int32, value []int32) {
	c.uniformi("uniform3iv", location, value)
}
func (c *webContext) Uniform4fv(location int32, value []float32) {
	c.uniformf("uniform4fv", location, value)
}
func (c *webContext) Uniform4iv(location int32, value []int32) {
	c.uniformi("uniform4iv", location, value)
}
func (c *webContext) UniformMatrix2fv(location int32, value []float32) {
	c.matrix("uniformMatrix2fv", location, value)
}
func (c *webContext) UniformMatrix3fv(location int32, value []float32) {
	c.matrix("uniformMatrix3fv", location, value)
}
func (c *webContext) UniformMatrix4fv(location int32, value []float32) {
	c.matrix("uniformMatrix4fv", location, value)
}
func (c *webContext) UseProgram(program uint32) { c.gl.Call("useProgram", c.obj(program)) }
func (c *webContext) VertexAttribDivisor(index, divisor uint32) {
	c.gl.Call("vertexAttribDivisor", index, divisor)
}
func (c *webContext) VertexAttribPointer(index uint32, size int32, xtype uint32, normalized bool, stride int32, offset int) {
	c.gl.Call("vertexAttribPointer", index, size, xtype, normalized, stride, offset)
}
func (c *webContext) Viewport(x, y, width, height int32) { c.gl.Call("viewport", x, y, width, height) }
