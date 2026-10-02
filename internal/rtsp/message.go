package rtsp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The bounds on a response are the point of this file. An RTSP peer is an
// untrusted network endpoint, and one that never sends a blank line would
// otherwise grow a header buffer until the process dies.
const (
	maxResponseHeaderBytes = 8 << 10
	maxHeaderLines         = 64
)

// Response is one parsed RTSP reply.
type Response struct {
	Version string
	Status  int
	Reason  string
	Header  map[string]string
	Body    []byte
}

// Get returns a header value, matched case-insensitively as HTTP requires.
func (r *Response) Get(name string) string {
	if r.Header == nil {
		return ""
	}
	if v, ok := r.Header[name]; ok {
		return v
	}
	for k, v := range r.Header {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// ErrMalformedResponse is returned for a reply this parser will not guess at.
var ErrMalformedResponse = errors.New("rtsp: malformed response")

// readResponse reads one reply. Bodies are bounded by their declared
// Content-Length and by the header bound, and a length that does not fit is a
// refusal rather than an allocation.
func readResponse(br *bufio.Reader) (*Response, error) {
	line, err := readLine(br, maxResponseHeaderBytes)
	if err != nil {
		return nil, err
	}
	// "RTSP/1.0 200 OK"
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "RTSP/") {
		return nil, fmt.Errorf("%w: status line %q", ErrMalformedResponse, truncate(line))
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: status code %q", ErrMalformedResponse, parts[1])
	}
	resp := &Response{Version: parts[0], Status: status, Header: map[string]string{}}
	if len(parts) == 3 {
		resp.Reason = parts[2]
	}

	used := len(line) + 2
	complete := false
	for i := 0; i < maxHeaderLines; i++ {
		h, err := readLine(br, maxResponseHeaderBytes)
		if err != nil {
			return nil, err
		}
		if h == "" {
			complete = true
			break
		}
		used += len(h) + 2
		if used > maxResponseHeaderBytes {
			return nil, fmt.Errorf("%w: header block exceeds %d bytes", ErrMalformedResponse, maxResponseHeaderBytes)
		}
		colon := strings.IndexByte(h, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: header %q", ErrMalformedResponse, truncate(h))
		}
		resp.Header[strings.TrimSpace(h[:colon])] = strings.TrimSpace(h[colon+1:])
	}
	if !complete {
		return nil, fmt.Errorf("%w: more than %d header lines", ErrMalformedResponse, maxHeaderLines)
	}

	if n, err := strconv.Atoi(resp.Get("Content-Length")); err == nil && n > 0 {
		if n > maxResponseHeaderBytes {
			return nil, fmt.Errorf("%w: body of %d bytes exceeds the bound", ErrMalformedResponse, n)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, fmt.Errorf("%w: body: %w", ErrMalformedResponse, err)
		}
		resp.Body = body
	}
	return resp, nil
}

// readLine reads one CRLF- or LF-terminated line, bounded.
func readLine(br *bufio.Reader, limit int) (string, error) {
	var b strings.Builder
	for b.Len() <= limit {
		c, err := br.ReadByte()
		if err != nil {
			if b.Len() == 0 {
				return "", err
			}
			return "", fmt.Errorf("%w: line ended without a newline", ErrMalformedResponse)
		}
		if c == '\n' {
			return strings.TrimSuffix(b.String(), "\r"), nil
		}
		b.WriteByte(c)
	}
	return "", fmt.Errorf("%w: line exceeds %d bytes", ErrMalformedResponse, limit)
}

func truncate(s string) string {
	const limit = 120
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// request renders one RTSP request.
//
// The order of the headers is fixed rather than map-iterated: two runs against
// the same camera then produce the same bytes, which is what makes a recorded
// session usable as a fixture.
func request(method, uri string, cseq int, headers []string, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s RTSP/1.0\r\n", method, uri)
	fmt.Fprintf(&b, "CSeq: %d\r\n", cseq)
	for _, h := range headers {
		b.WriteString(h)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}
