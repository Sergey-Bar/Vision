// Package source turns a source specification into one ffmpeg command line, and
// measures what the machine can actually do before the run starts.
//
// The package's hard rule is one reader: it builds arguments for a single
// ffmpeg process and it never opens the source itself. Go does not open the
// video, ever. The MJPEG pipe is drained by internal/recorder, which stamps
// each frame with time.Now() at the moment it is read, and that is the only
// clock in the system.
package source

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Kind is how the video is obtained. Exactly one process reads it.
type Kind string

const (
	// KindLoop plays a file in a loop. This is the measured source: a real
	// stream of frames from recorded material, at a rate the operator chooses.
	KindLoop Kind = "loop"
	// KindRTSP reads a live camera. No real camera has ever been reachable from
	// this repository; the path IS exercised, by relaying a recording through an
	// rtsp server, and a relayed recording proves the transport and proves
	// nothing about a camera. See Spec.Relay.
	KindRTSP Kind = "rtsp"
	// KindURL reads an http(s) stream, for example an m3u8 or an MJPEG feed.
	KindURL Kind = "url"
)

// Spec is a validated source. Build it with Parse, not by hand: Parse is where
// the kind/argument combination is checked, and a mismatch there is the thing
// that would make the report claim a path was exercised when it was not.
type Spec struct {
	Kind Kind
	Arg  string
	// User and Password authenticate a live camera.
	//
	// They are SEPARATE from Arg on purpose. A camera URL with the credentials
	// inline, rtsp://admin:hunter2@10.0.0.9/stream, leaks in four places at
	// once: it is stored in the run row, it is printed by `watch --config`, it
	// appears in the log of every command that echoes the source, and it is
	// visible in the process table to anything on the machine. ffmpeg is given
	// the credentials as an Authorization HEADER instead, so none of those four
	// places ever sees the password.
	//
	// The password is a secret and is redacted wherever a secret is redacted.
	User     string
	Password string

	// Relay says this live source is a RECORDING re-published over the network
	// and not a camera.
	//
	// It is a field on the Spec rather than a label the caller supplies because
	// the label is DERIVED. `Label()` said "live rtsp camera" for every rtsp
	// source, which is false for an ffmpeg publisher on loopback, and a derived
	// sentence cannot be wrong in a caller's hand: it is either the camera
	// sentence or the relay sentence, chosen by this one flag.
	//
	// The distinction is not pedantry. A relay proves the rtsp PATH and proves
	// nothing about a camera, and a report that says "camera" over a relay is
	// claiming a piece of hardware nobody has.
	Relay bool
}

// Live reports whether the source is a real-time one, as against recorded
// material played in a loop. The report and the provenance default both branch
// on this, and a live source is the only one that can honestly be `real`.
func (s Spec) Live() bool { return s.Kind == KindRTSP || s.Kind == KindURL }

