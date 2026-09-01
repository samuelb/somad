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

static OSStatus somaAACNewConverter(Float64 sampleRate, UInt32 channels, AudioConverterRef *out) {
	AudioStreamBasicDescription src = {0};
	src.mSampleRate = sampleRate;
	src.mFormatID = kAudioFormatMPEG4AAC;
	src.mFramesPerPacket = 1024;
	src.mChannelsPerFrame = channels;

	AudioStreamBasicDescription dst = {0};
	dst.mSampleRate = sampleRate;
	dst.mFormatID = kAudioFormatLinearPCM;
	dst.mFormatFlags = kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked;
	dst.mBitsPerChannel = 16;
	dst.mChannelsPerFrame = channels;
	dst.mFramesPerPacket = 1;
	dst.mBytesPerFrame = 2 * channels;
	dst.mBytesPerPacket = 2 * channels;

	return AudioConverterNew(&src, &dst, out);
}

// somaAACDecode feeds one AAC packet through the converter. On entry
// ioFrames is the output buffer's capacity in PCM frames; on return it is
// the number of frames produced. The converter running out of input is the
// expected way a call ends, not an error.
static OSStatus somaAACDecode(AudioConverterRef conv, const UInt8 *pkt, UInt32 pktLen, void *outBuf, UInt32 *ioFrames, UInt32 channels) {
	somaAACInput in = { pkt, pktLen, {0}, 0 };
	AudioBufferList out;
	out.mNumberBuffers = 1;
	out.mBuffers[0].mNumberChannels = channels;
	out.mBuffers[0].mDataByteSize = (*ioFrames) * 2 * channels;
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
	"fmt"
	"io"
	"runtime"
	"unsafe"
)

// aacSupported reports whether this build can decode AAC streams; macOS
// decodes through the system AudioToolbox framework.
const aacSupported = true

// aacFrameCapacity is the per-packet output capacity in PCM frames. AAC-LC
// yields 1024 frames per packet; double that leaves headroom for
// converter-internal buffering.
const aacFrameCapacity = 2048

// aacDecoder decodes an ADTS AAC stream to 16-bit little-endian stereo PCM
// through the system AudioToolbox converter. Like the MP3 decoder, mono
// input comes out duplicated onto both channels.
type aacDecoder struct {
	frames     *adtsReader
	conv       C.AudioConverterRef
	sampleRate int
	channels   int
	first      *adtsFrame // parsed by the constructor, decoded by the first Read
	scratch    []byte     // converter output, native channel count
	stereo     []byte     // mono-to-stereo expansion buffer (mono streams only)
	pcm        []byte     // decoded bytes not yet handed to Read
	err        error
}

// newAACDecoder blocks until the stream's first ADTS frame has arrived (so
// the caller gets synchronous connect semantics, like mp3.NewDecoder), then
// prepares the system converter for the stream's parameters.
func newAACDecoder(r io.Reader) (pcmDecoder, error) {
	frames := newADTSReader(r)
	first, err := frames.next()
	if err != nil {
		return nil, fmt.Errorf("reading first ADTS frame: %w", err)
	}
	d := &aacDecoder{
		frames:     frames,
		sampleRate: first.sampleRate,
		channels:   first.channels,
		first:      &first,
		scratch:    make([]byte, aacFrameCapacity*2*first.channels),
	}
	if first.channels == 1 {
		d.stereo = make([]byte, aacFrameCapacity*4)
	}
	// nolint below: gocritic's dupSubExpr trips on the pointer checks cgo
	// generates for &d.conv, not on anything in this source line.
	if st := C.somaAACNewConverter(C.Float64(d.sampleRate), C.UInt32(d.channels), &d.conv); st != 0 { //nolint:gocritic
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

// SampleRate returns the stream's sample rate in Hz.
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
	if d.first != nil {
		f, d.first = *d.first, nil
	} else {
		var err error
		if f, err = d.frames.next(); err != nil {
			return err
		}
	}
	if f.sampleRate != d.sampleRate || f.channels != d.channels {
		return fmt.Errorf("AAC stream parameters changed mid-stream (%d Hz, %d ch -> %d Hz, %d ch)",
			d.sampleRate, d.channels, f.sampleRate, f.channels)
	}

	frames := C.UInt32(aacFrameCapacity)
	st := C.somaAACDecode(d.conv,
		(*C.UInt8)(unsafe.Pointer(&f.payload[0])), C.UInt32(len(f.payload)),
		unsafe.Pointer(&d.scratch[0]), &frames, C.UInt32(d.channels))
	if st != 0 { // noErr
		return fmt.Errorf("decoding AAC frame: OSStatus %d", int32(st))
	}

	out := d.scratch[:int(frames)*2*d.channels]
	if d.channels == 1 {
		out = d.expandMono(out)
	}
	// Reusing scratch/stereo across calls is safe: decodeNext only runs
	// once the previous output has been fully consumed.
	d.pcm = out
	return nil
}

// expandMono duplicates each 16-bit mono sample onto both stereo channels.
func (d *aacDecoder) expandMono(src []byte) []byte {
	dst := d.stereo[:len(src)*2]
	for i := 0; i+1 < len(src); i += 2 {
		dst[2*i] = src[i]
		dst[2*i+1] = src[i+1]
		dst[2*i+2] = src[i]
		dst[2*i+3] = src[i+1]
	}
	return dst
}
