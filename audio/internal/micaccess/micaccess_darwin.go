//go:build darwin && !ios

// Package micaccess asks macOS whether the application may record, as
// the system keeps the user's answer (TCC), and asks the user where
// they have not answered yet.
package micaccess

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The application's leave to record, as the system keeps it: what
// AVCaptureDevice's authorizationStatusForMediaType: returns.
const (
	authNotDetermined = 0
	authRestricted    = 1
	authDenied        = 2
)

var (
	authOnce sync.Once
	// avCaptureDevice is the class, and avMediaTypeAudio the media type
	// asked of it; authErr says why AVFoundation would not load them.
	avCaptureDevice  objc.ID
	avMediaTypeAudio objc.ID
	authErr          error
)

// Check reports whether the system lets the application record: denied
// is true where it lets it record nothing, as Core Audio would then
// hand on silence. Where it has not been asked yet, Check asks, and the
// system shows its question as the input opens: what comes in is silent
// until it is answered. An application bundle without
// NSMicrophoneUsageDescription in its Info.plist is ended by the system
// as it asks. err says why Check could not tell.
func Check() (denied bool, err error) {
	authOnce.Do(func() {
		av, err := purego.Dlopen("/System/Library/Frameworks/AVFoundation.framework/AVFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			authErr = fmt.Errorf("loading AVFoundation, to ask whether the application may record: %w", err)
			return
		}
		sym, err := purego.Dlsym(av, "AVMediaTypeAudio")
		if err != nil {
			authErr = fmt.Errorf("AVFoundation has no AVMediaTypeAudio: %w", err)
			return
		}
		if sym != 0 {
			avMediaTypeAudio = **(**objc.ID)(unsafe.Pointer(&sym))
		}
		avCaptureDevice = objc.ID(objc.GetClass("AVCaptureDevice"))
		if avCaptureDevice == 0 || avMediaTypeAudio == 0 {
			authErr = errors.New("AVFoundation has no AVCaptureDevice or AVMediaTypeAudio, to ask " +
				"whether the application may record")
		}
	})
	if authErr != nil {
		return false, authErr
	}
	switch objc.Send[int](avCaptureDevice, objc.RegisterName("authorizationStatusForMediaType:"), avMediaTypeAudio) {
	case authRestricted, authDenied:
		return true, nil
	case authNotDetermined:
		ask()
	}
	return false, nil
}

var asked atomic.Bool

// ask asks the system for leave to record, once, and answers nothing:
// a recording made before the answer is silent.
func ask() {
	if asked.Swap(true) {
		return
	}
	answered := objc.NewBlock(func(objc.Block, bool) {})
	avCaptureDevice.Send(objc.RegisterName("requestAccessForMediaType:completionHandler:"), avMediaTypeAudio, answered)
}
