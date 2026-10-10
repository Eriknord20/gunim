# Code from Ebitengine

These packages are copied from [Ebitengine](https://github.com/hajimehoshi/ebiten)
v2.10.2, under the Apache License 2.0 in `LICENSE-ebitengine`:

| Here | Upstream |
| --- | --- |
| `glfw` | `internal/glfw`, a pure-Go port of GLFW |
| `gl` | `internal/graphicsdriver/opengl/gl`, OpenGL bindings through purego |
| `cocoa`, `exeicon`, `microsoftgdk`, `winver`, `windowsystem` | the `internal` packages of the same names, which `glfw` and `gl` need |

Go keeps another module from importing `internal` packages, so gunim
carries a copy. Tests, the js and console variants, `gen.go`, and the
cgo-only Xlib check are left out. Import paths are rewritten to gunim's.

gunim's changes are marked `gunim change` in the code:

- `glfw` tracks the current GL context per OS thread, as C GLFW does
  (`current_context.go`, `thread_*.go`). Ebitengine kept one global,
  which suited its one render thread. gunim renders each window on a
  thread of its own. macOS and the BSDs still share one slot.
- `gl` loads desktop OpenGL before OpenGL ES on Linux and the BSDs
  (`procaddr_linbsd.go`), so the context is created through GLX without
  asking `glxinfo` whether GLX can make an ES context.
- `glfw` gives the Win32 port the input-method API of the X11 one
  (`win32_ime_windows.go`): compositions through IMM32 while the
  application takes text, typed text through the text input callback,
  `ResetInputContext`, and, Windows only, `SetInputMethodEnabled` and
  `SetInputMethodCaret`. `windowProc` routes the `WM_IME_*` composition
  messages there, and the platform window state holds its fields.
- `glfw` implements the Win32 clipboard (`win32_clipboard_windows.go`),
  as C GLFW does. The Ebitengine port left it as a stub that panicked.
- `glfw` reports the position a Win32 button message carries as a
  cursor move before the button, when it differs from the last one
  (`windowProc` in `win32_window_windows.go`). A pointer moved by
  `SetCursorPos`, remote input or automation can deliver its button
  message first, and the click would land where the pointer was.
- `gl` binds `glGenerateMipmap` (`interface.go`, `default_purego.go`,
  `debug.go`), for image textures drawn smaller than their size, and
  `glClearColor`, through `purego.RegisterFunc` since it takes floats.
- `glfw` can drag files out of a window to other programs:
  `StartDragOut` and `CancelDragOut` (`dragout.go`). On X11 the window
  becomes an XDND source (`x11_dragout_linbsd.go`, with hooks in
  `x11_window_linbsd.go` for pointer moves, the button's release, the
  target's replies and XdndSelection requests). On Win32 it runs OLE's
  `DoDragDrop` over a shell data object made from the files' item IDs
  (`win32_dragout_windows.go`). Cocoa returns an error.
- `glfw` reads the modifier keys held now, for files dropped from another
  program while the window lacks the keyboard: `HeldModifiers`
  (`heldmods.go`), from `GetAsyncKeyState` on Win32 and `XQueryPointer`
  on X11. Cocoa reports none.
- `glfw` says where the pointer is on the screen, over any window or
  none: `PointerOnScreen` (`pointer.go`), from `GetCursorPos` on Win32
  and `XQueryPointer` on the root window on X11. Cocoa reports nothing.
- `glfw` has a `Popup` window hint, for menus, lists and tooltips: a
  window that never takes focus. On X11 it is override-redirect, so the
  window manager leaves it where it is put, and typed
  `_NET_WM_WINDOW_TYPE_POPUP_MENU` (`x11_window_linbsd.go`). On Win32
  it is a `WS_POPUP` tool window with `WS_EX_NOACTIVATE` and
  `WS_EX_TOPMOST`, and a click answers `WM_MOUSEACTIVATE` with
  `MA_NOACTIVATE` (`win32_window_windows.go`). Cocoa ignores it.
- `gl` makes the calls that pass only numbers through `call`
  (`default_purego.go`), which hands `purego.SyscallN` a list of
  arguments the context keeps. `SyscallN` is `go:uintptrescapes`, so
  the list each call built for it went to the heap: an allocation for
  every GL call. The `Uniform*v` calls copy up to 16 values into the
  context's scratch and pass its address the same way; longer ones, and
  the calls that pass other memory, call `SyscallN` as before.

To take a newer Ebitengine, copy the same files again and reapply those
changes.
