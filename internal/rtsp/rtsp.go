// Package rtsp is a minimal RTSP 1.0 receiver that turns a camera's H.264 RTP
// stream into Annex-B NAL units.
//
// # Why it is written at all
//
// The obvious way to read a camera is a media framework, and the obvious media
// framework on Windows is one that needs cgo, a plugin registry, or a runtime
// download. This product installs into air-gapped sites where none of those
// exist, and a capture path that can only be built on one machine is a capture
// path nobody can diagnose on the machine where the camera is.
//
// So: pure Go, no cgo, no system dependency, `go build ./...` green on a
// machine with no CUDA and no GStreamer. What that costs is scope, and the
// scope is written down rather than implied - see [Client].
//
// # What it does not do
//
// It does not fetch video from a VMS archive, it does not speak HTTP, and it
// does not fall back to HLS. A camera that offers only those is a camera this
// client names as unsupported, which is a different thing from a camera it
// silently fails to open.
//
// # Transport
//
// TCP interleaved only, in this build. UDP is the more efficient mode and TCP
// is the one that works on networks that are not the vendor's own, which is
// most of them; a client that quietly picked UDP and hung would cost more to
// diagnose than the refusal costs to write.
package rtsp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Transport names the transports this build can carry a session over.
const (
	// TransportTCPInterleaved multiplexes RTP and RTCP over the control
	// connection, as RFC 2326 section 10.12 describes.
	TransportTCPInterleaved = "TCP"
)

// Errors this package returns by name. They are named because the caller has to
// be able to say "the camera would not authenticate" differently from "the
// camera does not speak RTSP", and a merged "connection failed" cannot.
var (
	// ErrNotRTSP means the URL or the server's answer is not RTSP at all.
	ErrNotRTSP = errors.New("rtsp: not an RTSP endpoint")
	// ErrUnauthorized is a 401, and it is the one failure a caller can fix by
	// supplying credentials.
	ErrUnauthorized = errors.New("rtsp: authentication required or rejected")
	// ErrUnsupportedTransport means the session would have to run over a
	// transport this build does not carry.
	ErrUnsupportedTransport = errors.New("rtsp: transport not supported by this build")
	// ErrNoH264 means the camera describes a stream with no H.264 payload.
	ErrNoH264 = errors.New("rtsp: the described stream carries no H.264")
	// ErrNALTooLarge bounds a single depacketised NAL unit.
	ErrNALTooLarge = errors.New("rtsp: NAL unit exceeded the size bound")
	// ErrPacketizationMode means the camera asked for a packetization mode this
	// depacketiser does not implement.
	ErrPacketizationMode = errors.New("rtsp: unsupported H.264 packetization mode")
)

// URL is a parsed RTSP endpoint.
type URL struct {
	Scheme   string
	User     string
	Password string
	Host     string
	Port     int
	Path     string
}

// DefaultPort is the RTSP port when a URL names no port.
const DefaultPort = 554

// ParseURL parses an rtsp:// or rtsps:// URL.
//
// Credentials in the URL are accepted, because that is how RTSP is
// authenticated, and they are not logged anywhere in this package: a camera
// password that reaches a log line is a camera password in a file somebody
// attaches to a ticket.
func ParseURL(raw string) (*URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("rtsp: parse %q: %w", redact(raw), err)
	}
	switch strings.ToLower(u.Scheme) {
	case "rtsp":
	case "rtsps":
		// TLS is not carried by this build. Refusing by name beats accepting a
		// scheme whose security this client does not implement.
		return nil, fmt.Errorf("%w: rtsps needs a transport this build does not carry", ErrUnsupportedTransport)
	default:
		return nil, fmt.Errorf("%w: scheme %q", ErrNotRTSP, u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: no host in %q", ErrNotRTSP, redact(raw))
	}
	port := DefaultPort
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("%w: port %q", ErrNotRTSP, p)
		}
		port = n
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	return &URL{Scheme: u.Scheme, User: user, Password: pass, Host: u.Hostname(), Port: port, Path: path}, nil
}

// Address is the host:port to dial.
func (u *URL) Address() string { return net.JoinHostPort(u.Host, strconv.Itoa(u.Port)) }

// String is the URL with any credentials removed. Every diagnostic in this
// package prints the target through this, so a password cannot reach a log by
// being interpolated into one.
func (u *URL) String() string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Address(), Path: u.Path}).String()
}

// Request renders the absolute form RTSP requires, without credentials.
func (u *URL) Request() string { return u.String() }

func redact(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		if j := strings.Index(raw[i+3:], "@"); j >= 0 {
			return raw[:i+3] + "***@" + raw[i+3+j+1:]
		}
	}
	return raw
}
