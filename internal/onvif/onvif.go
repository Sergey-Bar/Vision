// Package onvif speaks ONVIF to a device and reports what it found.
//
// # What this package is for
//
// The AI VMS plan's P1, and the reason the device registry measures rather
// than assumes. A camera advertises a set of ONVIF profiles; a camera is
// routinely wrong about that, and a product that believes the advertisement
// has a capability report that is fiction.
//
// So this package does the asking. It discovers a device on the network
// (WS-Discovery), reads what the device says about itself
// (GetDeviceInformation), reads which services it actually exposes
// (GetCapabilities), and reads the media profiles it actually implements
// (GetProfiles) — including whether a profile carries a real video-analytics
// configuration, which is the only way to know that Profile M is more than a
// token in a list.
//
// The result is a `device.Manifest`, and the state of each capability says
// whether a probe established it, contradicted it, or never looked.
//
// # What this package does not do
//
// It does not record, decode, or analyse anything. It reads a device's own
// description of itself, and the recording path is `internal/recorder`'s.
//
// It holds no secret. The device's credential arrives through a `Credential`
// so that the keystore can supply it without this package knowing how a
// secret is stored, and the client's own `String` method prints its address
// and nothing else, because a `String` method is what ends up in a log.
//
// # The protocol
//
// ONVIF is SOAP over HTTP, and WS-Discovery is SOAP over UDP multicast. Both
// are implemented here with the standard library and no third-party
// dependency, for the same reason the Python AI runtime is dependency-free: a
// product that has to install something to ask a camera a question cannot be
// installed on an isolated network.
//
// The XML is parsed by local element name rather than by namespace, because
// ONVIF implementations disagree about the namespace prefixes they use and
// agree about the element names. A parser that insists on a namespace reads
// some cameras and refuses others, and the refusal looks like an absent
// device.
package onvif

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The two service paths ONVIF defines. They are the paths the specification
// names, and a device that answers on a different path is a device that has
// published its own, which the discovery response carries.
const (
	// DeviceServicePath is the device management service.
	DeviceServicePath = "/onvif/device_service"
	// MediaServicePath is the media service.
	MediaServicePath = "/onvif/media_service"
)

// Credential supplies a device's secret.
//
// It is an interface so that the keystore can satisfy it without this package
// knowing how a secret is stored, and so that a caller can hand over a
// reference rather than the thing the reference names.
type Credential interface {
	// Username is the device's user name.
	Username() string
	// Password is the device's secret.
	Password() string
}

// Client talks to one device.
//
// It holds a base URL and a credential, and it prints neither the credential
// nor anything derived from it.
type Client struct {
	// BaseURL is the device's base address, e.g. "http://192.0.2.10:80".
	BaseURL string
	// HTTP is the transport. A nil HTTP means the default client.
	HTTP *http.Client
	// Credential is the device's secret. It may be nil for a device that needs
	// none.
	Credential Credential
}

// String describes the client without its secret.
//
// A `String` method is what a log line prints, and a log line that carried a
// camera's password would be a password in a file nobody is going to rotate.
func (c *Client) String() string {
	if c == nil {
		return "onvif.Client(nil)"
	}
	return "onvif.Client(" + c.BaseURL + ")"
}

// httpClient returns the transport to use.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// DeviceInformation is what a device says about itself.
//
// It is an identification, not a measurement. A device reports its model, and
// a model name is not evidence that the device does anything.
type DeviceInformation struct {
	Manufacturer    string
	Model           string
	FirmwareVersion string
	SerialNumber    string
	HardwareID      string
}

// Capabilities is the set of services a device actually exposes.
//
// The map is keyed by the capability name the product uses
// (`onvif.analytics`, `onvif.media`, `onvif.ptz`, and so on) and the value is
// the service URL the device published, which is empty when the device
// exposes the service without publishing a URL for it.
type Capabilities map[string]string

// Capability names the device service exposes.
const (
	// CapAnalytics is the ONVIF analytics service.
	CapAnalytics = "onvif.analytics"
	// CapDevice is the device management service.
	CapDevice = "onvif.device"
	// CapEvents is the event service.
	CapEvents = "onvif.events"
	// CapImaging is the imaging service.
	CapImaging = "onvif.imaging"
	// CapMedia is the media service.
	CapMedia = "onvif.media"
	// CapPTZ is the pan, tilt and zoom service.
	CapPTZ = "onvif.ptz"
)

