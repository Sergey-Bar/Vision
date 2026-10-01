package vlm

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// quotedEscaper makes a string safe to place inside a quoted-string parameter of
// a Content-Disposition header.
//
// It is strings.NewReplacer rather than mime/multipart's own escaping because the
// stdlib escaping leaves a backslash-quoted filename, which some servers do not
// accept, and the two characters below are the only ones that need handling.
var quotedEscaper = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
)

// multipartWriter builds a multipart/form-data body.
//
// The framing is a file part and an optional name, and nothing else: every extra
// field is a field the provider has to tolerate and this client has to get right.
type multipartWriter struct {
	w   *multipart.Writer
	buf *bytes.Buffer
}

func newMultipart(buf *bytes.Buffer) *multipartWriter {
	return &multipartWriter{w: multipart.NewWriter(buf), buf: buf}
}

func (m *multipartWriter) file(field, filename, contentType string, data []byte) error {
	if filename == "" {
		return fmt.Errorf("vlm: a multipart file part needs a filename")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quotedEscaper.Replace(field), quotedEscaper.Replace(filename)))
	h.Set("Content-Type", contentType)
	p, err := m.w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("vlm: build the upload part: %w", err)
	}
	if _, err := p.Write(data); err != nil {
		return fmt.Errorf("vlm: write the upload part: %w", err)
	}
	return nil
}

func (m *multipartWriter) field(name, value string) error {
	if err := m.w.WriteField(name, value); err != nil {
		return fmt.Errorf("vlm: build the upload field %q: %w", name, err)
	}
	return nil
}

func (m *multipartWriter) close() error {
	if err := m.w.Close(); err != nil {
		return fmt.Errorf("vlm: finish the upload body: %w", err)
	}
	return nil
}

func (m *multipartWriter) contentType() string { return m.w.FormDataContentType() }
