// Package vst3 hosts VST3 plugins, as a mastering chain does Ozone and
// a DAW does a synth: it loads a plugin's module, makes its effect or
// its instrument, runs sound through it in realtime or offline, plays
// notes on it, keeps and restores its state, and shows its editor in a
// window of the system's. It is pure Go: the plugin's interfaces are
// tables of functions, called as they are laid out, and the host's are
// written here the same way.
//
// A plugin also runs behind package plug's interfaces, as
// [Module.Describe] and [Module.Register] describe it: its parameters
// from 0 to 1, as VST3 carries them, its buses beyond the main ones as
// aux ports, and its notes as VST3's events. Each note's group plays on
// a channel of its own; a bend is the note's own where the plugin takes
// VST3's tuning note expression, and else moves its group's pitch
// bend, and the sustain pedal and the mod wheel move the parameters the
// plugin maps those MIDI controllers to.
//
// # ARA
//
// A plugin that speaks Celemony's ARA 2 (Audio Random Access), as
// Melodyne does, sees each recording of a song whole, beyond the sound
// passing through it. The host describes its song to the plugin as a
// document of five kinds of object:
//
//   - a musical context: the tempo map, the bar signatures, the keys
//     and the chords, which the plugin reads from the host;
//   - region sequences: the tracks, each in a musical context;
//   - audio sources: the recordings, each known by a persistent ID,
//     whose samples the plugin reads whenever it likes;
//   - audio modifications: the plugin's edits of a source, each known
//     by a persistent ID, several to a source;
//   - playback regions: a stretch of a modification, placed on the
//     song's timeline in seconds, on a region sequence.
//
// [Class.ARA] says a class has an ARA factory, the "ARA Main Factory
// Class" of the same name in its module, and [Module.ARAFactory]
// returns it, initialized once for the program. [ARAFactory.NewDocument]
// makes a document controller, the plugin's half of a document, for an
// [ARAHost], the host's half: it reads a source's samples for the
// plugin, on threads of the plugin's own, several at once, and never
// the audio thread's.
//
// The document lives on the plugins' thread, the one that runs their
// controllers and editors, as ARA asks of one thread for the whole
// model; each method of [Document] runs its work there and waits.
// [Document.Edit] makes a set of changes as one, between ARA's
// beginEditing and endEditing: the [Edit] it hands its function adds,
// updates and takes out objects, and restores them from archives.
// [Document.Store] writes an archive of the whole document, or of the
// objects a [StoreFilter] names, so a host may keep each recording's
// edits apart. [Document.Poll] has the plugin tell what changed since
// it last asked, ARA's notifyModelUpdates, and returns it as [Update]s:
// an analysis's progress, the content of a source, a modification or a
// region changed, and the plugin's requests of the host's transport
// (play, stop, a new position, a cycle). A host polls every few tens of
// milliseconds, outside an edit.
//
// [Document.NewPlugin] makes a plugin of the factory's class bound to
// the document in the [Roles] given, before it first processes, as ARA
// asks; [Plugin.ARA] returns its [Extension]. A playback renderer
// renders the regions [Extension.SetRegions] hands it, at the times its
// process calls give, in place of its input: setting them stops and
// restarts the plugin's processing, so ProcessBlock must not run
// meanwhile. An editor renderer adds the sound of editing, as a note
// played as it is dragged, to its input, and takes its regions while it
// processes. An editor view shows the plugin's editor ([Plugin.OpenEditor])
// over the whole document, showing what [Extension.Select] picks.
//
// [FakeARA] is an ARA plugin written in Go, for the tests of a host
// with none to hand: it analyses, renders, archives and takes edits as
// a plugin does, and lists the rules of ARA's a host broke.
//
// ARA's structures are packed to the byte on x86, and naturally aligned
// on ARM; the package writes and reads them field by field, as each
// platform lays them out. The definitions follow Celemony's ARA_API
// headers, Apache-2.0-licensed.
//
// The definitions follow Steinberg's VST3 interfaces, MIT-licensed, in
// pluginterfaces.
package vst3

import (
	"encoding/binary"
	"unsafe"
)

// uid is a VST3 class or interface ID, as the platform lays it out.
type uid [16]byte

// iid returns the ID written as four numbers, as the interfaces' headers
// write them: on Windows in the order of a COM GUID, elsewhere as the
// numbers read.
func iid(l1, l2, l3, l4 uint32) uid {
	var u uid
	if comCompatible {
		binary.LittleEndian.PutUint32(u[0:], l1)
		binary.LittleEndian.PutUint16(u[4:], uint16(l2>>16))
		binary.LittleEndian.PutUint16(u[6:], uint16(l2))
	} else {
		binary.BigEndian.PutUint32(u[0:], l1)
		binary.BigEndian.PutUint32(u[4:], l2)
	}
	binary.BigEndian.PutUint32(u[8:], l3)
	binary.BigEndian.PutUint32(u[12:], l4)
	return u
}

