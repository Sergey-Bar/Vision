// Package cams is the curated table of PUBLIC live cameras and the resolver
// that turns one of them into a source spec.
//
// The table exists because "read a real camera" is otherwise a thing an
// operator does by pasting a URL they found, and a URL with no row is a URL
// with no recorded basis for being used. Every row states what the camera is,
// who runs it, what the public basis for watching it is, and the date the row
// was last resolved live by a human. A row without those is refused, exactly
// the way `testdata/labels.json` refuses a clip with no provenance.
//
// The resolver runs yt-dlp and nothing else. Go never opens the video: the
// resolved HLS playlist URL is handed to internal/source, which builds one
// ffmpeg command line from it, and from there the camera is read by ffmpeg
// like any other live source. A YouTube live cam is a PUBLIC RELAY of a real
// camera — the source line says "public relay of a real camera, NOT ours" and
// never "camera", because this installation does not own the hardware.
//
// The one thing this package deliberately does not do is decide whether
// recording is permitted. `SP_POC_SOURCE_LICENCE` is where that lives, and the
// configuration requires it for every live source: watching a feed and storing
// clips of it are different acts, and the second is the camera operator's to
// permit. A row here records what is publicly known; it does not manufacture
// permission.
package cams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/source"
)

// TableFile is the name of the curated table, at the module root beside
// testdata/. It is JSON for the reason labels.json is: it is read by a test
// as well as by the command, and a table only one of the two can parse is a
// table the test cannot check.
const TableFile = "cameras.json"

// Camera is one row of the table.
//
// The field set mirrors `testdata/labels.json` on purpose: the same reader
// rules apply. `Licence` is the permission basis for the MATERIAL, not a
// licence file, and `Verified` is the date the row was last resolved live —
// because a public stream that answered on Tuesday is a stream that may be
// gone on Friday, and a table that implies otherwise is a table that will be
// believed on the day it is wrong.
type Camera struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Place    string `json:"place"`
	Operator string `json:"operator"`
	// URL is the camera's public page (the YouTube watch page), not the stream.
	// The stream is resolved at run time and expires; the page is what a human
	// can go and look at.
	URL string `json:"url"`
	// Licence is the recorded basis for reading this camera. It is filled from
	// what is publicly known about the camera, never from what would be
	// convenient to claim, and `licenceUnrecorded` is a refused value.
	Licence string `json:"licence"`
	// Verified is the date (YYYY-MM-DD) the row was last resolved live and the
	// resolved stream was read by ffprobe.
	Verified string `json:"verified"`
	// ContainsPeople is what is publicly true of the view, stated as a
	// sentence. It is not a measurement of the frames: it is why a clip of
	// this camera is personal data and is treated as such.
	ContainsPeople string `json:"contains_people"`
	// ClearedForExternalUse is an explicit boolean, because "the licence is
	// unknown" and "the licence is known and permissive" are different answers
	// and a reader should not have to infer which one this is from prose. A
	// screenshot of a demo over this camera is a screenshot of this camera, so
	// this field decides whether that is allowed.
	ClearedForExternalUse bool     `json:"cleared_for_external_use"`
	Notes                 []string `json:"notes"`
}

// licenceUnrecorded is the marker a row may not use. A camera whose basis
// nobody can name is not a camera this module will resolve for a recorded run.
const licenceUnrecorded = "NOT RECORDED"

// TablePath is the table's location under a module root.
func TablePath(root string) string { return filepath.Join(root, TableFile) }

