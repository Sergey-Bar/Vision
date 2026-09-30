package rtsp

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// UserAgent identifies this client to a camera. It is a fixed string with no
// version in it: a build number in a User-Agent reaches a vendor's support
// system and outlives the build it named.
const UserAgent = "security-platform/1.0 rtsp-capture"

// interleavedMagic prefixes every interleaved frame on a TCP transport. Once
// PLAY has been answered it is the only byte on the socket, which is what makes
// it a usable resynchronisation check rather than a heuristic.
const interleavedMagic = 0x24 // '$'

// Options configures a Client.
type Options struct {
	// User and Password authenticate the session. A password is never logged,
	// never printed, and never included in a URL this package returns.
	User     string
	Password string
	// Timeout bounds the connect and every request. A camera that accepts a
	// connection and then stops answering must not hold a capture worker
	// forever, and a request with no deadline is exactly that.
	Timeout time.Duration
	// Transport names the transport to carry the session. Empty takes
	// TransportTCPInterleaved, the only one this build carries.
	Transport string
	// Transport is refused by name for anything else.
	MaxNALBytes int
}

// DefaultTimeout bounds a request when the caller names none.
const DefaultTimeout = 10 * time.Second

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Transport == "" {
		o.Transport = TransportTCPInterleaved
	}
	if o.MaxNALBytes <= 0 {
		o.MaxNALBytes = 8 << 20
	}
	return o
}

// Client is one RTSP session.
//
// It is not safe for concurrent use. A capture path runs one goroutine per
// camera, and a session is a sequence: a request, then packets, then another
// request on the same connection. Sharing one across goroutines would interleave
// a PLAY into a packet stream.
type Client struct {
	opts  Options
	url   *URL
	conn  net.Conn
	br    *bufio.Reader
	cseq  int
	desc  *Description
	dep   *Depacketizer
	state string
}

// Dial connects and authenticates, but starts no media.
//
// Connect and describe are separate on purpose: a caller that wants to say
// something about what the camera is before a stream starts - which is what an
// operator banner is - needs the description without the PLAY.
func Dial(ctx context.Context, rawURL string, opts Options) (*Client, error) {
	o := opts.withDefaults()
	if o.Transport != TransportTCPInterleaved {
		return nil, fmt.Errorf("%w: %q; this build carries %s interleaved only",
			ErrUnsupportedTransport, o.Transport, TransportTCPInterleaved)
	}
	u, err := ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	// Credentials in the URL are the normal way an RTSP session is
	// authenticated, so they are honoured when the caller did not pass them
	// separately. A caller who passes a user explicitly is overriding the URL,
	// and an override that silently loses to the URL is not an override.
	if o.User == "" && u.User != "" {
		o.User, o.Password = u.User, u.Password
	}
	d := net.Dialer{Timeout: o.Timeout}
	conn, err := d.DialContext(ctx, "tcp", u.Address())
	if err != nil {
		return nil, fmt.Errorf("rtsp: dial %s: %w", u.Address(), err)
	}
	c := &Client{opts: o, url: u, conn: conn, br: bufio.NewReaderSize(conn, 64<<10), state: "connected"}
	return c, nil
}

// URL returns the endpoint with credentials removed.
func (c *Client) URL() *URL { return c.url }

// Description returns what the camera described, once Describe has run.
func (c *Client) Description() *Description { return c.desc }

// State names where the session is, for a status line. It is a closed
// vocabulary because it is displayed, and a displayed string nobody can
// enumerate is a string nobody can act on.
func (c *Client) State() string { return c.state }

func (c *Client) authHeaders() []string {
	if c.opts.User == "" {
		return nil
	}
	// RTSP allows Basic preemptively. The alternative is a 401 round trip that
	// sends the camera's realm to an unauthenticated peer first, and Basic over
	// a plaintext RTSP connection is what the scheme was designed around.
	tok := base64.StdEncoding.EncodeToString([]byte(c.opts.User + ":" + c.opts.Password))
	return []string{"Authorization: Basic " + tok}
}

