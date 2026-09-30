package source

import (
	"strings"
	"testing"
)

// TestCredentialsInTheURLAreRefused is a security test, not a validation one.
//
// A camera URL with the credentials inline, rtsp://admin:hunter2@10.0.0.9/s,
// is stored in the run row, printed by `watch --config`, echoed into log lines,
// and visible in the process table. That is four copies of a public camera's
// password, none of which anybody chose to publish.
func TestCredentialsInTheURLAreRefused(t *testing.T) {
	for _, arg := range []string{
		"rtsp://admin:hunter2@10.0.0.9/stream",
		"rtsps://admin:hunter2@camera.local:554/Streaming/Channels/101",
		"https://user:pass@cam.example/live.m3u8",
		"rtmp://user:pass@edge/live",
	} {
		for _, kind := range []string{"rtsp", "url"} {
			_, err := Parse(kind, arg)
			if err == nil {
				t.Errorf("Parse(%s, %q) accepted a URL with credentials in it", kind, arg)
				continue
			}
			if !strings.Contains(err.Error(), "SP_POC_SOURCE_USER") ||
				!strings.Contains(err.Error(), "SP_POC_SOURCE_PASSWORD") {
				t.Errorf("the refusal for %q does not say where the credentials belong: %v", arg, err)
			}
		}
	}
}

// TestAPathWithAnAtSignIsNotMistakenForCredentials: `C:\Users\me\@backup\x.mp4`
// is a Windows path, not a URL with a user section.
func TestAPathWithAnAtSignIsNotMistakenForCredentials(t *testing.T) {
	p := `C:\Users\me\@backup\clip.mp4`
	if hasInlineCredentials(p) {
		t.Fatalf("%q was read as a URL carrying credentials; it is a path", p)
	}
	if _, err := Parse("loop", p); err != nil {
		t.Fatalf("a path containing @ was refused: %v", err)
	}
}

// TestCredentialsGoInAsAHeaderNotTheURL: the whole point of separating them.
func TestCredentialsGoInAsAHeaderNotTheURL(t *testing.T) {
	s := Spec{Kind: KindRTSP, Arg: "rtsp://10.0.0.9/stream", User: "admin", Password: "hunter2"}
	args := strings.Join(s.InputArgs(), " ")

	if !strings.Contains(args, "-headers") {
		t.Fatalf("the credentials are not carried as a header: %s", args)
	}
	if strings.Contains(args, "hunter2") {
		t.Fatalf("the password appears in the ffmpeg arguments, which are visible in the process table: %s", args)
	}
	if !strings.Contains(args, "rtsp://10.0.0.9/stream") {
		t.Fatalf("the source URL was altered: %s", args)
	}
	// Basic, base64, as every camera's RTSP server understands on the first
	// attempt. A camera wanting Digest answers 401 and ffmpeg takes over from
	// there.
	if !strings.Contains(args, "Authorization: Basic YWRtaW46aHVudGVyMg==") {
		t.Fatalf("the Authorization header is not Basic admin:hunter2: %s", args)
	}
	// And a single `-i`, still: a second input would be a second reader.
	if n := strings.Count(args, "-i "); n != 1 {
		t.Fatalf("the arguments have %d inputs, want exactly 1: %s", n, args)
	}
}

func TestNoCredentialsMeansNoHeader(t *testing.T) {
	s := Spec{Kind: KindRTSP, Arg: "rtsp://open.camera/stream"}
	args := strings.Join(s.InputArgs(), " ")
	if strings.Contains(args, "-headers") {
		t.Fatalf("an unauthenticated camera was given an empty Authorization header: %s", args)
	}
	if s.HasCredentials() {
		t.Fatal("HasCredentials is true for a camera with no credentials")
	}
}

// TestTheSourceLineSaysWhetherTheCameraIsOpen: an unauthenticated public
// camera is a real configuration, and the report must not imply the run
// authenticated to anything.
func TestTheSourceLineSaysWhetherTheCameraIsOpen(t *testing.T) {
	open := Spec{Kind: KindRTSP, Arg: "rtsp://cam/stream"}.LabelRedacted()
	if !strings.Contains(open, "NOT authenticated") {
		t.Fatalf("an unauthenticated camera does not say so: %q", open)
	}
	if strings.Contains(open, "authenticated as") {
		t.Fatalf("an unauthenticated camera claims a user: %q", open)
	}
	withUser := Spec{Kind: KindRTSP, Arg: "rtsp://cam/stream", User: "admin"}.LabelRedacted()
	if !strings.Contains(withUser, "authenticated as admin") {
		t.Fatalf("an authenticated camera does not name the user: %q", withUser)
	}
	// The password must never appear in the label.
	withPass := Spec{Kind: KindRTSP, Arg: "rtsp://cam/stream", User: "admin", Password: "hunter2"}.LabelRedacted()
	if strings.Contains(withPass, "hunter2") {
		t.Fatalf("the source line contains the password: %q", withPass)
	}
	// A loop is not a camera and must not claim to be one, in either direction.
	loop := Spec{Kind: KindLoop, Arg: "clip.mp4", User: "admin"}.LabelRedacted()
	if strings.Contains(loop, "authenticated") {
		t.Fatalf("a loop claims to be an authenticated camera: %q", loop)
	}
}

func TestLiveIsOnlyTheTwoRealTimeKinds(t *testing.T) {
	rtsp := Spec{Kind: KindRTSP}
	url := Spec{Kind: KindURL}
	if !rtsp.Live() || !url.Live() {
		t.Fatal("a live source is not reported as live")
	}
	loop := Spec{Kind: KindLoop}
	if loop.Live() {
		t.Fatal("a loop is reported as live; a recorded file is not a camera")
	}
}

// TestALoopStillRefusesAURL: unchanged, because a camera is not a measurement
// loop and presenting one as the other is the dishonesty the whole design is
// built against.
func TestALoopStillRefusesAURL(t *testing.T) {
	_, err := Parse("loop", "rtsp://cam/stream")
	if err == nil {
		t.Fatal("kind=loop accepted an rtsp URL")
	}
}
