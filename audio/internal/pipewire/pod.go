package pipewire

import "encoding/binary"

// SPA's numbers for a format, from spa/utils/type.h, spa/param/param.h,
// spa/param/format.h and spa/param/audio/raw.h.
const (
	podID     = 3  // SPA_TYPE_Id
	podInt    = 4  // SPA_TYPE_Int
	podArray  = 13 // SPA_TYPE_Array
	podObject = 15 // SPA_TYPE_Object

	objectFormat    = 0x40003 // SPA_TYPE_OBJECT_Format
	paramEnumFormat = 3       // SPA_PARAM_EnumFormat

	formatMediaType     = 1       // SPA_FORMAT_mediaType
	formatMediaSubtype  = 2       // SPA_FORMAT_mediaSubtype
	formatAudioFormat   = 0x10001 // SPA_FORMAT_AUDIO_format
	formatAudioRate     = 0x10003 // SPA_FORMAT_AUDIO_rate
	formatAudioChannels = 0x10004 // SPA_FORMAT_AUDIO_channels
	formatAudioPosition = 0x10005 // SPA_FORMAT_AUDIO_position

	mediaTypeAudio   = 1      // SPA_MEDIA_TYPE_audio
	mediaSubtypeRaw  = 1      // SPA_MEDIA_SUBTYPE_raw
	audioFormatF32LE = 0x11b  // SPA_AUDIO_FORMAT_F32_LE
	channelFL        = 3      // SPA_AUDIO_CHANNEL_FL
	channelFR        = 4      // SPA_AUDIO_CHANNEL_FR
	channelAux0      = 0x1000 // SPA_AUDIO_CHANNEL_AUX0
)

// formatPod returns the format a stream offers, as an SPA POD (plain
// old data) object: raw audio of 32-bit little-endian floats,
// interleaved, at rate, its channels as positions name them. It is what
// spa_format_audio_raw_build makes, an inline function of SPA's
// headers that purego cannot call, byte for byte.
//
// A POD is a header of two 32-bit words, its body's size and its type,
// then the body, padded to 8 bytes. An object's body is its type and
// its id, then its properties: each a key, flags, and a POD.
func formatPod(rate int, positions []uint32) []byte {
	var b []byte
	word := func(v uint32) { b = binary.LittleEndian.AppendUint32(b, v) }
	// prop adds a property whose value is a POD of type typ, its body
	// the words given, padded to 8 bytes.
	prop := func(key, typ uint32, body ...uint32) {
		word(key)
		word(0)
		word(uint32(4 * len(body)))
		word(typ)
		for _, v := range body {
			word(v)
		}
		if len(body)%2 == 1 {
			word(0)
		}
	}
	word(0) // the object's size, set below
	word(podObject)
	word(objectFormat)
	word(paramEnumFormat)
	prop(formatMediaType, podID, mediaTypeAudio)
	prop(formatMediaSubtype, podID, mediaSubtypeRaw)
	prop(formatAudioFormat, podID, audioFormatF32LE)
	prop(formatAudioRate, podInt, uint32(rate))
	prop(formatAudioChannels, podInt, uint32(len(positions)))
	// An array's body is its children's header, then their bodies
	// alone, each of the header's size.
	prop(formatAudioPosition, podArray, append([]uint32{4, podID}, positions...)...)
	binary.LittleEndian.PutUint32(b, uint32(len(b)-8))
	return b
}