// do sends one request and returns the reply, mapping the status codes a caller
// has to be able to tell apart.
//
// uri is the request target rather than the session URL, because RTSP gives
// each verb a different one: DESCRIBE and PLAY address the session, and SETUP
// addresses the media control URI the description declared. Sending SETUP at
// the session URL is accepted by some cameras and refused by others, and which
// is which is not something to find out in production.
func (c *Client) do(ctx context.Context, method, uri string, headers []string) (*Response, error) {
	if c.conn == nil {
		return nil, ErrNotRTSP
	}
	if uri == "" {
		uri = c.url.Request()
	}
	deadline := time.Now().Add(c.opts.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("rtsp: set deadline: %w", err)
	}
	c.cseq++
	all := append(c.authHeaders(), headers...)

	if _, err := c.conn.Write(request(method, uri, c.cseq, all, "")); err != nil {
		return nil, fmt.Errorf("rtsp: %s: %w", method, err)
	}
	resp, err := readResponse(c.br)
	if err != nil {
		return nil, fmt.Errorf("rtsp: %s: %w", method, err)
	}
	if got := resp.Get("CSeq"); got != "" {
		if n, err := strconv.Atoi(got); err != nil || n != c.cseq {
			// A mismatched CSeq means the reply belongs to another request, and
			// acting on it would be acting on an answer to something else.
			return nil, fmt.Errorf("%w: CSeq %q does not answer the %d we sent", ErrMalformedResponse, got, c.cseq)
		}
	}
	switch resp.Status {
	case 401, 403:
		return resp, fmt.Errorf("%w: %s answered %d %s", ErrUnauthorized, c.url.Address(), resp.Status, resp.Reason)
	case 200:
		return resp, nil
	}
	if resp.Status == 461 || resp.Status == 551 {
		return resp, fmt.Errorf("%w: %s answered %d %s", ErrUnsupportedTransport, c.url.Address(), resp.Status, resp.Reason)
	}
	return resp, fmt.Errorf("rtsp: %s answered %d %s", method, resp.Status, resp.Reason)
}

// Options lists what the server will accept, which is the cheapest way to learn
// whether it speaks RTSP at all before spending a DESCRIBE on it.
func (c *Client) Options(ctx context.Context) (*Response, error) {
	return c.do(ctx, "OPTIONS", "", nil)
}

// Describe asks the camera what its stream is.
func (c *Client) Describe(ctx context.Context) (*Response, error) {
	resp, err := c.do(ctx, "DESCRIBE", "", []string{"Accept: application/sdp"})
	if err != nil {
		return resp, err
	}
	desc, err := parseSDP(resp.Body)
	if err != nil {
		return resp, err
	}
	c.desc = desc
	if s := resp.Get("Session"); s != "" {
		desc.Session = sessionID(s)
	}
	return resp, nil
}

// sessionID strips the optional ";timeout=" suffix servers append.
func sessionID(v string) string {
	if i := strings.IndexByte(v, ';'); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return strings.TrimSpace(v)
}

// Play sets the single media track up and starts it.
func (c *Client) Play(ctx context.Context) error {
	if c.desc == nil {
		if _, err := c.Describe(ctx); err != nil {
			return err
		}
	}
	control := c.url.Request()
	if c.desc.Control != "" && c.desc.Control != "*" {
		control = joinControl(c.url, c.desc.Control)
	}
	setup, err := c.do(ctx, "SETUP", control, []string{
		"Transport: RTP/AVP/TCP;unicast;interleaved=0-1",
		"Accept: application/sdp",
	})
	if err != nil {
		return err
	}
	// The Session header the SETUP returned is the only statement of the id
	// this server will accept afterwards, and cameras disagree about whether it
	// is the DESCRIBE's or the SETUP's. Taking the later one is what both
	// families of camera accept.
	if s := sessionID(setup.Get("Session")); s != "" {
		c.desc.Session = s
	}
	if _, err := c.do(ctx, "PLAY", "", []string{
		"Range: npt=0.000-",
		"Session: " + c.desc.Session,
	}); err != nil {
		return err
	}
	c.state = "playing"
	c.dep = NewDepacketizer(c.opts.MaxNALBytes)
	return nil
}

