package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// The credential purposes. The vocabulary is closed and it is the same list the
// schema's CHECK carries, because a free-text purpose becomes "the camera
// login" and then "the camera login (old)" and then two credentials for one
// device with nobody able to say which one is live.
const (
	// PurposeRTSP is the credential that opens the device's stream.
	PurposeRTSP = "rtsp"
	// PurposeONVIF is the credential that opens the device's control service.
	PurposeONVIF = "onvif"
	// PurposeFTP is the credential that opens the device's file service.
	PurposeFTP = "ftp"
	// PurposeOther is anything else, named by the detail.
	PurposeOther = "other"
)

// Purposes is the closed vocabulary.
var Purposes = []string{PurposeRTSP, PurposeONVIF, PurposeFTP, PurposeOther}

// The refusals the store makes.
var (
	// ErrNoPool is a store with no database behind it.
	ErrNoPool = errors.New("device: no pool")
	// ErrNoScope is a read with no site scope.
	ErrNoScope = errors.New("device: a site scope is required")
	// ErrNotFound is a device the registry does not hold.
	ErrNotFound = errors.New("device: no such device")
	// ErrUnknownPurpose is a purpose outside the closed vocabulary.
	ErrUnknownPurpose = errors.New("device: a credential carries a purpose outside the closed vocabulary")
	// ErrNoSecretRef is a credential with no keystore reference.
	ErrNoSecretRef = errors.New("device: a credential has no keystore reference")
)

// Device is one row of the registry.
//
// The identity fields are what the device says about itself; the Manifest is
// what a probe established about it. The two are kept apart on purpose: a
// model name is not a measurement, and a column that mixed them would let a
// reader believe a capability because a string looked familiar.
type Device struct {
	TenantID    string
	DeviceID    string
	SiteID      string
	ZoneID      string
	Kind        string
	DisplayName string
	Enabled     bool
	Make        string
	Model       string
	Firmware    string
	// ONVIFDeviceID is the identifier the device reports for itself.
	ONVIFDeviceID string
	// ONVIFBaseURL is the base address of the device's control service.
	//
	// It is a host and a port and nothing else. The credential is in the
	// keystore and is named by CredentialRef, so an address in this table is
	// not a secret and a log line may carry it.
	//
	// Empty means the product does not know where the device is. The probe
	// refuses in that case rather than guessing an address and measuring
	// whatever happened to answer.
	ONVIFBaseURL string
	// RTSPRef names the stream configuration. It is a reference, not a URL: a
	// URL with a password in it is a password in a log.
	RTSPRef string
	// CredentialRef names the keystore reference for the device's primary
	// credential. It is a pointer, never a secret.
	CredentialRef string
	// SubstreamProfile is the substream the recorder should use.
	SubstreamProfile string
	// Manifest is the measured capability set, or nil when the device has
	// never been probed.
	Manifest *Manifest
}

// Credential is one row of the credential INVENTORY.
//
// The secret is not here. `SecretRef` names a row in the keystore, and the
// column is called that on purpose so that nobody reading the table mistakes
// the pointer for the secret.
type Credential struct {
	TenantID   string
	DeviceID   string
	Purpose    string
	SecretRef  string
	EnrolledAt time.Time
	// LastVerifiedAt is when the credential was last checked against the
	// device itself. Nil means it has never been verified, which is not the
	// same as working.
	LastVerifiedAt *time.Time
}

// Service is the registry's store.
type Service struct {
	Pool *dbx.Pool
}

// ---------------------------------------------------------------------------
// The reads
// ---------------------------------------------------------------------------

// listSQL reads the devices a principal may see.
//
// The site filter is in the SQL as well as in the row-level-security policy.
// The policy is the boundary; the filter is the query, and a query that
// returns a row the policy would have refused is a row the reader did not ask
// for.
const listSQL = `
SELECT device_id, site_id, COALESCE(zone_id, ''), kind, display_name, enabled,
       COALESCE(make, ''), COALESCE(model, ''), COALESCE(firmware, ''),
       COALESCE(onvif_device_id, ''), COALESCE(onvif_base_url, ''),
       COALESCE(rtsp_ref, ''),
       COALESCE(credential_ref, ''), COALESCE(substream_profile, ''),
       capabilities
  FROM devices
 WHERE tenant_id = $1
   AND site_id = ANY($2)
 ORDER BY device_id`