// The interfaces' IDs.
var (
	iidFUnknown          = iid(0x00000000, 0x00000000, 0xC0000000, 0x00000046)
	iidPluginFactory2    = iid(0x0007B650, 0xF24B4C0B, 0xA464EDB9, 0xF00B2ABB)
	iidPluginFactory3    = iid(0x4555A2AB, 0xC1234E57, 0x9B122910, 0x36878931)
	iidComponent         = iid(0xE831FF31, 0xF2D54301, 0x928EBBEE, 0x25697802)
	iidAudioProcessor    = iid(0x42043F99, 0xB7DA453C, 0xA569E79D, 0x9AAEC33D)
	iidEditController    = iid(0xDCD7BBE3, 0x7742448D, 0xA874AACC, 0x979C759E)
	iidConnectionPoint   = iid(0x70A4156F, 0x6E6E4026, 0x989148BF, 0xAA60D8D1)
	iidComponentHandler  = iid(0x93A0BEA3, 0x0BD045DB, 0x8E890B0C, 0xC1E46AC6)
	iidComponentHandler2 = iid(0xF040B4B3, 0xA36045EC, 0xABCDC045, 0xB4D5A2CC)
	iidMidiMapping       = iid(0xDF0FF9F7, 0x49B74669, 0xB63AB732, 0x7ADBF5E5)
	iidNoteExpression    = iid(0xB7F8F859, 0x41234872, 0x91169581, 0x4F3721A3)
	iidHostApplication   = iid(0x58E595CC, 0xDB2D4969, 0x8B6AAF8C, 0x36A664E5)
	iidBStream           = iid(0xC3BF6EA2, 0x30994752, 0x9B6BF990, 0x1EE33E9B)
	iidMessage           = iid(0x936F033B, 0xC6C047DB, 0xBB0882F8, 0x13C1E613)
	iidAttributeList     = iid(0x1E5F0AEB, 0xCC7F4533, 0xA2544011, 0x38AD5EE4)
	iidParamValueQueue   = iid(0x01263A18, 0xED074F6F, 0x98C9D356, 0x4686F9BA)
	iidParameterChanges  = iid(0xA4779663, 0x0BB64A56, 0xB44384A8, 0x466FEB9D)
	iidEventList         = iid(0x3A2C4214, 0x346349FE, 0xB2C4F397, 0xB9695A44)
	iidPlugFrame         = iid(0x367FAF01, 0xAFA94693, 0x8D4DA2A0, 0xED0882A3)
	iidRunLoop           = iid(0x18C35366, 0x97764F1A, 0x9C5B8385, 0x7A871389)
)

// The result codes every platform shares.
const (
	resultOK    = 0
	resultFalse = 1
)

// The methods' places in their tables: FUnknown's three first, then each
// interface's own after those of the one it extends.
const (
	mQueryInterface = 0
	mAddRef         = 1
	mRelease        = 2

	// IPluginBase.
	mInitialize = 3
	mTerminate  = 4

	// IPluginFactory, 2 and 3.
	mGetFactoryInfo = 3
	mCountClasses   = 4
	mGetClassInfo   = 5
	mCreateInstance = 6
	mGetClassInfo2  = 7
	mGetClassInfoW  = 8
	mSetHostContext = 9

	// IComponent, after IPluginBase.
	mGetControllerClassID = 5
	mSetIoMode            = 6
	mGetBusCount          = 7
	mGetBusInfo           = 8
	mGetRoutingInfo       = 9
	mActivateBus          = 10
	mSetActive            = 11
	mSetState             = 12
	mGetState             = 13

	// IAudioProcessor.
	mSetBusArrangements   = 3
	mGetBusArrangement    = 4
	mCanProcessSampleSize = 5
	mGetLatencySamples    = 6
	mSetupProcessing      = 7
	mSetProcessing        = 8
	mProcess              = 9
	mGetTailSamples       = 10

	// IEditController, after IPluginBase.
	mSetComponentState      = 5
	mCtrlSetState           = 6
	mCtrlGetState           = 7
	mGetParameterCount      = 8
	mGetParameterInfo       = 9
	mGetParamStringByValue  = 10
	mGetParamValueByString  = 11
	mNormalizedParamToPlain = 12
	mPlainParamToNormalized = 13
	mGetParamNormalized     = 14
	mSetParamNormalized     = 15
	mSetComponentHandler    = 16
	mCreateView             = 17

	// IConnectionPoint.
	mConnect    = 3
	mDisconnect = 4
	mNotify     = 5

	// IPlugView.
	mIsPlatformTypeSupported = 3
	mAttached                = 4
	mRemoved                 = 5
	mOnWheel                 = 6
	mOnKeyDown               = 7
	mOnKeyUp                 = 8
	mGetSize                 = 9
	mOnSize                  = 10
	mOnFocus                 = 11
	mSetFrame                = 12
	mCanResize               = 13
	mCheckSizeConstraint     = 14

	// IMidiMapping.
	mGetMidiControllerAssignment = 3

	// INoteExpressionController.
	mGetNoteExpressionCount = 3
	mGetNoteExpressionInfo  = 4
)

// call calls method m of the object obj, with integer arguments, and
// returns what it returns.
//
//go:uintptrescapes
func call(obj uintptr, m int, args ...uintptr) uintptr {
	// The arguments, obj first, in an array of the call's own, as a
	// slice made for them would be made anew each call.
	var all [maxCallArgs]uintptr
	all[0] = obj
	n := 1 + copy(all[1:], args)
	return callN(method(obj, m), all[:n])
}

// maxCallArgs is the most arguments a method takes, obj counted.
const maxCallArgs = 8

// result is a method's result code.
func result(r uintptr) int32 { return int32(uint32(r)) }

// method returns the function at place m of obj's table.
func method(obj uintptr, m int) uintptr {
	vtbl := *ptr[unsafe.Pointer](obj)
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(m)*unsafe.Sizeof(uintptr(0))))
}

// query asks obj for the interface id, and returns it, or 0.
func query(obj uintptr, id uid) uintptr {
	if obj == 0 {
		return 0
	}
	var out uintptr
	if result(call(obj, mQueryInterface, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&out)))) != resultOK {
		return 0
	}
	return out
}

// release lets go of obj.
func release(obj uintptr) {
	if obj != 0 {
		call(obj, mRelease)
	}
}

// cstring returns the C string at the start of b.
func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// ptr is the pointer p a call passed as a number: memory of the
// plugin's, which Go neither moves nor frees.
func ptr[T any](p uintptr) *T {
	return *(**T)(unsafe.Pointer(&p))
}