// joinControl resolves a media-level control attribute against the request URI.
func joinControl(u *URL, control string) string {
	if strings.HasPrefix(control, "rtsp://") {
		return control
	}
	base := strings.TrimSuffix(u.Path, "/")
	switch {
	case strings.HasPrefix(control, "/"):
		return (&urlScheme{Host: u.Address(), Path: control}).String()
	case strings.Contains(control, "=") || strings.HasPrefix(control, "trackID"):
		// A trackID or a session-level control belongs to the base URL, and
		// cameras reject a SETUP that appends it to the track path.
		return (&urlScheme{Host: u.Address(), Path: base}).String()
	default:
		return (&urlScheme{Host: u.Address(), Path: base + "/" + control}).String()
	}
}

// KeepAlive holds the session open. Cameras drop an RTSP session that has said
// nothing for a timeout, and a 24/7 stream with a dropped session is a stream
// that stops recording and says nothing about it.
func (c *Client) KeepAlive(ctx context.Context) error {
	headers := []string{}
	if c.desc != nil && c.desc.Session != "" {
		headers = append(headers, "Session: "+c.desc.Session)
	}
	//nolint:nilerr // a refusal here is deliberately not a session failure; see below
	if _, err := c.do(ctx, "GET_PARAMETER", "", headers); err != nil {
		// A camera that does not implement GET_PARAMETER is common enough that
		// a refusal here is not a session failure; the next read will find out
		// within a timeout, which is the honest signal.
		return nil
	}
	return nil
}

// ReadPacket returns the next RTP packet on the interleaved channel.
//
// RTCP is consumed and discarded: this client sends no receiver reports, and a
// report it never sends is one it cannot be judged on.
func (c *Client) ReadPacket() (*Packet, error) {
	if c.state != "playing" {
		return nil, fmt.Errorf("%w: session is %q", ErrNotRTSP, c.state)
	}
	if err := c.conn.SetReadDeadline(time.Now().Add(c.opts.Timeout)); err != nil {
		return nil, err
	}
	for {
		first, err := c.br.ReadByte()
		if err != nil {
			return nil, err
		}
		if first != interleavedMagic {
			// Interleaved data is the only thing on this socket once PLAY has
			// been answered. Anything else means the stream is not what the
			// handshake said it was, and resynchronising a byte stream by
			// guessing is how a client ends up muxing somebody else's video.
			return nil, fmt.Errorf("%w: expected an interleaved frame, got 0x%02x", ErrMalformedResponse, first)
		}
		var hdr [3]byte
		if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
			return nil, err
		}
		channel := int(hdr[0])
		length := int(hdr[1])<<8 | int(hdr[2])
		if length > c.opts.MaxNALBytes*2 {
			return nil, fmt.Errorf("%w: interleaved frame of %d bytes", ErrNALTooLarge, length)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return nil, err
		}
		if channel != 0 {
			continue // RTCP
		}
		return parseRTP(payload)
	}
}

// NALs returns the complete NAL units the packet completed, and updates the
// access-unit boundary state.
//
// A STAP-A packet carries several NAL units, so this returns a slice. It is
// nil when the packet is a fragment that has not completed a unit yet, which is
// the common case and must not be read as "nothing arrived".
func (c *Client) NALs(p *Packet) ([][]byte, error) {
	return c.dep.Push(*p)
}

// ReadNAL blocks until the next complete NAL unit.
func (c *Client) ReadNAL() ([]byte, bool, uint32, error) {
	for {
		p, err := c.ReadPacket()
		if err != nil {
			return nil, false, 0, err
		}
		out, err := c.NALs(p)
		if err != nil {
			return nil, false, 0, err
		}
		if len(out) == 0 {
			continue
		}
		return out[0], p.Marker, p.Timestamp, nil
	}
}

// Close shuts the session down with a TEARDOWN when it can, and drops the
// connection either way.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	if c.state == "playing" && c.desc != nil && c.desc.Session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = c.do(ctx, "TEARDOWN", "", []string{"Session: " + c.desc.Session})
		cancel()
	}
	c.state = "closed"
	err := c.conn.Close()
	c.conn = nil
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// urlScheme is a tiny helper so control-URI joining does not drag net/url's
// escaping rules into a path a camera has to accept verbatim.
type urlScheme struct {
	Host string
	Path string
}

func (u *urlScheme) String() string {
	p := u.Path
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "rtsp://" + u.Host + p
}
