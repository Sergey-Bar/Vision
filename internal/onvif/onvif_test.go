package onvif_test

// What a probe establishes, and what it refuses to establish.
//
// # The finding this file exists for
//
// A camera advertises a set of ONVIF profiles, and a camera is routinely
// wrong about that. The classic case is a device whose capabilities document
// names the analytics service and whose media profiles carry no video
// analytics configuration at all: it says it does analytics and it does not.
//
// A product that believes the advertisement offers an analytics feature the
// camera cannot do, and the first person to find out is a customer.
//
// So the tests below are shaped around that: the probe must record a
// capability only when something answered, and it must record a CONTRADICTION
// when the device's own claim is not borne out.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/device"
	"github.com/Sergey-Bar/vision/internal/onvif"
)

// The fixtures. They are written the way a real device writes them, with
// namespaces the product does not care about, because a parser that only
// reads one vendor's prefixes reads one vendor's cameras.

const deviceInformationXML = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <tds:GetDeviceInformationResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
      <tds:Manufacturer>Acme</tds:Manufacturer>
      <tds:Model>Cam 9000</tds:Model>
      <tds:FirmwareVersion>1.4.2</tds:FirmwareVersion>
      <tds:SerialNumber>SN-0001</tds:SerialNumber>
      <tds:HardwareId>hw-0001</tds:HardwareId>
    </tds:GetDeviceInformationResponse>
  </s:Body>
</s:Envelope>`

// capabilitiesXML names the analytics service and no others beyond media.
const capabilitiesXML = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <tds:GetCapabilitiesResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
      <tds:Capabilities>
        <tt:Analytics xmlns:tt="http://www.onvif.org/ver10/schema"/>
        <tt:Device xmlns:tt="http://www.onvif.org/ver10/schema"/>
        <tt:Media xmlns:tt="http://www.onvif.org/ver10/schema">
          <tt:XAddr>http://192.0.2.10:80/onvif/media_service</tt:XAddr>
        </tt:Media>
      </tds:Capabilities>
    </tds:GetCapabilitiesResponse>
  </s:Body>
</s:Envelope>`

// profilesWithAnalytics carries a real video analytics configuration.
const profilesWithAnalytics = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <trt:GetProfilesResponse xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
      <trt:Profiles token="Profile_1">
        <tt:Name xmlns:tt="http://www.onvif.org/ver10/schema">Main Stream</tt:Name>
        <tt:VideoEncoderConfiguration xmlns:tt="http://www.onvif.org/ver10/schema">
          <tt:Encoding>H264</tt:Encoding>
          <tt:Rate>25</tt:Rate>
          <tt:Resolution>
            <tt:Width>1920</tt:Width>
            <tt:Height>1080</tt:Height>
          </tt:Resolution>
        </tt:VideoEncoderConfiguration>
        <tt:VideoAnalyticsConfiguration xmlns:tt="http://www.onvif.org/ver10/schema"/>
      </trt:Profiles>
    </trt:GetProfilesResponse>
  </s:Body>
</s:Envelope>`

// profilesWithoutAnalytics claims the analytics service and implements
// nothing: a video profile, and no analytics configuration at all.
const profilesWithoutAnalytics = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <trt:GetProfilesResponse xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
      <trt:Profiles token="Profile_1">
        <tt:Name xmlns:tt="http://www.onvif.org/ver10/schema">Main Stream</tt:Name>
        <tt:VideoEncoderConfiguration xmlns:tt="http://www.onvif.org/ver10/schema">
          <tt:Encoding>H264</tt:Encoding>
          <tt:Rate>25</tt:Rate>
          <tt:Resolution>
            <tt:Width>1920</tt:Width>
            <tt:Height>1080</tt:Height>
          </tt:Resolution>
        </tt:VideoEncoderConfiguration>
      </trt:Profiles>
    </trt:GetProfilesResponse>
  </s:Body>
</s:Envelope>`