// Load reads and validates the table at path.
//
// Strict decoding: an unknown field is a refused table rather than an ignored
// one, because a field the loader does not know is a field the table author
// believed was being read.
func Load(path string) ([]Camera, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cams: the camera table at %s could not be read: %w", path, err)
	}
	var doc struct {
		// Comment is the `$comment` array the table's header uses, the same
		// convention `testdata/labels.json` uses. It is DECLARED rather than
		// ignored: strict decoding is the point, and a documented key that
		// strict decoding rejects is a table whose own header makes it invalid.
		Comment []string `json:"$comment"`
		Cameras []Camera `json:"cameras"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("cams: the camera table at %s is not a valid table: %w", path, err)
	}
	if err := validateTable(doc.Cameras); err != nil {
		return nil, err
	}
	return doc.Cameras, nil
}

// LoadFromRoot reads the table at the module root.
func LoadFromRoot(root string) ([]Camera, error) { return Load(TablePath(root)) }

// Find returns the row with the given id.
func Find(cams []Camera, id string) (Camera, bool) {
	for _, c := range cams {
		if c.ID == id {
			return c, true
		}
	}
	return Camera{}, false
}

// IDs are the table's ids, sorted, so a command's output order is the table's
// order rather than the map's.
func IDs(cams []Camera) []string {
	out := make([]string, 0, len(cams))
	for _, c := range cams {
		out = append(out, c.ID)
	}
	sort.Strings(out)
	return out
}

// validateTable refuses a table that would let a run read a camera with no
// recorded basis. Every check is named after the lie it prevents.
func validateTable(cams []Camera) error {
	if len(cams) == 0 {
		return errors.New("cams: the camera table is empty, and an empty table resolves nothing while looking like a configuration")
	}
	seen := map[string]bool{}
	for _, c := range cams {
		if err := validateRow(c); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("cams: the camera table has two rows with id %q, and one of them would be resolved as the other", c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}

func validateRow(c Camera) error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("cams: a camera row has no id, so `pocctl live resolve` cannot name it and the run cannot say which camera it read")
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("cams: camera %q has no name, so the screen would show an id where a place should be", c.ID)
	}
	if err := validatePageURL(c.URL); err != nil {
		return fmt.Errorf("cams: camera %q: %w", c.ID, err)
	}
	// The licence basis is the one field with no default. A row may say "no
	// written permission exists" — that is a fact about the world. A row may
	// not leave the field empty or carry the marker, because an unstated basis
	// reads as "nothing needed stating" to the next person, which is the whole
	// failure this table exists to end.
	lic := strings.TrimSpace(c.Licence)
	if lic == "" {
		return fmt.Errorf("cams: camera %q has no licence. A public feed being PUBLIC is not the same as being free to record: watching it live and storing clips of it are different acts, and the second is the operator's to permit. State the basis in the row, even if the basis is that nobody has given one", c.ID)
	}
	if strings.HasPrefix(strings.ToUpper(lic), licenceUnrecorded) {
		return fmt.Errorf("cams: camera %q records its licence as NOT RECORDED, and a row that says so is refused for a recorded run rather than resolved. `testdata/labels.json`'s YouTube row is the precedent: the footage stayed, the label stayed, and the row is the record of why the run may not be shown", c.ID)
	}
	if strings.TrimSpace(c.Verified) == "" {
		return fmt.Errorf("cams: camera %q has no verified date. A row nobody has resolved live is a row describing a stream that may not exist", c.ID)
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(c.Verified)); err != nil {
		return fmt.Errorf("cams: camera %q's verified date %q is not YYYY-MM-DD: %v", c.ID, c.Verified, err)
	}
	return nil
}

// validatePageURL is the YouTube-page check. It is deliberately narrow: this
// table is the YouTube relay path, and a row pointing at some other host is a
// row the resolver has never been run against. Widening the table means
// running the resolver against the new host and recording that it worked.
func validatePageURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("the row has no URL, so there is nothing to resolve")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a URL: %w", raw, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%q is not https; the camera's public page is public and has no reason to be read in the clear", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host != "www.youtube.com" && host != "youtube.com" && host != "m.youtube.com" && host != "youtu.be" {
		return fmt.Errorf("%q is not a YouTube page. This table is the public-relay path and the resolver is run against YouTube; a row for another host is a row nobody has resolved", raw)
	}
	return nil
}

// HasLicence reports whether the row states a usable basis. It is exported for
// the command surface, which prints the same sentence the loader enforces.
func (c Camera) HasLicence() bool {
	lic := strings.TrimSpace(c.Licence)
	return lic != "" && !strings.HasPrefix(strings.ToUpper(lic), licenceUnrecorded)
}

// Redacted is the camera's stream URL as the console may print it.
//
// A resolved HLS playlist is a signed URL that carries the resolver's IP
// address in its path. The full URL is stored in the run row by design, like
// any camera address, but a console line and a screenshot do not need it: this
// keeps the scheme and host and drops everything after, so the line says where
// the stream is from and stops there.
func Redacted(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "(not a URL)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// Resolver turns a camera row into a stream URL by running yt-dlp.
//
// Every dependency is a field so the tests can supply it: on a machine with no
// network the resolver is still fully exercised, and a resolver only ever run
// against the real thing is a resolver nobody has watched fail.
type Resolver struct {
	// YtDlp is the binary's name or path. Empty means the SP_POC_YTDLP
	// environment variable, then a PATH lookup.
	YtDlp string
	// Format is the yt-dlp format selection. Empty means DefaultFormat.
	Format string
	// JSRuntime names the JavaScript runtime yt-dlp should use ("node" or
	// "deno"). Empty means auto-detect; "none" disables the flag entirely.
	JSRuntime string
	// LookPath resolves a binary name. Empty means exec.LookPath.
	LookPath func(string) (string, error)
	// Run executes the resolver. Empty means exec with a context.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DefaultFormat is the selection this module resolves with, and why it is not
// `best` deserves saying because the plan named `best`.
//
// `-f best` asks yt-dlp for the best PRE-MERGED format: one file carrying both
// video and audio. A live segmented stream has no such format — its formats
// are all HLS playlists, video-only and audio-only — so `-f best` answers
// "Requested format is not available" against every YouTube live cam on earth.
// The pipeline reads frames and drops audio, so video-only is the correct
// selection anyway: `bestvideo` is the highest video rendition the stream
// publishes, which for these cameras is 1080p30.
const DefaultFormat = "bestvideo"

// EnvYtDlp is the environment variable that names the resolver binary.
const EnvYtDlp = "SP_POC_YTDLP"

func (r Resolver) lookPath(name string) (string, error) {
	if r.LookPath != nil {
		return r.LookPath(name)
	}
	return exec.LookPath(name)
}

func (r Resolver) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Run != nil {
		return r.Run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}

func (r Resolver) format() string {
	if strings.TrimSpace(r.Format) == "" {
		return DefaultFormat
	}
	return strings.TrimSpace(r.Format)
}

func (r Resolver) jsRuntime() string {
	if r.JSRuntime == "none" {
		return ""
	}
	if r.JSRuntime != "" {
		return r.JSRuntime
	}
	// YouTube's extraction now needs a JS runtime and yt-dlp says so in a
	// warning rather than an error, which is the worst of both: the resolve
	// can still work with a reduced format list. Auto-detect rather than
	// demand, because a machine with deno and a machine with node and a
	// machine with neither all exist, and the third still resolves.
	for _, candidate := range []string{"deno", "node"} {
		if _, err := r.lookPath(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// Resolve runs yt-dlp against the camera's public page and returns the
// stream URL.
//
// Four refusals, all by name and none silent:
//
//   - no yt-dlp: the command and the install line are printed. There is no
//     fallback to a guessed manifest URL, because a guessed URL is a stream
//     nobody resolved.
//   - the command fails: its own words are carried in the error, because
//     "the camera is offline", "this is members-only" and "yt-dlp is too old
//     for this YouTube change" are three different problems and only the tool
//     can tell them apart.
//   - the output is not exactly one non-empty line: a merged pair (video and
//     audio) is not a mistake to silently take the first of — it is a format
//     selection this resolver did not ask for.
//   - the URL is not https, or carries credentials inline: internal/source
//     refuses the second by design, and the first would put a plaintext
//     stream in the run row.
func (r Resolver) Resolve(ctx context.Context, c Camera) (string, error) {
	if err := validateRow(c); err != nil {
		return "", err
	}
	name := strings.TrimSpace(r.YtDlp)
	if name == "" {
		name = strings.TrimSpace(os.Getenv(EnvYtDlp))
	}
	if name == "" {
		name = "yt-dlp"
	}
	path, err := r.lookPath(name)
	if err != nil {
		return "", fmt.Errorf("cams: %w: %v\n\n"+
			"  yt-dlp resolves a public YouTube page to its HLS playlist, and it is the only\n"+
			"  thing this module asks to touch the camera's page. Install it:\n"+
			"    winget install --id yt-dlp.yt-dlp -e\n"+
			"  or point SP_POC_YTDLP at the binary you already have.", errUnresolvedTool, err)
	}
	args := []string{}
	if rt := r.jsRuntime(); rt != "" {
		args = append(args, "--js-runtimes", rt)
	}
	args = append(args, "-g", "-f", r.format(), c.URL)
	out, err := r.run(ctx, path, args...)
	if err != nil {
		return "", fmt.Errorf("cams: camera %q (%s) could not be resolved with yt-dlp: %w\n\n%s",
			c.ID, c.Name, err, indent(string(out)))
	}
	lines := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			lines = append(lines, s)
		}
	}
	if len(lines) != 1 {
		return "", fmt.Errorf("cams: camera %q resolved to %d URLs, and this resolver asked for exactly one. A second URL is a merged video+audio pair, which is a format selection nobody made:\n%s",
			c.ID, len(lines), indent(strings.Join(lines, "\n")))
	}
	stream := lines[0]
	u, perr := url.Parse(stream)
	if perr != nil {
		return "", fmt.Errorf("cams: camera %q resolved to %q, which is not a URL: %w", c.ID, Redacted(stream), perr)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("cams: camera %q resolved to a %s URL. The stream address is stored in the run row, and a plaintext stream would put that row's contents on the wire in the clear: %s",
			c.ID, u.Scheme, Redacted(stream))
	}
	if u.User != nil {
		return "", fmt.Errorf("cams: camera %q resolved to a URL carrying credentials inline. That string is stored in the run row and printed by `watch --config`, which is why internal/source refuses it: %s", c.ID, Redacted(stream))
	}
	return stream, nil
}

// Spec is the source spec for a resolved camera: kind=url, the stream as its
// argument, and the public-relay label derived on.
//
// It goes through source.Parse rather than being built by hand, so the same
// URL validation the rest of the module relies on applies here too — and the
// flag is set after the parse, not passed to it, because Parse validates a
// (kind, arg) pair and the honesty label is not part of the pair.
func Spec(c Camera, streamURL string) (source.Spec, error) {
	spec, err := source.Parse(string(source.KindURL), streamURL)
	if err != nil {
		return source.Spec{}, fmt.Errorf("cams: camera %q: %w", c.ID, err)
	}
	spec.PublicRelay = true
	return spec, nil
}

// errUnresolvedTool is the sentinel the no-yt-dlp refusal carries, so a
// caller can tell "the tool is missing" from "the camera is offline" without
// reading the sentence.
var errUnresolvedTool = errors.New("cams: no stream resolver on this machine")

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "        (nothing: the tool wrote nothing)"
	}
	return "        " + strings.ReplaceAll(s, "\n", "\n        ")
}
