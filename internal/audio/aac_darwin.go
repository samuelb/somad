//go:build darwin

package audio

/*
#cgo LDFLAGS: -framework AudioToolbox

#include <AudioToolbox/AudioToolbox.h>

// somaAACNoMoreData is the private status the input proc returns once its
// single packet is consumed, telling the converter to stop asking for input
// within this FillComplexBuffer call. Any non-zero status works; a private
// four-char code keeps it distinguishable from real AudioToolbox errors.
enum { somaAACNoMoreData = 'snmd' };

// somaAACInput hands exactly one AAC packet to the converter per
// FillComplexBuffer call.
typedef struct {
	const UInt8 *data;
	UInt32 len;
	AudioStreamPacketDescription desc;
	int consumed;
} somaAACInput;

static OSStatus somaAACInputProc(AudioConverterRef conv, UInt32 *ioNumberDataPackets, AudioBufferList *ioData, AudioStreamPacketDescription **outDesc, void *inUserData) {
	somaAACInput *in = (somaAACInput *)inUserData;
	if (in->consumed) {
		*ioNumberDataPackets = 0;
		return somaAACNoMoreData;
	}
	in->consumed = 1;
	in->desc.mStartOffset = 0;
	in->desc.mVariableFramesInPacket = 0;
	in->desc.mDataByteSize = in->len;
	ioData->mNumberBuffers = 1;
	ioData->mBuffers[0].mNumberChannels = 0;
	ioData->mBuffers[0].mDataByteSize = in->len;
	ioData->mBuffers[0].mData = (void *)in->data;
	if (outDesc) {
		*outDesc = &in->desc;
	}
	*ioNumberDataPackets = 1;
	return noErr;
}

// somaAACFormat is what the system ADTS parser makes of a stream's first
// frames: the best format it can decode (AAC-LC, HE-AAC, or HE-AAC v2) and
// the magic cookie describing it to the converter.
typedef struct {
	AudioStreamBasicDescription asbd;
	UInt8 cookie[256];
	UInt32 cookieLen;
	int found;
} somaAACFormat;

static void somaAACProbeProperty(void *inClientData, AudioFileStreamID stream, AudioFileStreamPropertyID id, AudioFileStreamPropertyFlags *ioFlags) {
	somaAACFormat *f = (somaAACFormat *)inClientData;
	UInt32 size = 0;
	Boolean writable;
	if (id == kAudioFileStreamProperty_MagicCookieData) {
		if (AudioFileStreamGetPropertyInfo(stream, id, &size, &writable) != noErr || size > sizeof(f->cookie)) {
			return;
		}
		if (AudioFileStreamGetProperty(stream, id, &size, f->cookie) == noErr) {
			f->cookieLen = size;
		}
	} else if (id == kAudioFileStreamProperty_FormatList) {
		// The list runs from the richest decoding the stream supports (HE-AAC
		// v2, HE-AAC) down to its plain AAC-LC core.
		AudioFormatListItem items[8];
		if (AudioFileStreamGetPropertyInfo(stream, id, &size, &writable) != noErr) {
			return;
		}
		if (size > sizeof(items)) {
			size = sizeof(items);
		}
		if (AudioFileStreamGetProperty(stream, id, &size, items) != noErr || size < sizeof(items[0])) {
			return;
		}
		UInt32 index = 0, indexSize = sizeof(index);
		if (AudioFormatGetProperty(kAudioFormatProperty_FirstPlayableFormatFromList, size, items, &indexSize, &index) != noErr ||
			index >= size / sizeof(items[0])) {
			return;
		}
		f->asbd = items[index].mASBD;
		f->found = 1;
	}
}

static void somaAACProbePackets(void *inClientData, UInt32 inNumberBytes, UInt32 inNumberPackets, const void *inInputData, AudioStreamPacketDescription *inPacketDescriptions) {
}

// somaAACProbe runs ADTS bytes through the system stream parser, which,
// unlike an ADTS header, reveals SBR (HE-AAC) and parametric stereo
// (HE-AAC v2): both are signalled only inside the payload. f->found stays 0
// until the parser has seen enough of the stream.
static OSStatus somaAACProbe(const UInt8 *data, UInt32 len, somaAACFormat *f) {
	AudioFileStreamID stream;
	OSStatus st = AudioFileStreamOpen(f, somaAACProbeProperty, somaAACProbePackets, kAudioFileAAC_ADTSType, &stream);
	if (st != noErr) {
		return st;
	}
	st = AudioFileStreamParseBytes(stream, len, data, 0);
	AudioFileStreamClose(stream);
	return st;
}

// somaAACNewConverter prepares a converter from the probed format to 16-bit
// interleaved stereo PCM at the format's output rate. Mono input is
// duplicated onto both channels by the converter.
static OSStatus somaAACNewConverter(const somaAACFormat *f, AudioConverterRef *out) {
	AudioStreamBasicDescription dst = {0};
	dst.mSampleRate = f->asbd.mSampleRate;
	dst.mFormatID = kAudioFormatLinearPCM;
	dst.mFormatFlags = kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked;
	dst.mBitsPerChannel = 16;
	dst.mChannelsPerFrame = 2;
	dst.mFramesPerPacket = 1;
	dst.mBytesPerFrame = 4;
	dst.mBytesPerPacket = 4;

	OSStatus st = AudioConverterNew(&f->asbd, &dst, out);
	if (st != noErr || f->cookieLen == 0) {
		return st;
	}
	st = AudioConverterSetProperty(*out, kAudioConverterDecompressionMagicCookie, f->cookieLen, f->cookie);
	if (st != noErr) {
		AudioConverterDispose(*out);
		*out = NULL;
	}
	return st;
}

// somaAACDecode feeds one AAC packet through the converter. On entry
// ioFrames is the output buffer's capacity in stereo PCM frames; on return
// it is the number of frames produced. The converter running out of input
// is the expected way a call ends, not an error.
static OSStatus somaAACDecode(AudioConverterRef conv, const UInt8 *pkt, UInt32 pktLen, void *outBuf, UInt32 *ioFrames) {
	somaAACInput in = { pkt, pktLen, {0}, 0 };
	AudioBufferList out;
	out.mNumberBuffers = 1;
	out.mBuffers[0].mNumberChannels = 2;
	out.mBuffers[0].mDataByteSize = (*ioFrames) * 4;
	out.mBuffers[0].mData = outBuf;
	OSStatus st = AudioConverterFillComplexBuffer(conv, somaAACInputProc, &in, ioFrames, &out, NULL);
	if (st == somaAACNoMoreData) {
		return noErr;
	}
	return st;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"unsafe"
)

// aacSupported reports whether this build can decode AAC streams; macOS
// decodes through the system AudioToolbox framework.
const aacSupported = true

// aacFrameCapacity is the per-packet output capacity in PCM frames. AAC-LC
// yields 1024 frames per packet and HE-AAC 2048 (SBR doubles the rate);
// double that leaves headroom for converter-internal buffering.
const aacFrameCapacity = 4096

// aacProbeFrames bounds how many frames newAACDecoder feeds the system
// parser before giving up on identifying the stream. The parser needs about
// 128 bytes, which one frame of a real stream exceeds; an encoder's
// priming frames can be shorter.
const aacProbeFrames = 8

// aacDecoder decodes an ADTS AAC stream — AAC-LC, HE-AAC (SBR), or HE-AAC
// v2 (parametric stereo) — to 16-bit little-endian stereo PCM through the
// system AudioToolbox converter. Like the MP3 decoder, mono input comes out
// duplicated onto both channels.
type aacDecoder struct {
	frames     *adtsReader
	conv       C.AudioConverterRef
	sampleRate int // output rate: twice the ADTS header's rate with SBR
	// coreRate and coreChannels are the first frame's ADTS header values,
	// which every later frame must repeat.
	coreRate     int
	coreChannels int
	queued       []adtsFrame // read while probing, decoded by the first Reads
	scratch      []byte      // converter output
	pcm          []byte      // decoded bytes not yet handed to Read
	err          error
}

// newAACDecoder blocks until the stream's first ADTS frames have arrived
// (so the caller gets synchronous connect semantics, like mp3.NewDecoder),
// identifies the stream's format, and prepares the system converter for it.
//
// The ADTS header only describes the AAC-LC core. SomaFM's HE-AAC streams
// (the "aacp" playlists, and many "aac" ones too) carry SBR and parametric
// stereo in the payload, at twice the header's sample rate; decoding them
// as the header says would silently drop everything above half the output
// rate and any stereo image. So the first frames go through the system's
// own ADTS parser, which inspects the payload and reports the full format
// and a magic cookie for the converter.
func newAACDecoder(r io.Reader) (pcmDecoder, error) {
	frames := newADTSReader(r)
	first, err := frames.next()
	if err != nil {
		return nil, fmt.Errorf("reading first ADTS frame: %w", err)
	}
	d := &aacDecoder{
		frames:       frames,
		coreRate:     first.sampleRate,
		coreChannels: first.channels,
		queued:       []adtsFrame{first},
		scratch:      make([]byte, aacFrameCapacity*4),
	}

	format, err := d.probe()
	if err != nil {
		return nil, err
	}
	d.sampleRate = int(format.asbd.mSampleRate)
	// nolint below: gocritic's dupSubExpr trips on the pointer checks cgo
	// generates for &d.conv, not on anything in this source line.
	if st := C.somaAACNewConverter(format, &d.conv); st != 0 { //nolint:gocritic
		return nil, fmt.Errorf("creating the system AAC converter: OSStatus %d", int32(st))
	}
	// The playback pipeline drops decoders rather than closing them (the
	// MP3 decoder has nothing to close), so the converter is released with
	// the decoder object.
	runtime.SetFinalizer(d, func(d *aacDecoder) { C.AudioConverterDispose(d.conv) })

	// Decode the first frame now, not lazily on the first Read, so Play
	// fails synchronously when the converter rejects the stream — parity
	// with mp3.NewDecoder, which decodes its first frame on construction.
	// This is best-effort: AudioToolbox conceals corrupt payloads instead
	// of erroring (verified empirically), so the load-bearing validation
	// for "this is really AAC" is the strict ADTS header parsing above — a
	// stream that is not ADTS at all never reaches this point.
	if err := d.decodeNext(); err != nil {
		return nil, fmt.Errorf("decoding first AAC frame: %w", err)
	}
	return d, nil
}

// probe identifies the stream's format from its first frames, reading
// further frames into d.queued until the system parser has seen enough.
func (d *aacDecoder) probe() (*C.somaAACFormat, error) {
	format := new(C.somaAACFormat)
	var head []byte
	for i := 0; ; i++ {
		head = append(head, d.queued[i].raw...)
		st := C.somaAACProbe((*C.UInt8)(unsafe.Pointer(&head[0])), C.UInt32(len(head)), format)
		if st != 0 {
			return nil, fmt.Errorf("identifying the AAC stream format: OSStatus %d", int32(st))
		}
		if format.found != 0 {
			return format, nil
		}
		if i+1 == aacProbeFrames {
			return nil, errors.New("identifying the AAC stream format: no format found")
		}
		f, err := d.frames.next()
		if err != nil {
			return nil, fmt.Errorf("reading ADTS frame: %w", err)
		}
		d.queued = append(d.queued, f)
	}
}

// SampleRate returns the stream's output sample rate in Hz.
func (d *aacDecoder) SampleRate() int { return d.sampleRate }

// Read returns decoded stereo PCM, decoding further ADTS frames as needed.
func (d *aacDecoder) Read(p []byte) (int, error) {
	for len(d.pcm) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if err := d.decodeNext(); err != nil {
			d.err = err
		}
	}
	n := copy(p, d.pcm)
	d.pcm = d.pcm[n:]
	return n, nil
}

// decodeNext decodes one ADTS frame into d.pcm. The converter may buffer
// and yield zero frames (start-up priming); the Read loop simply continues.
func (d *aacDecoder) decodeNext() error {
	var f adtsFrame
	if len(d.queued) > 0 {
		f, d.queued = d.queued[0], d.queued[1:]
	} else {
		var err error
		if f, err = d.frames.next(); err != nil {
			return err
		}
	}
	if f.sampleRate != d.coreRate || f.channels != d.coreChannels {
		return fmt.Errorf("AAC stream parameters changed mid-stream (%d Hz, %d ch -> %d Hz, %d ch)",
			d.coreRate, d.coreChannels, f.sampleRate, f.channels)
	}

	frames := C.UInt32(aacFrameCapacity)
	st := C.somaAACDecode(d.conv,
		(*C.UInt8)(unsafe.Pointer(&f.payload[0])), C.UInt32(len(f.payload)),
		unsafe.Pointer(&d.scratch[0]), &frames)
	if st != 0 { // noErr
		return fmt.Errorf("decoding AAC frame: OSStatus %d", int32(st))
	}

	// Reusing scratch across calls is safe: decodeNext only runs once the
	// previous output has been fully consumed.
	d.pcm = d.scratch[:int(frames)*4]
	return nil
}