// aDevice serves the three ONVIF calls.
//
// The handler routes on the path and then on the action in the body, which is
// how a real device routes and how the product's own client asks.
func aDevice(t *testing.T, profiles string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(body)
		}
		text := string(body)
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		switch {
		case strings.Contains(text, "GetDeviceInformation"):
			_, _ = w.Write([]byte(deviceInformationXML))
		case strings.Contains(text, "GetCapabilities"):
			_, _ = w.Write([]byte(capabilitiesXML))
		case strings.Contains(text, "GetProfiles"):
			_, _ = w.Write([]byte(profiles))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// aReference is a probe's subject.
func aReference() onvif.Reference {
	return onvif.Reference{
		TenantID: "tenant-a",
		SiteID:   "site-a",
		DeviceID: "device-a",
		ProbeID:  "probe-1",
	}
}

// TestTheProbeRecordsAProfileMWhenTheProfileCarriesOne is the positive case.
//
// The device's media profile carries a video analytics configuration, so the
// capability is established, and the manifest says so.
func TestTheProbeRecordsAProfileMWhenTheProfileCarriesOne(t *testing.T) {
	server := aDevice(t, profilesWithAnalytics)
	client := &onvif.Client{BaseURL: server.URL, HTTP: server.Client()}

	manifest, err := onvif.Probe(context.Background(), aReference(), client)
	if err != nil {
		t.Fatalf("probe the device: %v", err)
	}
	if !manifest.Supports(onvif.CapProfileM) {
		t.Fatalf("the probe did not establish Profile M on a device whose profile "+
			"carries a video analytics configuration: %v", manifest.Claimed())
	}
	if manifest.Make != "Acme" || manifest.Model != "Cam 9000" {
		t.Fatalf("the probe did not record the device's identity: %+v", manifest)
	}
}

// TestTheProbeContradictsADeviceThatClaimsAnalyticsAndDoesNotImplementIt is
// the control.
//
// The device names the analytics service and its media profile carries no
// analytics configuration. The manifest must record the CONTRADICTION, and
// must not claim the capability.
func TestTheProbeContradictsADeviceThatClaimsAnalyticsAndDoesNotImplementIt(t *testing.T) {
	server := aDevice(t, profilesWithoutAnalytics)
	client := &onvif.Client{BaseURL: server.URL, HTTP: server.Client()}

	manifest, err := onvif.Probe(context.Background(), aReference(), client)
	if err != nil {
		t.Fatalf("probe the device: %v", err)
	}
	if manifest.Supports(onvif.CapProfileM) {
		t.Fatal("the probe claimed Profile M on a device that names the analytics " +
			"service and implements no analytics configuration. This is the exact " +
			"case the manifest exists for.")
	}
	state, found := manifest.StateOf(onvif.CapProfileM)
	if !found {
		t.Fatal("the probe said nothing about Profile M. A device that claims a " +
			"capability it does not have is a finding, and a finding that is not " +
			"recorded is a finding nobody can act on.")
	}
	if state != device.StateContradicted {
		t.Fatalf("the probe recorded Profile M as %q; a device that claims it and does "+
			"not implement it is contradicted, and that is the state that says so.", state)
	}
}

// TestTheProbeRecordsTheVideoProfileTheDeviceActuallyCarries checks the
// ordinary case: a profile with a video encoder configuration is a
// capability.
func TestTheProbeRecordsTheVideoProfileTheDeviceActuallyCarries(t *testing.T) {
	server := aDevice(t, profilesWithAnalytics)
	client := &onvif.Client{BaseURL: server.URL, HTTP: server.Client()}

	manifest, err := onvif.Probe(context.Background(), aReference(), client)
	if err != nil {
		t.Fatalf("probe the device: %v", err)
	}
	if !manifest.Supports(onvif.CapProfileS) {
		t.Fatalf("the probe did not establish the video profile: %v", manifest.Claimed())
	}
	if !manifest.Supports(onvif.CapMedia) {
		t.Fatal("the probe did not establish the media service, which answered")
	}
}

// TestTheProbeDoesNotClaimAServiceTheDeviceOnlyNames is the discipline.
//
// The capabilities document NAMES the analytics service. Nothing has called
// it, so it is a claim, and a claim is not a capability.
func TestTheProbeDoesNotClaimAServiceTheDeviceOnlyNames(t *testing.T) {
	server := aDevice(t, profilesWithAnalytics)
	client := &onvif.Client{BaseURL: server.URL, HTTP: server.Client()}

	manifest, err := onvif.Probe(context.Background(), aReference(), client)
	if err != nil {
		t.Fatalf("probe the device: %v", err)
	}
	if manifest.Supports(onvif.CapAnalytics) {
		t.Fatal("the probe claimed the analytics service because the device's own " +
			"capabilities document named it. Naming is not measuring.")
	}
	state, found := manifest.StateOf(onvif.CapAnalytics)
	if !found || state != device.StateUnverified {
		t.Fatalf("the analytics service was recorded as %q (found: %v); a service the "+
			"device names and nobody has called is unverified.", state, found)
	}
}

// TestEveryCapabilityTheProbeRecordsCarriesTheProbe checks the manifest's own
// rule, on the probe's output.
//
// The probe is the only thing allowed to write a verified capability, and
// every capability it writes names the probe that decided it.
func TestEveryCapabilityTheProbeRecordsCarriesTheProbe(t *testing.T) {
	server := aDevice(t, profilesWithAnalytics)
	client := &onvif.Client{BaseURL: server.URL, HTTP: server.Client()}

	manifest, err := onvif.Probe(context.Background(), aReference(), client)
	if err != nil {
		t.Fatalf("probe the device: %v", err)
	}
	if len(manifest.Capabilities) == 0 {
		t.Fatal("the probe recorded no capabilities at all")
	}
	for _, c := range manifest.Capabilities {
		if c.Probe == "" {
			t.Fatalf("the probe recorded %s with no probe behind it. A capability "+
				"without a probe is a claim.", c.Name)
		}
		if c.State == device.StateVerified && c.Probe != manifest.ProbeID {
			t.Fatalf("the probe recorded %s as verified by %q, which is not this "+
				"probe (%q)", c.Name, c.Probe, manifest.ProbeID)
		}
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("the manifest the probe produced does not satisfy its own rules: %v", err)
	}
}

// TestTheClientDoesNotPrintItsCredential keeps a secret out of a log.
//
// A `String` method is what a log line prints, and a camera's password in a
// log is a camera's password in a file nobody is going to rotate.
func TestTheClientDoesNotPrintItsCredential(t *testing.T) {
	client := &onvif.Client{
		BaseURL:    "http://192.0.2.10:80",
		Credential: staticCredential{user: "admin", password: "hunter2-camera-secret"},
	}
	printed := client.String()
	if strings.Contains(printed, "hunter2-camera-secret") {
		t.Fatalf("the client's own description carries its credential: %q", printed)
	}
	if !strings.Contains(printed, "192.0.2.10") {
		t.Fatalf("the client's own description does not carry its address: %q", printed)
	}
}

// TestASOAPFaultIsNamedRatherThanATimeout keeps a refusal and a silence
// apart.
//
// A device that answers with a fault has answered, and its reason is
// information. A device that does not answer has said nothing at all, and
// reading the two the same way is how a "the camera is broken" becomes "the
// camera does not support that" in a report.
func TestASOAPFaultIsNamedRatherThanATimeout(t *testing.T) {
	const fault = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <s:Fault>
      <s:Code>s:Sender</s:Code>
      <s:Reason><s:Text xml:lang="en">NotAuthorized</s:Text></s:Reason>
      <s:Detail>the credential was refused</s:Detail>
    </s:Fault>
  </s:Body>
</s:Envelope>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		_, _ = w.Write([]byte(fault))
	}))
	t.Cleanup(server.Close)

	client := &onvif.Client{BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.GetDeviceInformation(context.Background())
	if err == nil {
		t.Fatal("a device that answered with a fault was treated as having answered")
	}
	if !strings.Contains(err.Error(), "NotAuthorized") {
		t.Fatalf("the device's own reason was lost: %v", err)
	}
}

