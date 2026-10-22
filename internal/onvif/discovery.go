package onvif

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"time"
)

// The WS-Discovery constants.
//
// The multicast group and the port are fixed by the specification, and a
// device that listens anywhere else is not a device this probe can find.
const (
	// MulticastGroup is the WS-Discovery multicast group.
	MulticastGroup = "239.255.255.250"
	// MulticastPort is the WS-Discovery port.
	MulticastPort = 3702
	// ProbeAction is the WS-Discovery probe action.
	ProbeAction = "http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe"
	// ProbeTarget is the address a probe is sent to.
	ProbeTarget = "urn:schemas-xmlsoap-org:ws:2005:04:discovery"
	// NetworkVideoTransmitter is the ONVIF device type a probe asks for.
	NetworkVideoTransmitter = "dn:NetworkVideoTransmitter"
)

// Endpoint is a device that answered a discovery probe.
//
// The address is what the device says it is reachable at, and the service
// addresses are the URLs it published. Both are the device's own account of
// itself, and the probe that follows is what turns them into measurements.
type Endpoint struct {
	// Address is the device's host, with or without a port.
	Address string
	// XAddrs are the service URLs the device published.
	XAddrs []string
	// Types are the device types it claims.
	Types []string
	// Scopes are the discovery scopes it published, which carry the ONVIF
	// version and the hardware and the location.
	Scopes []string
	// Name is the device's friendly name, if it published one.
	Name string
	// Location is the physical location it published.
	Location string
}

// BaseURL returns the device's base address, derived from the first service
// address it published and falling back to its own address.
//
// A service address is preferred because it is the one the device says its
// services are reachable at, and the address it publishes for itself is
// sometimes the address of a different interface.
func (e Endpoint) BaseURL() (string, error) {
	for _, addr := range e.XAddrs {
		if base, err := ParseBaseURL(addr); err == nil {
			return base, nil
		}
	}
	if e.Address != "" {
		return ParseBaseURL(e.Address)
	}
	return "", fmt.Errorf("onvif: the device published neither a service address nor an address")
}

// probeEnvelope is the WS-Discovery probe.
//
// The body asks for a network video transmitter, which is the ONVIF device
// type. The header carries a message id, because a device that sees the same
// id twice may answer the second probe with the first probe's reply, and a
// probe that reads a stale answer reports a device that has since moved.
func probeEnvelope() []byte {
	messageID := fmt.Sprintf("urn:uuid:probe-%d", time.Now().UnixNano())
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"` +
		` xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"` +
		` xmlns:dn="http://www.onvif.org/ver10/network/wsdl">` +
		`<e:Header>` +
		`<w:MessageID>` + messageID + `</w:MessageID>` +
		`<w:To e:mustUnderstand="true">` + ProbeTarget + `</w:To>` +
		`<w:Action e:mustUnderstand="true">` + ProbeAction + `</w:Action>` +
		`</e:Header>` +
		`<e:Body><d:Probe><d:Types>` + NetworkVideoTransmitter + `</d:Types></d:Probe></e:Body>` +
		`</e:Envelope>`)
}

// ProbeMessage returns the bytes of a discovery probe.
//
// It is exported because the probe is worth sending from a test, and a test
// that reuses the production bytes is a test that cannot drift from them.
func ProbeMessage() []byte { return probeEnvelope() }

// probeMatchResponse is a discovery response.
//
// The nesting is modelled because Go's XML decoder matches a field against a
// DIRECT child: a struct that looked for `ProbeMatch` directly under the body
// would find nothing, and a discovery response that found nothing looks
// exactly like a network with no devices on it.
type probeMatchResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Matches struct {
			ProbeMatches []struct {
				EndpointReference struct {
					Address string `xml:"Address"`
				} `xml:"EndpointReference"`
				XAddrs   []string `xml:"XAddrs"`
				Types    []string `xml:"Types"`
				Scopes   []string `xml:"Scopes"`
				Name     string   `xml:"Name"`
				Location string   `xml:"Location"`
			} `xml:"ProbeMatch"`
		} `xml:"ProbeMatches"`
	} `xml:"Body"`
}

// ParseProbeMatch reads a discovery response into the endpoints it names.
//
// A response that names no endpoint is a response that said nothing, and it
// is an error rather than an empty list: "the device answered and named
// nothing" and "no device answered" are different facts, and the probe
// reports the first as a failure rather than as an empty room.
func ParseProbeMatch(raw []byte) ([]Endpoint, error) {
	var resp probeMatchResponse
	if err := xml.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("onvif: decode the discovery response: %w", err)
	}
	matches := resp.Body.Matches.ProbeMatches
	out := make([]Endpoint, 0, len(matches))
	for _, m := range matches {
		out = append(out, Endpoint{
			Address:  m.EndpointReference.Address,
			XAddrs:   m.XAddrs,
			Types:    m.Types,
			Scopes:   m.Scopes,
			Name:     m.Name,
			Location: m.Location,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("onvif: the discovery response named no endpoint")
	}
	return out, nil
}

// Discover finds the ONVIF devices on the local network.
//
// The probe is a multicast datagram and the answers are multicast datagrams
// back, so the implementation opens a multicast socket, sends one probe, and
// reads until the timeout. A device that answers after the timeout is a
// device the next probe will find, and a probe that waited for every possible
// answer would wait for ever.
//
// The result is deduplicated by address: a device that answers twice is one
// device, and a registry that listed it twice would count it twice.
func Discover(ctx context.Context, timeout time.Duration) ([]Endpoint, error) {
	addr := &net.UDPAddr{IP: net.ParseIP(MulticastGroup), Port: MulticastPort}
	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("onvif: open the discovery socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.WriteToUDP(probeEnvelope(), addr); err != nil {
		return nil, fmt.Errorf("onvif: send the discovery probe: %w", err)
	}

	deadline := time.Now().Add(timeout)
	seen := map[string]bool{}
	var out []Endpoint
	buf := make([]byte, 64<<10)
	for time.Now().Before(deadline) {
		// The caller's context ends the scan, and the scan's own timeout
		// bounds it otherwise. A discovery that a cancelled request keeps
		// running is a discovery that outlives the operator who asked for it.
		if err := ctx.Err(); err != nil {
			break
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, fmt.Errorf("onvif: set the discovery read deadline: %w", err)
		}
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if isTimeout(err) {
				break
			}
			return nil, fmt.Errorf("onvif: read a discovery response: %w", err)
		}
		endpoints, err := ParseProbeMatch(buf[:n])
		if err != nil {
			// A datagram that is not a discovery response is noise on a
			// shared multicast group, and it is not a failure.
			continue
		}
		for _, e := range endpoints {
			key := e.Address
			if key == "" && len(e.XAddrs) > 0 {
				key = e.XAddrs[0]
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			if e.Address == "" && from != nil {
				e.Address = from.IP.String()
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// isTimeout reports whether an error is a read deadline expiring.
func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