// List returns the devices a principal may see at the given sites.
//
// A principal with no site scope sees nothing. The empty scope is not "all
// sites" — it is "no sites", and the difference is the whole of the site
// isolation policy.
func (s *Service) List(ctx context.Context, tenantID string, sites []string) ([]Device, error) {
	if s == nil || s.Pool == nil {
		return nil, ErrNoPool
	}
	if len(sites) == 0 {
		return nil, ErrNoScope
	}
	rows, err := s.Pool.WithSites(tenantID, "device", sites).Query(ctx, listSQL, tenantID, sites)
	if err != nil {
		return nil, fmt.Errorf("device: list the devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("device: iterate the devices: %w", err)
	}
	return out, nil
}

// manifestSQL reads one device's measured manifest.
const manifestSQL = `
SELECT capabilities
  FROM devices
 WHERE tenant_id = $1 AND site_id = ANY($2) AND device_id = $3`

// Manifest returns one device's measured manifest.
//
// A device that has never been probed has no manifest, and the error says so.
// It is not an empty manifest: an empty manifest is a probe that found
// nothing, and the difference is the difference between "we did not look" and
// "we looked and there was nothing", which is a distinction the whole
// package exists to keep.
func (s *Service) Manifest(ctx context.Context, tenantID string, sites []string, deviceID string) (Manifest, error) {
	if s == nil || s.Pool == nil {
		return Manifest{}, ErrNoPool
	}
	if len(sites) == 0 {
		return Manifest{}, ErrNoScope
	}
	var raw []byte
	err := s.Pool.WithSites(tenantID, "device", sites).
		QueryRow(ctx, manifestSQL, tenantID, sites, deviceID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Manifest{}, ErrNotFound
		}
		return Manifest{}, fmt.Errorf("device: read the manifest of %s: %w", deviceID, err)
	}
	if len(raw) == 0 {
		return Manifest{}, ErrNotFound
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("device: decode the manifest of %s: %w", deviceID, err)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// The writes
// ---------------------------------------------------------------------------

// saveManifestSQL writes one device's measured manifest.
//
// The tenant predicate is in the WHERE because the table is tenant-scoped and
// invariant 5 says a mutation of a tenant-scoped table names its tenant. The
// site predicate is there for the same reason the site policy is: a probe
// writes the manifest of the device it probed, and nothing else.
const saveManifestSQL = `
UPDATE devices
   SET capabilities = $4
 WHERE tenant_id = $1 AND site_id = $2 AND device_id = $3`

// SaveManifest writes a measured manifest.
//
// The manifest is validated first, so a manifest that claims a capability
// with no probe behind it never reaches the database. The store is the last
// line of that defence rather than the only one.
func (s *Service) SaveManifest(ctx context.Context, m Manifest) error {
	if s == nil || s.Pool == nil {
		return ErrNoPool
	}
	if err := m.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("device: encode the manifest of %s: %w", m.DeviceID, err)
	}
	tag, err := s.Pool.WithSites(m.TenantID, "device", []string{m.SiteID}).
		Exec(ctx, saveManifestSQL, m.TenantID, m.SiteID, m.DeviceID, raw)
	if err != nil {
		return fmt.Errorf("device: save the manifest of %s: %w", m.DeviceID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, m.DeviceID)
	}
	return nil
}

// registerSQL records a device.
//
// The columns that carry a capability are NOT written here: `capabilities` is
// written only by a probe, through `SaveManifest`, so that a device can never
// acquire a capability by being registered.
const registerSQL = `
INSERT INTO devices
    (tenant_id, device_id, site_id, zone_id, kind, display_name, enabled,
     make, model, firmware, onvif_device_id, onvif_base_url, rtsp_ref,
     credential_ref, substream_profile)
VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''),
        NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''),
        NULLIF($14, ''), NULLIF($15, ''))
ON CONFLICT (tenant_id, device_id) DO UPDATE
   SET site_id = EXCLUDED.site_id,
       zone_id = EXCLUDED.zone_id,
       kind = EXCLUDED.kind,
       display_name = EXCLUDED.display_name,
       enabled = EXCLUDED.enabled,
       make = EXCLUDED.make,
       model = EXCLUDED.model,
       firmware = EXCLUDED.firmware,
       onvif_device_id = EXCLUDED.onvif_device_id,
       onvif_base_url = EXCLUDED.onvif_base_url,
       rtsp_ref = EXCLUDED.rtsp_ref,
       credential_ref = EXCLUDED.credential_ref,
       substream_profile = EXCLUDED.substream_profile
 WHERE devices.tenant_id = $1 AND devices.site_id = $3`

// Register records a device in the registry.
//
// It does not touch the capability manifest, and that is the point: a device
// acquires capabilities by being probed, not by being registered.
func (s *Service) Register(ctx context.Context, d Device) error {
	if s == nil || s.Pool == nil {
		return ErrNoPool
	}
	if d.TenantID == "" || d.DeviceID == "" || d.SiteID == "" {
		return errors.New("device: a registration needs a tenant, a device and a site")
	}
	_, err := s.Pool.WithSites(d.TenantID, "device", []string{d.SiteID}).Exec(ctx, registerSQL,
		d.TenantID, d.DeviceID, d.SiteID, d.ZoneID, d.Kind, d.DisplayName, d.Enabled,
		d.Make, d.Model, d.Firmware, d.ONVIFDeviceID, d.ONVIFBaseURL, d.RTSPRef,
		d.CredentialRef, d.SubstreamProfile)
	if err != nil {
		return fmt.Errorf("device: register %s: %w", d.DeviceID, err)
	}
	return nil
}

// credentialsSQL reads one device's credential inventory.
const credentialsSQL = `
SELECT purpose, secret_ref, enrolled_at, last_verified_at
  FROM device_credentials
 WHERE tenant_id = $1 AND device_id = $2
 ORDER BY purpose`

// Credentials returns one device's credential inventory.
//
// The inventory is the whole of what we hold for a device, so rotation and
// revocation have somewhere to read from. The secrets are in the keystore and
// the references are here.
func (s *Service) Credentials(ctx context.Context, tenantID, deviceID string) ([]Credential, error) {
	if s == nil || s.Pool == nil {
		return nil, ErrNoPool
	}
	rows, err := s.Pool.For(tenantID, "device").Query(ctx, credentialsSQL, tenantID, deviceID)
	if err != nil {
		return nil, fmt.Errorf("device: read the credentials of %s: %w", deviceID, err)
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		c.TenantID = tenantID
		c.DeviceID = deviceID
		var verified *time.Time
		if err := rows.Scan(&c.Purpose, &c.SecretRef, &c.EnrolledAt, &verified); err != nil {
			return nil, fmt.Errorf("device: scan a credential of %s: %w", deviceID, err)
		}
		c.LastVerifiedAt = verified
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("device: iterate the credentials of %s: %w", deviceID, err)
	}
	return out, nil
}

// enrolCredentialSQL records a credential reference.
//
//nolint:gosec // the column is a POINTER into the keystore; the secret is never in the table
const enrolCredentialSQL = `
INSERT INTO device_credentials
    (tenant_id, device_id, purpose, secret_ref, enrolled_at, last_verified_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, device_id, purpose) DO UPDATE
   SET secret_ref = EXCLUDED.secret_ref,
       last_verified_at = EXCLUDED.last_verified_at
 WHERE device_credentials.tenant_id = $1`

// EnrolCredential records a credential reference for a device.
//
// The reference is not the secret, and the method name says "enrol" rather
// than "set password" on purpose: what is written here is a pointer, and the
// keystore holds the thing the pointer names.
func (s *Service) EnrolCredential(ctx context.Context, c Credential) error {
	if s == nil || s.Pool == nil {
		return ErrNoPool
	}
	if c.TenantID == "" || c.DeviceID == "" {
		return errors.New("device: a credential needs a tenant and a device")
	}
	if !knownPurpose(c.Purpose) {
		return fmt.Errorf("%w: %q", ErrUnknownPurpose, c.Purpose)
	}
	if c.SecretRef == "" {
		return ErrNoSecretRef
	}
	enrolled := c.EnrolledAt
	if enrolled.IsZero() {
		enrolled = time.Now().UTC()
	}
	_, err := s.Pool.For(c.TenantID, "device").Exec(ctx, enrolCredentialSQL,
		c.TenantID, c.DeviceID, c.Purpose, c.SecretRef, enrolled, c.LastVerifiedAt)
	if err != nil {
		return fmt.Errorf("device: enrol the %s credential of %s: %w", c.Purpose, c.DeviceID, err)
	}
	return nil
}

// knownPurpose reports whether a purpose is in the closed vocabulary.
func knownPurpose(p string) bool {
	for _, candidate := range Purposes {
		if candidate == p {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The scan
// ---------------------------------------------------------------------------

// rowScanner is what pgx.Rows satisfies, so the scan helper does not depend on
// the concrete type.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanDevice reads one device row.
func scanDevice(rows rowScanner) (Device, error) {
	var d Device
	var capabilities []byte
	if err := rows.Scan(
		&d.DeviceID, &d.SiteID, &d.ZoneID, &d.Kind, &d.DisplayName, &d.Enabled,
		&d.Make, &d.Model, &d.Firmware, &d.ONVIFDeviceID, &d.ONVIFBaseURL, &d.RTSPRef,
		&d.CredentialRef, &d.SubstreamProfile, &capabilities); err != nil {
		return Device{}, fmt.Errorf("device: scan a device: %w", err)
	}
	if len(capabilities) > 0 {
		var m Manifest
		if err := json.Unmarshal(capabilities, &m); err != nil {
			return Device{}, fmt.Errorf("device: decode the manifest of %s: %w", d.DeviceID, err)
		}
		d.Manifest = &m
	}
	return d, nil
}