// Parse validates a (kind, arg) pair. A loop given a URL is refused: a loop run
// is a measurement over recorded material and a live URL would be presented as
// one, which is the specific dishonesty the run exists to avoid.
func Parse(kind, arg string) (Spec, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Spec{}, fmt.Errorf("SP_POC_SOURCE_ARG: not set; ffmpeg has no input, so preflight never sees a first frame and the run cannot start; set it to a file path or a URL")
	}
	// A URL carrying credentials is refused outright rather than extracted. The
	// password is already in the run row by the time this is reached, so the
	// refusal is loud on purpose: the operator has to move it to
	// SP_POC_SOURCE_PASSWORD, and the hint says so.
	if hasInlineCredentials(arg) {
		return Spec{}, fmt.Errorf(
			"SP_POC_SOURCE_ARG: the source URL carries credentials inline. That string is stored in the run row, printed by `watch --config`, and visible in the process table, so it is refused.\n" +
				"  Put the camera's address in SP_POC_SOURCE_ARG and its credentials in SP_POC_SOURCE_USER and SP_POC_SOURCE_PASSWORD. ffmpeg is given them as a header, not as part of the URL.")
	}
	switch Kind(kind) {
	case KindLoop:
		if hasURLScheme(arg) {
			return Spec{}, fmt.Errorf("SP_POC_SOURCE_ARG: kind=loop was given %q, which is a URL; a loop run is a measurement over recorded material and presenting a live feed as one is a false claim", arg)
		}
	case KindRTSP:
		if !strings.HasPrefix(strings.ToLower(arg), "rtsp://") && !strings.HasPrefix(strings.ToLower(arg), "rtsps://") {
			return Spec{}, fmt.Errorf("SP_POC_SOURCE_ARG: kind=rtsp was given %q, which is not an rtsp URL; the report would claim the rtsp path was exercised when it was not", arg)
		}
	case KindURL:
		lower := strings.ToLower(arg)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			return Spec{}, fmt.Errorf("SP_POC_SOURCE_ARG: kind=url was given %q, which is not an http(s) URL", arg)
		}
	default:
		return Spec{}, fmt.Errorf("SP_POC_SOURCE_KIND: %q is not one of loop, rtsp, url; ffmpeg would be handed an input we do not understand and the failure would appear as a hang", kind)
	}
	return Spec{Kind: Kind(kind), Arg: arg}, nil
}