// TestTheDiscoveryResponseNamesTheDevicesServices reads a discovery reply.
//
// The probe is the first thing that happens, and a reply it cannot read is a
// device the product cannot find at all.
func TestTheDiscoveryResponseNamesTheDevicesServices(t *testing.T) {
	const response = `<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"
             xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
  <e:Body>
    <d:ProbeMatches>
      <d:ProbeMatch>
        <d:EndpointReference>
          <d:Address>urn:uuid:11111111-2222-3333-4444-555555555555</d:Address>
        </d:EndpointReference>
        <d:XAddrs>http://192.0.2.10:80/onvif/device_service</d:XAddrs>
        <d:Types>dn:NetworkVideoTransmitter</d:Types>
        <d:Scopes>onvif://www.onvif.org/type/video_encoder</d:Scopes>
        <d:Name>Loading Dock</d:Name>
        <d:Location>Building A</d:Location>
      </d:ProbeMatch>
    </d:ProbeMatches>
  </e:Body>
</e:Envelope>`
	endpoints, err := onvif.ParseProbeMatch([]byte(response))
	if err != nil {
		t.Fatalf("read the discovery response: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("the discovery response named %d endpoints; it named one", len(endpoints))
	}
	e := endpoints[0]
	if e.Name != "Loading Dock" {
		t.Errorf("the discovery response lost the device's name: %q", e.Name)
	}
	if len(e.XAddrs) != 1 || !strings.Contains(e.XAddrs[0], "192.0.2.10") {
		t.Errorf("the discovery response lost the device's service address: %v", e.XAddrs)
	}
	base, err := e.BaseURL()
	if err != nil {
		t.Fatalf("derive the device's base URL: %v", err)
	}
	if base != "http://192.0.2.10:80" {
		t.Errorf("the device's base URL is %q; it should be the service address's "+
			"scheme and host", base)
	}
}

// TestADiscoveryResponseThatNamesNothingIsAnError keeps "the device answered
// and said nothing" apart from "no device answered".
func TestADiscoveryResponseThatNamesNothingIsAnError(t *testing.T) {
	const response = `<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope">
  <e:Body><d:ProbeMatches xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"/></e:Body>
</e:Envelope>`
	if _, err := onvif.ParseProbeMatch([]byte(response)); err == nil {
		t.Fatal("a discovery response that named no endpoint was accepted as one. " +
			"'Nobody answered' and 'the device answered and named nothing' are " +
			"different findings.")
	}
}

// TestTheProbeAsksForANetworkVideoTransmitter checks the probe's own bytes.
//
// A probe that asks for the wrong device type finds the wrong things, and a
// device list that includes a printer is a device list nobody trusts.
func TestTheProbeAsksForANetworkVideoTransmitter(t *testing.T) {
	message := string(onvif.ProbeMessage())
	for _, want := range []string{
		onvif.NetworkVideoTransmitter,
		onvif.ProbeAction,
		onvif.ProbeTarget,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the discovery probe does not carry %q", want)
		}
	}
}

// staticCredential is a credential for the tests.
//
// It is a struct with methods rather than a literal so that the test reads
// the way the product's callers read: a keystore entry that hands over a user
// name and a secret.
type staticCredential struct {
	user     string
	password string
}

func (c staticCredential) Username() string { return c.user }

func (c staticCredential) Password() string { return c.password }