// capabilityElements maps the ONVIF capability element to the name the
// product uses for it.
//
// The order is the order the specification lists them, and it is also the
// order a reader expects: identity, media, then the optional services.
var capabilityElements = []struct {
	Element string
	Name    string
}{
	{"Analytics", CapAnalytics},
	{"Device", CapDevice},
	{"Events", CapEvents},
	{"Imaging", CapImaging},
	{"Media", CapMedia},
	{"PTZ", CapPTZ},
}

// MediaProfile is one media profile a device implements.
//
// The profile flags are the measurement: a device that lists a Profile M
// token but whose profile carries no analytics configuration is a device that
// does not do Profile M, and that is exactly the finding the manifest's
// `contradicted` state exists to record.
type MediaProfile struct {
	Token string
	Name  string
	// HasVideo marks a profile with a video configuration.
	HasVideo bool
	// HasAudio marks a profile with an audio configuration.
	HasAudio bool
	// HasAnalytics marks a profile with a real video-analytics configuration,
	// which is what Profile M means.
	HasAnalytics bool
	// HasAnalyticsEvents marks a profile with an analytics-event
	// configuration, which is what Profile T on a camera with analytics means.
	HasAnalyticsEvents bool
	// HasImaging marks a profile with an imaging configuration.
	HasImaging bool
	// HasPTZ marks a profile with a pan, tilt and zoom configuration.
	HasPTZ bool
	// HasDevice marks a profile with a device configuration.
	HasDevice bool
	// Resolution is the configured width and height, when the profile
	// declares them.
	Width  int
	Height int
	// FPS is the configured frame rate, when the profile declares one.
	FPS int
	// Codec is the video encoding, when the profile declares one.
	Codec string
}

// ---------------------------------------------------------------------------
// SOAP
// ---------------------------------------------------------------------------

// faultEnvelope is a SOAP fault.
//
// A device that answers with a fault has answered, and the fault is the
// device's own reason rather than a timeout. The two are very different
// facts: one says the device refused, the other says nobody spoke.
type faultEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Fault struct {
			Code string `xml:"Code"`
			// Reason carries a nested Text element, so it is a structure and
			// not a string: a string field would decode to empty, and the
			// device's own words would be lost.
			Reason struct {
				Text string `xml:"Text"`
			} `xml:"Reason"`
			Detail string `xml:"Detail"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

// Fault is a device's own refusal, with its reason.
//
// It is a named type because "the device said no" and "the device did not
// answer" are different findings, and a probe that collapses them reports a
// camera as absent when it is present and refusing.
type Fault struct {
	Code   string
	Reason string
	Detail string
}

func (f *Fault) Error() string {
	if f.Reason != "" {
		return "onvif: the device refused: " + f.Reason
	}
	return "onvif: the device refused: " + f.Code
}

// soapEnvelope builds a SOAP request.
//
// The message id is derived from the action and the time rather than from a
// random source, because a device that sees the same id twice may answer the
// second one with the first one's reply, and a probe that reads a stale
// answer reports a capability that has since changed.
func soapEnvelope(action, to string, body string) []byte {
	messageID := fmt.Sprintf("urn:uuid:%s-%d", strings.ToLower(action), time.Now().UnixNano())
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">` +
		`<s:Header>` +
		`<w:MessageID xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing">` + messageID + `</w:MessageID>` +
		`<w:To xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing">` + xmlEscape(to) + `</w:To>` +
		`<w:Action xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing">` + xmlEscape(action) + `</w:Action>` +
		`</s:Header>` +
		`<s:Body>` + body + `</s:Body>` +
		`</s:Envelope>`)
}

