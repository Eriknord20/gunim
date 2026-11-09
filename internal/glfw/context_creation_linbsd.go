// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build freebsd || linux || netbsd

package glfw

// creatingContext holds [ContextCreation] for as long as it takes to make a
// context, if the context shares. One that shares nothing joins no list
// another thread walks. It returns what lets go.
func creatingContext(shares bool) (done func()) {
	if !shares {
		return func() {}
	}
	ContextCreation.Lock()
	return ContextCreation.Unlock
}
