// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build darwin || freebsd || linux || netbsd || windows

package glfw

import "sync"

// ContextCreation keeps a new context's creation apart from the GL calls
// that make, fill or delete a buffer in the share group it joins. A render
// thread holds it for reading around those calls; GLX and EGL hold it for
// writing while they make a context that shares.
//
// gunim change: Mesa since 25.3, with a threaded driver such as Intel's
// iris or AMD's radeonsi, walks the share group's contexts as it lets a
// buffer's store go, and reads each one's st. A new context joins that list
// in _mesa_initialize_context, before st_create_context_priv sets its st, so
// a window opening could crash any window drawing with a read of address
// 0x10. Still so on Mesa's main branch at the time of writing.
//
// Only Mesa's platforms, X11 and Wayland through GLX and EGL, ever write
// here. Elsewhere the read lock is never contested. A context leaving the
// group needs no lock: Mesa takes it off the list before freeing its st.
//
// A reader must not make a context while it holds this, or wait for the
// thread that does. With Mesa's glthread on, as it is by default on radeonsi,
// a GL call only queues the work, so a reader waits for that queue before it
// lets go: glGetError does, as a call that returns a value.
var ContextCreation sync.RWMutex