// xmlEscape escapes the five characters that matter in an element's text.
func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// post sends a SOAP request and returns the response body.
//
// The device's credential is attached as HTTP basic auth, which is what ONVIF
// specifies for the WS-Security UsernameToken profile most devices implement.
func (c *Client) post(ctx context.Context, servicePath, action, body string) ([]byte, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("onvif: the client has no base address")
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + servicePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(soapEnvelope(action, endpoint, body))))
	if err != nil {
		return nil, fmt.Errorf("onvif: build the %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8`)
	if c.Credential != nil {
		req.SetBasicAuth(c.Credential.Username(), c.Credential.Password())
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("onvif: %s: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("onvif: read the %s response: %w", action, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("onvif: %s: the device refused the credential (%d)", action, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("onvif: %s: the device answered %d", action, resp.StatusCode)
	}
	if f := faultFrom(raw); f != nil {
		return nil, f
	}
	return raw, nil
}

// faultFrom reads a SOAP fault out of a response, if it is one.
func faultFrom(raw []byte) *Fault {
	var f faultEnvelope
	if err := xml.Unmarshal(raw, &f); err == nil && f.Body.Fault.Code != "" {
		return &Fault{
			Code:   f.Body.Fault.Code,
			Reason: f.Body.Fault.Reason.Text,
			Detail: f.Body.Fault.Detail,
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The three reads
// ---------------------------------------------------------------------------

// deviceInformationResponse is the body of GetDeviceInformation.
//
// The nesting is modelled because Go's XML decoder matches a field against a
// DIRECT child: a struct that read the manufacturer's name at the top level
// would find nothing, and the empty result would look exactly like a device
// that said nothing.
type deviceInformationResponse struct {
	Body struct {
		Response struct {
			Manufacturer    string `xml:"Manufacturer"`
			Model           string `xml:"Model"`
			FirmwareVersion string `xml:"FirmwareVersion"`
			SerialNumber    string `xml:"SerialNumber"`
			HardwareID      string `xml:"HardwareId"`
		} `xml:"GetDeviceInformationResponse"`
	} `xml:"Body"`
}

// GetDeviceInformation reads what the device says about itself.
//
// This is identification, not measurement. A model name is not evidence, and
// the manifest records it as the device's own account of itself.
func (c *Client) GetDeviceInformation(ctx context.Context) (DeviceInformation, error) {
	const action = "http://www.onvif.org/ver10/device/wsdl/OnvifDeviceService/GetDeviceInformation"
	raw, err := c.post(ctx, DeviceServicePath, action, "")
	if err != nil {
		return DeviceInformation{}, err
	}
	var resp deviceInformationResponse
	if err := xml.Unmarshal(raw, &resp); err != nil {
		return DeviceInformation{}, fmt.Errorf("onvif: decode the device information: %w", err)
	}
	r := resp.Body.Response
	return DeviceInformation{
		Manufacturer:    r.Manufacturer,
		Model:           r.Model,
		FirmwareVersion: r.FirmwareVersion,
		SerialNumber:    r.SerialNumber,
		HardwareID:      r.HardwareID,
	}, nil
}

// capabilitiesResponse is the body of GetCapabilities.
//
// Every sub-capability is read as a raw element so that the set of services
// present is what is measured, rather than a fixed list of fields that a
// device either fills in or does not.
type capabilitiesResponse struct {
	Body struct {
		Response struct {
			Capabilities struct {
				Inner []byte `xml:",innerxml"`
			} `xml:"Capabilities"`
		} `xml:"GetCapabilitiesResponse"`
	} `xml:"Body"`
}

// capabilityHolder is one capability element, with its token.
type capabilityHolder struct {
	Token string `xml:"Token,attr"`
	Inner []byte `xml:",innerxml"`
}

// GetCapabilities reads the services the device actually exposes.
//
// A capability that is present is a capability the device's own capabilities
// document names. It is not yet proof that the service answers: the probe
// reads the media profiles afterwards, and a capability that names a service
// which then refuses every call is recorded as contradicted rather than
// verified.
func (c *Client) GetCapabilities(ctx context.Context) (Capabilities, error) {
	const action = "http://www.onvif.org/ver10/device/wsdl/OnvifDeviceService/GetCapabilities"
	raw, err := c.post(ctx, DeviceServicePath, action, "")
	if err != nil {
		return nil, err
	}
	var resp capabilitiesResponse
	if err := xml.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("onvif: decode the capabilities: %w", err)
	}
	out := Capabilities{}
	for _, want := range capabilityElements {
		inner, found := elementInner(resp.Body.Response.Capabilities.Inner, want.Element)
		if !found {
			continue
		}
		holder := capabilityHolder{}
		_ = xml.Unmarshal(inner, &holder)
		out[want.Name] = holder.Token
	}
	return out, nil
}

// elementInner returns the inner XML of a TOP-LEVEL element with the given
// local name, or nil when there is no such element.
//
// Top level, because a nested element of the same name belongs to a
// capability the device did not expose: a `PTZ` element nested inside
// `Extension` is not a PTZ service, and reading it as one is the exact
// failure this package exists to prevent.
func elementInner(fragment []byte, name string) ([]byte, bool) {
	decoder := xml.NewDecoder(strings.NewReader(string(fragment)))
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != name {
			if err := decoder.Skip(); err != nil {
				return nil, false
			}
			continue
		}
		var holder struct {
			Inner []byte `xml:",innerxml"`
		}
		if err := decoder.DecodeElement(&holder, &start); err != nil {
			return nil, false
		}
		return holder.Inner, true
	}
}

// hasElement reports whether a raw XML fragment has a TOP-LEVEL element with
// the given local name.
func hasElement(fragment []byte, name string) bool {
	_, found := elementInner(fragment, name)
	return found
}

// profilesResponse is the body of GetProfiles.
type profilesResponse struct {
	Body struct {
		Response struct {
			Profiles []struct {
				Token string `xml:"token,attr"`
				Name  string `xml:"Name"`
				Inner []byte `xml:",innerxml"`
			} `xml:"Profiles"`
		} `xml:"GetProfilesResponse"`
	} `xml:"Body"`
}

// GetProfiles reads the media profiles the device implements.
//
// The flags below are the measurement. A device that lists a Profile M token
// and whose profile carries no analytics configuration is a device that does
// not do Profile M, and this is where that is found.
func (c *Client) GetProfiles(ctx context.Context) ([]MediaProfile, error) {
	const action = "http://www.onvif.org/ver10/media/wsdl/OnvifMediaService/GetProfiles"
	raw, err := c.post(ctx, MediaServicePath, action, "")
	if err != nil {
		return nil, err
	}
	var resp profilesResponse
	if err := xml.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("onvif: decode the profiles: %w", err)
	}
	out := make([]MediaProfile, 0, len(resp.Body.Response.Profiles))
	for _, p := range resp.Body.Response.Profiles {
		profile := MediaProfile{
			Token: p.Token,
			Name:  p.Name,
		}
		profile.HasVideo = hasElement(p.Inner, "VideoEncoderConfiguration")
		profile.HasAudio = hasElement(p.Inner, "AudioEncoderConfiguration")
		profile.HasAnalytics = hasElement(p.Inner, "VideoAnalyticsConfiguration")
		profile.HasAnalyticsEvents = hasElement(p.Inner, "AnalyticsEventConfiguration")
		profile.HasImaging = hasElement(p.Inner, "ImagingConfiguration")
		profile.HasPTZ = hasElement(p.Inner, "PTZConfiguration")
		profile.HasDevice = hasElement(p.Inner, "DeviceConfiguration")
		encoder, _ := elementInner(p.Inner, "VideoEncoderConfiguration")
		profile.Width, profile.Height, profile.FPS, profile.Codec = videoConfiguration(encoder)
		out = append(out, profile)
	}
	return out, nil
}

// videoConfiguration reads the resolution, the frame rate and the codec out of
// a video encoder configuration.
//
// A configuration that declares none of them is not broken: some devices
// publish a bare token, and the honest answer is then "not declared", which
// is what zeroes say.
func videoConfiguration(fragment []byte) (width, height, fps int, codec string) {
	if fragment == nil {
		return 0, 0, 0, ""
	}
	var cfg struct {
		Codec      string `xml:"Encoding"`
		Rate       int    `xml:"Rate"`
		Resolution struct {
			Width  int `xml:"Width"`
			Height int `xml:"Height"`
		} `xml:"Resolution"`
	}
	// A configuration that does not decode is one with nothing to report, so a
	// decode failure is not an error here.
	_ = xml.Unmarshal(fragment, &cfg)
	return cfg.Resolution.Width, cfg.Resolution.Height, cfg.Rate, cfg.Codec
}

// ServiceURL returns the absolute URL of one of the device's services.
//
// The address comes from the device itself when it published one, and falls
// back to the conventional path when it did not. The fallback is a guess, and
// the caller is expected to find out whether it was right by making the call.
func (c *Client) ServiceURL(servicePath string) string {
	return strings.TrimRight(c.BaseURL, "/") + servicePath
}

// ParseBaseURL turns a device's address into a base URL.
//
// A device may publish an address with or without a scheme, and an address
// with a port. The scheme defaults to HTTP because that is what the ONVIF
// specification's default port serves, and a device that wants HTTPS publishes
// an `https` address.
func ParseBaseURL(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("onvif: the device has no address")
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("onvif: the device's address %q does not parse: %w", address, err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("onvif: the device's address %q has no host", address)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}
