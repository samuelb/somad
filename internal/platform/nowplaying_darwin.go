//go:build darwin

package platform

// The Objective-C side of mpris_darwin.go lives in this file's preamble
// rather than in a .m file: cgo links libobjc once more for every package
// that has one, and the vendored systray already does, which makes the
// linker warn about the duplicate on every build. No file with an //export
// may define functions in its preamble, hence a file of its own.

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework CoreGraphics -framework Foundation -framework MediaPlayer

// macOS Now Playing (ADR-0039): the entry in Control Center and on the lock
// screen, and the remote commands behind the media keys, headset buttons
// and the controls shown there.
//
// MediaPlayer delivers the commands on the main thread's run loop, and a
// process whose main thread runs none never becomes the Now Playing app at
// all. Everything here therefore happens in blocks on the main queue: the
// tray's NSApplication loop runs them, or somaRunMainLoop does.

#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>
#import <MediaPlayer/MediaPlayer.h>

// The remote commands handed to somaNowPlayingCommand.
enum {
	somaCommandPlay = 1,
	somaCommandPause,
	somaCommandTogglePlayPause,
	somaCommandStop,
	somaCommandNext,
	somaCommandPrevious,
};

// Implemented in Go (mpris_darwin.go), and called on the main thread.
extern void somaNowPlayingCommand(int command);

// CGSessionCopyCurrentDictionary is safe to call from any context (a
// daemon, an SSH session) and returns NULL when there is no window server
// session, so it never itself pulls in AppKit.
static int somaHasGUISession(void) {
	CFDictionaryRef session = CGSessionCopyCurrentDictionary();
	if (session == NULL) {
		return 0;
	}
	CFRelease(session);
	return 1;
}

// The registered handlers, as (command, target) pairs; nil until the first
// update and after a clear. Main queue only.
static NSMutableArray<NSArray *> *handlers;

static void handle(MPRemoteCommand *command, int code) {
	command.enabled = YES;
	id target = [command addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		somaNowPlayingCommand(code);
		return MPRemoteCommandHandlerStatusSuccess;
	}];
	[handlers addObject:@[command, target]];
}

// Main queue only.
static void registerCommands(void) {
	if (handlers != nil) {
		return;
	}
	handlers = [NSMutableArray array];
	MPRemoteCommandCenter *center = [MPRemoteCommandCenter sharedCommandCenter];
	handle(center.playCommand, somaCommandPlay);
	handle(center.pauseCommand, somaCommandPause);
	handle(center.togglePlayPauseCommand, somaCommandTogglePlayPause);
	handle(center.stopCommand, somaCommandStop);
	handle(center.nextTrackCommand, somaCommandNext);
	handle(center.previousTrackCommand, somaCommandPrevious);
}

static NSString *string(const char *utf8) {
	NSString *s = utf8 != NULL ? [NSString stringWithUTF8String:utf8] : nil;
	return s != nil ? s : @"";
}

static void somaNowPlayingUpdate(const char *title, const char *artist, const char *station, int playing) {
	NSMutableDictionary *info = [NSMutableDictionary dictionary];
	info[MPMediaItemPropertyTitle] = string(title);
	info[MPMediaItemPropertyAlbumTitle] = string(station);
	NSString *by = string(artist);
	if (by.length > 0) {
		info[MPMediaItemPropertyArtist] = by;
	}
	info[MPMediaItemPropertyMediaType] = @(MPMediaTypeAnyAudio);
	info[MPNowPlayingInfoPropertyIsLiveStream] = @YES;
	info[MPNowPlayingInfoPropertyPlaybackRate] = playing ? @1.0 : @0.0;
	dispatch_async(dispatch_get_main_queue(), ^{
		registerCommands();
		MPNowPlayingInfoCenter *center = [MPNowPlayingInfoCenter defaultCenter];
		center.nowPlayingInfo = info;
		// macOS has no audio session to tell it who plays: this is what
		// makes the process the Now Playing app, and keeps it that while
		// paused, until another app plays.
		center.playbackState = playing ? MPNowPlayingPlaybackStatePlaying : MPNowPlayingPlaybackStatePaused;
	});
}

static void somaNowPlayingClear(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (handlers == nil) {
			return;
		}
		for (NSArray *pair in handlers) {
			[(MPRemoteCommand *)pair[0] removeTarget:pair[1]];
		}
		handlers = nil;
		MPNowPlayingInfoCenter *center = [MPNowPlayingInfoCenter defaultCenter];
		center.nowPlayingInfo = nil;
		center.playbackState = MPNowPlayingPlaybackStateStopped;
	});
}

static void keepRunning(CFRunLoopTimerRef timer, void *info) {
}

static void somaRunMainLoop(void) {
	// A run loop with nothing to wait for returns at once. This timer
	// never comes due and keeps it running until it is stopped.
	CFRunLoopTimerRef keep = CFRunLoopTimerCreate(kCFAllocatorDefault, CFAbsoluteTimeGetCurrent() + 1.0e10, 1.0e10, 0, 0, keepRunning, NULL);
	CFRunLoopAddTimer(CFRunLoopGetCurrent(), keep, kCFRunLoopCommonModes);
	CFRunLoopRun();
	CFRunLoopRemoveTimer(CFRunLoopGetCurrent(), keep, kCFRunLoopCommonModes);
	CFRelease(keep);
}

static void somaStopMainLoop(void) {
	// As a block on the queue that loop serves, so a stop that comes
	// before the loop runs still ends it, as soon as it does.
	dispatch_async(dispatch_get_main_queue(), ^{
		CFRunLoopStop(CFRunLoopGetMain());
	});
}
*/
import "C"

import "unsafe"

// The remote commands macOS sends, as the preamble numbers them.
const (
	commandPlay            = int(C.somaCommandPlay)
	commandPause           = int(C.somaCommandPause)
	commandTogglePlayPause = int(C.somaCommandTogglePlayPause)
	commandStop            = int(C.somaCommandStop)
	commandNext            = int(C.somaCommandNext)
	commandPrevious        = int(C.somaCommandPrevious)
)

// HasGUISession reports whether the process runs in a login session with a
// window server. Without one (a headless Mac, an SSH session) there is no
// menu bar for the tray and no Now Playing to register with.
func HasGUISession() bool {
	return C.somaHasGUISession() != 0
}

// systemNowPlaying is the system's Now Playing itself.
type systemNowPlaying struct{}

// update shows what plays, or what is paused, and takes the remote
// commands for it from the first call on. It returns at once: the work is
// done on the main thread's run loop.
func (systemNowPlaying) update(title, artist, station string, playing bool) {
	cTitle, cArtist, cStation := C.CString(title), C.CString(artist), C.CString(station)
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cArtist))
	defer C.free(unsafe.Pointer(cStation))
	var cPlaying C.int
	if playing {
		cPlaying = 1
	}
	C.somaNowPlayingUpdate(cTitle, cArtist, cStation, cPlaying)
}

// clear gives Now Playing and the remote commands up.
func (systemNowPlaying) clear() { C.somaNowPlayingClear() }

// runMainLoop runs the calling thread's run loop, which has to be the main
// thread's, until stopMainLoop, which may come first.
func runMainLoop() { C.somaRunMainLoop() }

func stopMainLoop() { C.somaStopMainLoop() }
