package main

// `serve-camera` — a long-running RTSP server, so criterion 02 can be evidenced.
//
// # What this is, in the sentence a jury will read
//
// **It is a live RTSP server replaying a committed clip.** It is not a camera.
//
// The distinction is not pedantry. A camera is a device that decides how to encode,
// when to send a keyframe, what its authentication policy is, how it behaves when
// its network is lossy, and what it does with a client that asks for a codec it
// does not have. This server decides none of those things and guesses one of them
// (the frame rate). What the criterion now claims is narrower and true:
//
//   the capture path runs end to end over TCP, against a peer that answers a real
//   OPTIONS / DESCRIBE / SETUP / PLAY / TEARDOWN handshake, with real SDP parsing,
//   real RFC 6184 packetisation and depacketisation, and the recovered NAL units
//   compared byte for byte against the source.
//
// # Why it is `securityctl serve-camera` and not `pocctl serve-camera`
//
// The plan says `pocctl`. The plan's premise was that the RTSP client and the muxer
// were in the POC module. They are not — they are in THIS module, at
// `internal/rtsp` and `internal/mp4`, and the POC has no `replace` directive, so Go's
// `internal/` visibility rule makes them unreachable from there without inventing a
// second packetiser. A server in the POC would have to reimplement the client it is
// supposed to exercise, and a client and a server that share code agree with each
// other whether or not either is right.
//
// So the server lives next to the client it exercises. That is the same reason
// `internal/treestate` insists the gate a package is tested with is in the same
// module as the package.
//
// # What it is extracted from
//
// `internal/rtsp/rtsp_test.go`'s `scriptedServer`, moved into `server.go`. The
// client's tests now start THIS server rather than a second one, so there is no
// second opinion about RFC 6184 held by the same author. Four things changed in the
// move and each is recorded at the code: the FU-A indicator carried the real
// `nal_ref_idc` rather than a constant, the RTP marker is per access unit rather
// than once per stream, the stream is paced and loops rather than firing as fast as
// the socket allows and then dropping the connection, and the SDP declares the
// fixture's real frame geometry rather than a constant invented beside the code
// that captures.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/rtsp"
)

func serveCameraCommand() *command.Command {
	return &command.Command{
		Name:    "serve-camera",
		Summary: "Serve a committed clip over RTSP on a real socket, for criterion 02",
		Usage: `serve-camera [--clip <path>] [--listen <addr>] [--fps <n>] [--loop]

Runs an RTSP server that answers a real handshake and streams a committed
Annex-B clip as RTP. This is a CAMERA SUBSTITUTE, not a camera, and nothing it
prints calls it one.

  --clip     the Annex-B elementary stream. Default testdata/poc/testsrc.h264.
  --listen   host:port. Port 0 asks for one and the URL printed is the real one.
  --fps      declared and paced frame rate. Default 25, matching the fixture's SDP.
  --loop     repeat the clip when it ends. Default true; --loop=false stops at the
             end, and a client blocked on a read is a different code path.
  --user     --password   require HTTP Basic on DESCRIBE. Off by default, which is
             right for a loopback demo and wrong for a camera on a network.

Prints the URL, then every request it received, then exits on SIGINT.`,
		Run: runServeCamera,
	}
}

func runServeCamera(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("serve-camera", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	clip := fs.String("clip", "testdata/poc/testsrc.h264", "The Annex-B clip to serve.")
	listen := fs.String("listen", "127.0.0.1:0", "host:port to listen on.")
	fps := fs.Int("fps", 25, "Declared and paced frame rate.")
	loop := fs.Bool("loop", true, "Repeat the clip when it ends.")
	user := fs.String("user", "", "HTTP Basic user for DESCRIBE.")
	password := fs.String("password", "", "HTTP Basic password for DESCRIBE.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	nals, err := readAnnexB(*clip)
	if err != nil {
		return err
	}

	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now()
	srv, err := rtsp.StartServer(serveCtx, rtsp.ServerOptions{
		Listen: *listen, NALs: nals, FPS: *fps, Loop: *loop,
		User: *user, Password: *password,
	})
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	url, err := srv.URL()
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "A CAMERA SUBSTITUTE, NOT A CAMERA\n")
	fmt.Fprintf(env.Stdout, "  serving %s over RTSP at %s\n", *clip, url)
	fmt.Fprintf(env.Stdout, "  %d NAL units, %d fps declared, loop=%v\n", len(nals), *fps, *loop)
	if *user == "" {
		fmt.Fprintf(env.Stdout, "  no authentication: this is a loopback fixture and a camera on a\n")
		fmt.Fprintf(env.Stdout, "  network would not do this. Pass --user and --password for that.\n")
	}
	fmt.Fprintf(env.Stdout, "\n  securityctl poc --camera %s --max-time 10s\n", url)
	fmt.Fprintf(env.Stdout, "\nCtrl-C to stop.\n\n")

	// The request log is the reason this is a command and not a fixture. A server
	// nobody can inspect is a server nobody can tell apart from a client that never
	// dialled it, and the acceptance criterion quotes the handshake it saw.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	reported := 0
	for {
		select {
		case <-serveCtx.Done():
			fmt.Fprintf(env.Stdout, "\nstopped after %s\n", time.Since(started).Round(time.Millisecond))
			fmt.Fprintf(env.Stdout, "requests seen: %s\n", strings.Join(srv.Seen(), ", "))
			return nil
		case <-ticker.C:
			seen := srv.Seen()
			if len(seen) > reported {
				for _, r := range seen[reported:] {
					fmt.Fprintf(env.Stdout, "  <- %s\n", r)
				}
				reported = len(seen)
			}
		}
	}
}

// readAnnexB reads a clip into NAL units, naming the file in any failure.
//
// A missing file is the single most likely thing to go wrong here, and
// `rtsp.ReadNALs` reports on a reader rather than on a path, so the path is
// carried into the message.
func readAnnexB(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open the clip at %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	nals, err := rtsp.ReadNALs(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return nals, nil
}