func hasURLScheme(arg string) bool {
	lower := strings.ToLower(arg)
	for _, p := range []string{"http://", "https://", "rtsp://", "rtsps://", "rtmp://", "udp://", "tcp://"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// hasInlineCredentials reports whether a source argument is a URL with a
// `user:pass@` or bare `user@` section.
func hasInlineCredentials(arg string) bool {
	// Only a URL can carry credentials; a Windows path may legitimately contain
	// '@' (`C:\Users\me\@backup\x.mp4`), so the scheme check comes first.
	lower := strings.ToLower(arg)
	scheme := false
	for _, s := range []string{"rtsp://", "rtsps://", "http://", "https://", "rtmp://", "udp://", "tcp://"} {
		if strings.HasPrefix(lower, s) {
			scheme = true
			break
		}
	}
	if !scheme {
		return false
	}
	rest := arg[strings.Index(arg, "://")+3:]
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		return true
	}
	return false
}

// InputArgs returns the -i arguments for ffmpeg. It is deliberately
// conservative: transport settings that change what a camera delivers are left
// at ffmpeg's defaults except for the two that are known to matter here —
// rtsp_transport, because the default sometimes picks UDP and a corporate
// network silently drops it, and reconnect for a live camera, which is
// recorded rather than silent (see Recorder's reconnect watcher).
// socketTimeoutSeconds bounds ffmpeg's network reads on a LIVE source.
//
// It is a socket timeout, not the pipeline's frame budget: it answers "has the
// far end stopped talking", which is the only question the reader can ask while
// nothing is arriving. The pipeline's own `SP_POC_GAP_THRESHOLD` answers "how
// long may a run have no frames before it is a gap", and the two are different
// questions with different answers, so the reader is bounded well below the
// threshold rather than at it.
const socketTimeoutSeconds = 10

func (s Spec) InputArgs() []string {
	switch s.Kind {
	case KindLoop:
		return []string{"-re", "-stream_loop", "-1", "-i", s.Arg}
	case KindRTSP:
		// RTSP is over TCP because UDP loses packets on exactly the networks a
		// camera is on, and ffmpeg will silently produce a green run over a
		// dropped stream.
		//
		// There are deliberately NO `-reconnect` flags. `-reconnect`,
		// `-reconnect_streamed` and `-reconnect_delay_max` are HTTP PROTOCOL
		// options; the RTSP demuxer has no such option, and ffmpeg answers
		// `Option reconnect not found.` and exits. Because the reader exits, the
		// pipeline records nothing at all: the run ends in twenty milliseconds and
		// the report says zero of everything, which is what a reader would take
		// for "the road was empty".
		//
		// They used to be here, on the strength of "RTSP really does reconnect".
		// It does — at the SERVER. ffmpeg's rtsp CLIENT does not, and the two are
		// not the same thing. What the demuxer does have is `-timeout`, which
		// bounds a hung socket so a stalled camera ends the run with a recorded
		// reason instead of an indefinite stall.
		//
		// The consequence, stated rather than papered over: a run over RTSP does
		// NOT survive the publisher being killed. `Recorder.watchReconnects` still
		// records a `stream_gap` whenever ffmpeg's log reports one, and the RTSP
		// client reports none. A camera drop is a RUN ENDING, and the durability
		// AC1 claims over a seven-day run does not extend to a transport whose
		// client cannot reconnect. That is a real limit of this build, and it is
		// written where a reader of `InputArgs` will find it.
		return append(
			append(
				[]string{"-rtsp_transport", "tcp", "-timeout", strconv.Itoa(socketTimeoutSeconds)},
				s.authArgs()...,
			),
			"-i", s.Arg)
	case KindURL:
		// HTTP, where the reconnect options DO exist and do what the comment above
		// used to claim for RTSP.
		return append(
			append([]string{"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5"}, s.authArgs()...),
			"-i", s.Arg)
	default:
		return []string{"-i", s.Arg}
	}
}

// authArgs builds the credential arguments.
//
// The credentials go in as an explicit header rather than in the URL, for the
// leak reasons on the Spec fields. ffmpeg also honours `user:pass@` in the URL
// for RTSP, and that form is refused by Parse precisely because it leaks.
//
// `-headers` is used rather than `-rtsp_flags` because a Basic/Digest
// Authorization header is what every camera's RTSP server understands; the
// alternative is per-vendor flag sets, and a camera that needs one of those
// will say so in its own error, which names the vendor.
func (s Spec) authArgs() []string {
	if s.User == "" && s.Password == "" {
		return nil
	}
	cred := s.User + ":" + s.Password
	// RTSP Basic, base64. A camera that wants Digest sends 401 and ffmpeg's
	// digest handling takes over; this is the first credential offered, not the
	// only one.
	enc := base64.StdEncoding.EncodeToString([]byte(cred))
	return []string{"-headers", "Authorization: Basic " + enc + "\r\n"}
}

// HasCredentials reports whether the source authenticates, so the report can
// say whether the camera was open or not. An unauthenticated public camera is a
// real configuration and the report must not imply otherwise.
func (s Spec) HasCredentials() bool { return s.User != "" || s.Password != "" }

// Label is the human sentence the screen header and the report print next to
// every number. It is the truth line, so it is derived, never composed by the
// caller.
// LabelRedacted is the source line the REPORT and the log print.
//
// It never contains the credentials, and for a live source it names the
// authentication, because "is this camera open or not" changes what a run means:
// an unauthenticated camera is a real configuration and the report must not imply
// otherwise.
func (s Spec) LabelRedacted() string {
	base := s.Label()
	if !s.Live() {
		return base
	}
	if s.HasCredentials() {
		return base + ", authenticated as " + s.User
	}
	return base + ", NOT authenticated"
}

func (s Spec) Label() string {
	switch s.Kind {
	case KindLoop:
		return "source: recorded loop (no live camera)"
	case KindRTSP:
		if s.Relay {
			// D-5, in words, on every surface that prints a source. The
			// parenthetical is the whole sentence and it is deliberately blunt:
			// "relay of recorded material, NOT a camera" cannot be misread as a
			// camera that happens to be relayed, which is the misreading that
			// matters.
			return "source: live rtsp (relay of recorded material, NOT a camera)"
		}
		return "source: live rtsp camera"
	case KindURL:
		if s.Relay {
			return "source: live http stream (relay of recorded material, NOT a camera)"
		}
		return "source: live http stream"
	default:
		return "source: unknown"
	}
}

// LoopArgs returns the arguments that make a file kind loop forever.
func (s Spec) LoopArgs() []string { return s.InputArgs() }
