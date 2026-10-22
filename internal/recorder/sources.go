package recorder

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
)

// The camera list.
//
// # Why it is a FILE and not a table
//
// The list carries credentials. A camera's password in a table is a camera's
// password in every backup of that table, in every dump of it, and in every
// replica of it, and the product already has a table for what it knows about
// a device — the registry — and that table deliberately holds a REFERENCE to
// a credential and not the credential.
//
// So the DEVICE is the registry's and the STREAM is configuration, and the
// two are joined by the device's id. The file is the customer's own, on the
// customer's own disk, with the customer's own permissions.
//
// # The permission rule
//
// The file is refused on POSIX when it is readable by anyone other than its
// owner, because a camera credential on a world-readable file is a camera
// credential on every account on the host. On Windows the mode bits are
// advisory and the ACL is the control, so the same condition is a WARNING and
// not a refusal — the same split the secret key store makes, for the same
// reason: refusing an advisory condition makes the product unstartable on
// Windows, and silently accepting it makes a genuinely exposed file look
// protected.

// ErrNoSources is a recorder with no camera list.
var ErrNoSources = errors.New("recorder: no camera list")

// Sources is the recorder's camera list.
type Sources struct {
	list []Source
}

// sourceFile is the file's own shape.
//
// The JSON names are the same as the fields, in camelCase, because a file a
// person edits should read like the documentation.
type sourceFile struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	DeviceID string `json:"deviceId"`
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// LoadSources reads the camera list from a file.
//
// The file must exist, must parse, and every entry in it must be complete: a
// camera with no device id cannot be indexed, and a camera with no URL has
// nothing to record. An entry that is wrong is a REFUSAL and not a warning,
// because a recorder that silently skips a camera reports a site as
// recording while the camera is dark.
func LoadSources(path string) (*Sources, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrNoSources
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("recorder: the camera list at %s: %w", path, err)
	}
	if problem := sourcesPermissionProblem(info.Mode().Perm()); problem != "" {
		if permissionBitsMeanSomething() {
			return nil, fmt.Errorf("recorder: the camera list at %s is %s; it holds camera "+
				"credentials and must be readable by its owner alone", path, problem)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("recorder: read the camera list at %s: %w", path, err)
	}
	var entries []sourceFile
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("recorder: the camera list at %s is not a JSON array of cameras: %w", path, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: the camera list at %s is empty", ErrNoSources, path)
	}
	out := &Sources{}
	for i, e := range entries {
		src := Source{
			TenantID: strings.TrimSpace(e.TenantID),
			SiteID:   strings.TrimSpace(e.SiteID),
			DeviceID: strings.TrimSpace(e.DeviceID),
			URL:      strings.TrimSpace(e.URL),
			User:     e.User,
			Password: e.Password,
		}
		if err := src.validate(); err != nil {
			return nil, fmt.Errorf("recorder: the camera list at %s, camera %d: %w", path, i+1, err)
		}
		out.list = append(out.list, src)
	}
	return out, nil
}

// All returns the cameras.
func (s *Sources) All() []Source {
	if s == nil {
		return nil
	}
	return append([]Source(nil), s.list...)
}

// Len is how many cameras the list holds.
func (s *Sources) Len() int {
	if s == nil {
		return 0
	}
	return len(s.list)
}

// validate refuses an incomplete camera.
//
// The four refusals are four different mistakes, and each is named: a camera
// with no tenant cannot be indexed, a camera with no site cannot be scoped,
// a camera with no device id cannot be joined to the registry, and a camera
// with no URL has nothing to record.
func (s Source) validate() error {
	if s.TenantID == "" {
		return errors.New("the camera has no tenantId")
	}
	if s.SiteID == "" {
		return errors.New("the camera has no siteId")
	}
	if s.DeviceID == "" {
		return errors.New("the camera has no deviceId")
	}
	if s.URL == "" {
		return errors.New("the camera has no url")
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return fmt.Errorf("the camera's url does not parse: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "rtsp", "rtsps":
	default:
		return fmt.Errorf("the camera's url is %q: this recorder speaks RTSP", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("the camera's url has no host")
	}
	return nil
}

// sourcesPermissionProblem reports why a camera list's mode is unacceptable,
// or "" when it is acceptable.
//
// Pure over the mode, so the rule is testable on a host whose mode bits
// enforce nothing — which is the only way to prove the control on the very
// machine that does not enforce it.
func sourcesPermissionProblem(mode os.FileMode) string {
	if mode.Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("readable by anyone other than its owner (mode %04o)", mode.Perm())
}

// permissionBitsMeanSomething reports whether this platform decides file
// access by the mode bits.
//
// True on POSIX; false on Windows and Plan 9, where the mode is advisory and
// the ACL is the real control.
func permissionBitsMeanSomething() bool {
	return runtime.GOOS != "windows" && runtime.GOOS != "plan9"
}
