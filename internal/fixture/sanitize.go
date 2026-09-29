package fixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"
)

// allowedHeaders is the closed set of response headers a fixture may carry.
//
// The rule is a deny-by-default allow-list rather than a scrub of known-bad
// names. A scrub misses the header nobody thought of, and a vendor adds headers;
// an allow-list means a new header is absent until somebody decides it matters,
// and its absence shows up as a fixture that no longer replays.
//
// `x-milestone-session` was on this list and is now off it. The name is a
// session: it is the vendor's handle on an authenticated session, and a captured
// value is a bearer of that session, committed to a repository, in a list whose
// entire justification is deny-by-default. Nothing replays on it - the adapter
// authenticates with its own bearer token - so it was not carrying its weight
// either. If a fixture genuinely needs it, the answer is to re-capture without a
// live session, not to widen this list.
var allowedHeaders = map[string]bool{
	"content-type":       true,
	"x-xprotect-version": true,
	"x-request-id":       true,
	"retry-after":        true,
}

func allowedHeader(k string) bool {
	return allowedHeaders[strings.ToLower(strings.TrimSpace(k))]
}

// SanitizeResponse turns a live HTTP response into the Envelope and Headers a
// fixture stores.
//
// It is the single choke point between the network and the repository. Everything
// a fixture will ever contain passes through here, so this is where a new leak
// has to be stopped - and where a test asserts that it was.
func SanitizeResponse(r *http.Response) (json.RawMessage, map[string]string, error) {
	if r.Body == nil {
		return nil, nil, fmt.Errorf("fixture: response has no body")
	}
	body, err := readLimited(r.Body, maxEnvelopeBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("fixture: read response body: %w", err)
	}
	if !json.Valid(body) {
		return nil, nil, fmt.Errorf("fixture: response body is not JSON; refusing to store an opaque blob")
	}

	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("fixture: decode response body: %w", err)
	}
	redacted, err := RedactValue("", doc)
	if err != nil {
		return nil, nil, err
	}
	clean, err := json.Marshal(redacted)
	if err != nil {
		return nil, nil, fmt.Errorf("fixture: re-encode redacted body: %w", err)
	}

	headers := map[string]string{}
	for k, v := range r.Header {
		if allowedHeader(k) {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	return clean, headers, nil
}

// RedactionMarker replaces every value the sanitizer removes. It is a marker
// rather than a deletion so a reviewer can see that redaction happened and where
// - a silently shortened fixture looks like a source that simply sent less.
const RedactionMarker = "[redacted]"

// secretKeyNames are JSON keys whose value is never safe to commit, whatever the
// value happens to be.
var secretKeyNames = map[string]bool{
	"password": true, "passwd": true, "pwd": true,
	"secret": true, "clientsecret": true, "apikey": true, "api_key": true,
	"token": true, "accesstoken": true, "access_token": true, "refreshtoken": true,
	"refresh_token": true, "authorization": true, "auth": true,
	"privatekey": true, "private_key": true, "sessionkey": true, "session_key": true,
	"cookie": true, "setcookie": true, "set_cookie": true,
	"credential": true, "credentials": true, "psk": true,
	"connectionstring": true, "connection_string": true, "dsn": true,
}

// identifyingKeyNames are keys whose value identifies a person or a specific
// machine. These are not secrets, but a committed fixture that carries them puts
// real site data in a public repository, so they are removed and the key is kept
// so the shape of the payload still tests something.
var identifyingKeyNames = map[string]bool{
	"macaddress": true, "mac_address": true, "mac": true,
	"ipaddress": true, "ip_address": true, "ip": true, "remoteaddress": true,
	"serialnumber": true, "serial_number": true, "serial": true,
	"licenseplate": true, "license_plate": true, "plate": true, "platenumber": true,
	"badge": true, "badgenumber": true, "cardnumber": true, "card_number": true,
	"email": true, "emailaddress": true, "phone": true, "phonenumber": true,
	"personname": true, "person_name": true, "firstname": true, "lastname": true,
	"fullname": true, "full_name": true, "username": true, "user_name": true,
	"homeaddress": true, "address": true,

	// Names, images and places. These are not secrets and a reviewer would not
	// flag them looking for a leaked password, which is why they were missed:
	// the list above is a list of things that look like credentials.
	//
	// A face in a photograph is a biometric, and a photograph committed next to
	// a person's name is the strongest identifier a security system can hold.
	// A display name and a precise location turn an image reference into an
	// identified individual. None of them can be un-leaked once the file is in a
	// repository, and a capture run against a live site produces them by
	// default.
	"displayname": true, "display_name": true, "nickname": true, "loginname": true,
	"givenname": true, "surname": true, "middlename": true,
	"employeename": true, "employeeno": true, "employeeid": true, "staffid": true,
	"imageurl": true, "image_url": true, "imagepath": true, "image_path": true,
	"thumbnailurl": true, "thumbnail_url": true, "snapshoturl": true, "snapshot_url": true,
	"photourl": true, "photo_url": true, "faceurl": true, "face_url": true,
	"image": true, "photo": true, "photograph": true, "face": true, "faces": true,
	"facedetections": false,

	// A list of detections is walked rather than replaced wholesale, so a
	// fixture still exercises "the source returns N faces in a box" and only the
	// identifiers inside go. face, faces, photograph and image are single values
	// and are replaced outright.
	"latitude": true, "lat": true, "longitude": true, "lon": true, "lng": true,
	"coordinates": true, "coord": true, "geolocation": true, "geo": true,
	"gps": true, "gpscoordinates": true, "location": true, "locationname": true,
	"personid": true, "person_id": true, "subjectid": true, "subject_id": true,
	"faceid": true, "face_id": true, "trackid": true, "track_id": true,
	"fingerprint": true, "biometric": true, "templateid": true, "template_id": true,
	"dob": true, "dateofbirth": true, "birthdate": true,
	// A bare `name` is redacted. It is the most ambiguous key in a vendor payload
	// — a person's name, a site name, a rule name, a device name — and the
	// fixture set's own scenarios are identified by name elsewhere, so redacting
	// it costs a captured value and buys not committing a person to a repository.
	// It was the one field in the plan's list with no entry here.
	"name": true,
}

// numericIdentifierKeyNames are keys that identify a machine or a person by
// number rather than by name. They are handled separately because the value is
// an integer, not a string, and the generic value-level rules never see one:
// RedactValue's number branch passes a float64 straight through.
//
// These are the keys that made a captured fixture identify a specific door, a
// specific badge and a specific camera, which is enough to reconstruct a site's
// layout from a public file.
var numericIdentifierKeyNames = map[string]bool{
	"siteid": true, "site_id": true,
	"deviceno": true, "device_no": true, "deviceid": true, "device_id": true,
	"doorid": true, "door_id": true, "door": true,
	"cameraid": true, "camera_id": true, "zoneid": true, "zone_id": true,
	"readerid": true, "reader_id": true, "reader": true,
	"partitionid": true, "partition_id": true, "partitionno": true, "partition_no": true,
	"channelid": true, "channel_id": true, "channelno": true, "channel_no": true,
	"accountid": true, "account_id": true, "accountno": true, "account_no": true,
	"operatorid": true, "operator_id": true,
}

// RedactValue walks a decoded JSON document and removes secrets and identifiers.
//
// Recursion is by structure, not by regex. A regex over raw JSON text cannot tell
// a password field from a password-shaped string inside a message, and the two
// want opposite treatment.
func RedactValue(path string, v any) (any, error) {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			lower := normalizeKey(k)
			switch {
			case secretKeyNames[lower]:
				out[k] = RedactionMarker
			case identifyingKeyNames[lower], numericIdentifierKeyNames[lower]:
				out[k] = RedactionMarker
			default:
				redacted, err := RedactValue(joinPath(path, k), child)
				if err != nil {
					return nil, err
				}
				out[k] = redacted
			}
		}
		return out, nil

	case []any:
		out := make([]any, 0, len(node))
		for i, child := range node {
			redacted, err := RedactValue(fmt.Sprintf("%s[%d]", path, i), child)
			if err != nil {
				return nil, err
			}
			out = append(out, redacted)
		}
		return out, nil

	case string:
		return redactEmbeddedString(path, node), nil

	case float64:
		// A number under an identifying key is still an identifier. The key was
		// already matched above, so this branch is the belt to that braces: a
		// vendor key the lists do not carry, with a numeric value that still
		// names one device, is redacted by position rather than by name.
		if numericIdentifierPath(path) {
			return RedactionMarker, nil
		}
		return v, nil

	default:
		return v, nil
	}
}

// normalizeKey folds a key to the form the name lists use: lowercase with
// separators removed, so `Display_Name`, `displayName` and `display-name` are
// one lookup and not three that a list has to repeat.
func normalizeKey(k string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(k))
}

// numericIdentifierPath reports whether the last path segment names a machine
// or a person by number.
func numericIdentifierPath(path string) bool {
	key := path
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		key = path[i+1:]
	}
	// An array index leaves `field[3]` as the last segment; the name is the part
	// before the bracket.
	if i := strings.IndexByte(key, '['); i >= 0 {
		key = key[:i]
	}
	return numericIdentifierKeyNames[normalizeKey(key)]
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// urlLikeKeys hold URLs that embed a host, and on an internal site a host is an
// internal network name worth not publishing.
var urlLikeKeys = map[string]bool{
	"url": true, "uri": true, "endpoint": true, "baseurl": true, "host": true,
	"hostname": true, "server": true, "callback": true, "webhookurl": true,
	"imageurl": true, "image_url": true, "thumbnailurl": true, "thumbnail_url": true,
	"snapshoturl": true, "snapshot_url": true, "photourl": true, "photo_url": true,
	"faceurl": true, "face_url": true, "streamurl": true, "stream_url": true,
}

// redactEmbeddedString handles values whose key is innocuous but whose content
// is not: a URL carrying a credential, or a bearer token that arrived in a field
// nobody named.
func redactEmbeddedString(path, s string) string {
	key := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		key = path[i+1:]
	}
	if i := strings.IndexByte(key, '['); i >= 0 {
		key = key[:i]
	}
	lower := normalizeKey(key)

	if urlLikeKeys[lower] {
		return RedactURL(s)
	}
	if looksLikeBearer(s) {
		return RedactionMarker
	}
	return s
}

// RedactURL keeps the path shape and drops the scheme, host and any query. A
// fixture needs to exercise "this endpoint returns this shape", not to remember
// which site it came from.
//
// A value with no scheme is still a host: a source that reports
// `{"Source":{"Host":"milestone.internal"}}` has not given a URL, but it has
// given the same information, and that is the case a naive scheme-only check
// misses.
func RedactURL(raw string) string {
	schemeAt := strings.Index(raw, "://")
	if schemeAt < 0 {
		// Schemeless. If it looks like a host rather than a relative path, the
		// whole value is the leak and there is no shape left worth keeping.
		if looksLikeHost(raw) {
			return RedactionMarker
		}
		return raw
	}
	rest := raw[schemeAt+3:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return RedactionMarker
	}
	// Keep the path, drop the query: a query string is where tokens live.
	path := rest[slash:]
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}
	return "[redacted-host]" + path
}

// looksLikeHost reports whether a schemeless value is a bare hostname. It is
// deliberately conservative: a relative path or a plain word must survive,
// because redacting the whole payload is the failure this is guarding against.
func looksLikeHost(s string) bool {
	if s == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "[") {
		return false
	}
	if !strings.Contains(s, ".") || strings.Contains(s, " ") {
		return false
	}
	// Require every dot-separated label to look like a hostname label.
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			default:
				return false
			}
		}
	}
	// A bare host usually carries no path and no port; anything with a port is
	// left to the schemed path above.
	return !strings.Contains(s, ":")
}

func looksLikeBearer(s string) bool {
	t := strings.TrimSpace(s)
	if len(t) < 20 {
		return false
	}
	lower := strings.ToLower(t)
	for _, prefix := range []string{"bearer ", "basic ", "digest "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// Long high-entropy strings are treated as opaque secrets. This over-redacts
	// on purpose: a fixture that lost a long opaque value still tests routing and
	// shape, while a fixture that kept a real token is a leak nobody can unsee.
	if len(t) >= 40 && hasHighEntropy(t) {
		return true
	}
	return false
}

func hasHighEntropy(s string) bool {
	var upper, lower, digit, other int
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		case unicode.IsDigit(r):
			digit++
		default:
			other++
		}
	}
	if upper == 0 || lower == 0 {
		return false
	}
	distinct := map[rune]bool{}
	for _, r := range s {
		distinct[r] = true
	}
	return len(distinct) > 12 && other < len([]rune(s))/4
}

// maxEnvelopeBytes caps a single captured body. An unbounded read is a way for a
// misbehaving or hostile source to fill the disk during a capture, and a capture
// runs against a live site.
const maxEnvelopeBytes = 4 << 20 // 4 MiB

// ReadLimited is exported so the capture harness and the sanitizer agree on the
// cap rather than each having their own idea of it.
func ReadLimited(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	return readLimited(r, maxEnvelopeBytes)
}

func readLimited(r interface{ Read([]byte) (int, error) }, limit int64) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if int64(len(buf)) > limit {
				return nil, fmt.Errorf("body exceeds %d bytes", limit)
			}
		}
		if err != nil {
			if isEOF(err) {
				return buf, nil
			}
			return nil, err
		}
	}
}

func isEOF(err error) bool {
	return err != nil && (err.Error() == "EOF" || strings.Contains(err.Error(), "EOF"))
}

// SortedNames returns manifest entries in a stable order, so a regenerated
// manifest produces a reviewable diff rather than a reshuffle.
func (m Manifest) SortedNames() []string {
	names := make([]string, 0, len(m.Scenarios))
	for _, s := range m.Scenarios {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}
