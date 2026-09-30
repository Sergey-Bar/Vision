package hafnia

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// multipartBuilder writes the upload body.
//
// It is here rather than a bare mime/multipart.Writer because the upload is the
// one place a mistake is both easy and expensive: a wrong field name means the
// provider silently stores nothing and the call appears to succeed. The field
// names are therefore written once, in one place, and the content type is
// derived from the same boundary that was written.
type multipartBuilder struct {
	w *multipart.Writer
}

func newMultipart(buf *bytes.Buffer) *multipartBuilder {
	return &multipartBuilder{w: multipart.NewWriter(buf)}
}

// file adds a file part under a fixed filename.
//
// The filename is fixed rather than taken from the caller. It comes from the
// clip we produced, and letting a caller choose the name a remote processor
// stores the footage under is a way to end up with a path-shaped string in
// someone else's bucket.
func (m *multipartBuilder) file(field, filename, contentType string, data []byte) error {
	if strings.TrimSpace(contentType) == "" {
		return errors.New("hafnia: a file part with no content type")
	}
	// EscapeQuotes is not cosmetic: a filename containing a quote or a newline
	// would otherwise inject headers into the part.
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, escapeQuotes(filename)))
	h.Set("Content-Type", contentType)
	part, err := m.w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("hafnia: build the upload part: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("hafnia: write the upload part: %w", err)
	}
	return nil
}

func (m *multipartBuilder) field(name, value string) error {
	if err := m.w.WriteField(name, value); err != nil {
		return fmt.Errorf("hafnia: write the %q field: %w", name, err)
	}
	return nil
}

func (m *multipartBuilder) close() error {
	if err := m.w.Close(); err != nil {
		return fmt.Errorf("hafnia: finish the upload body: %w", err)
	}
	return nil
}

func (m *multipartBuilder) contentType() string { return m.w.FormDataContentType() }

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }
