package rtsp

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Description is what a camera says its stream is, in the terms the rest of the
// capture path needs.
type Description struct {
	Session     string
	Control     string
	ClockRate   int
	PayloadType int
	// PacketizationMode is the RFC 6184 mode. Mode 1 is the one every camera
	// that fragments at all uses, and it is the one the depacketiser implements.
	PacketizationMode int
	// SPS and PPS come from the session description when the camera offers
	// them. A camera that does not still sends them in band, and the recorder
	// takes them from the stream instead.
	SPS []byte
	PPS []byte
	// Width, Height and FPS are the declared geometry. They are advisory: a
	// camera's declared frame size and the stream's actual one disagree often
	// enough that the muxer reads the truth from the SPS rather than from here.
	Width  int
	Height int
	FPS    int
}

// ErrNoMediaSection means the description names no video media at all.
var ErrNoMediaSection = errors.New("rtsp: the session description has no video media section")

// parseSDP extracts the one media section this client can use.
//
// It reads the SDP as text and does not build a full session description: one
// video track, one codec, and everything else ignored. A description with two
// video tracks is not an error here - a camera may offer an audio track and a
// main and a substream, and refusing the whole session because of the audio
// track would be refusing a working camera.
func parseSDP(body []byte) (*Description, error) {
	var (
		desc     Description
		sawVideo bool
		inVideo  bool
		rtpmap   = map[int]string{}
		fmtp     = map[int]string{}
		framesz  = map[int]string{}
		fps      = map[int]string{}
		control  string
	)
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		typ, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch typ {
		case "m":
			fields := strings.Fields(value)
			if len(fields) < 4 {
				continue
			}
			inVideo = fields[0] == "video"
			if !inVideo {
				continue
			}
			sawVideo = true
			// The last format on a media line is the one a SETUP without an
			// explicit payload type selects, and cameras list several so the
			// client can pick. Payload type 0 is legal in RTP, so it is carried
			// as a value and never as a sentinel for "nothing was declared" -
			// sawVideo is what says a media section was there at all.
			if pt, err := strconv.Atoi(fields[3]); err == nil {
				desc.PayloadType = pt
			}
		case "a":
			if !inVideo {
				continue
			}
			name, rest, ok := strings.Cut(value, ":")
			if !ok {
				// A bare attribute such as a=sendonly. Nothing here needs one.
				continue
			}
			switch name {
			case "control":
				control = rest
			case "rtpmap":
				pt, codec, ok := strings.Cut(rest, " ")
				if !ok {
					continue
				}
				n, err := strconv.Atoi(strings.TrimSpace(pt))
				if err != nil {
					continue
				}
				rtpmap[n] = strings.TrimSpace(codec)
			case "fmtp":
				pt, params, ok := strings.Cut(rest, " ")
				if !ok {
					continue
				}
				n, err := strconv.Atoi(strings.TrimSpace(pt))
				if err != nil {
					continue
				}
				fmtp[n] = params
			case "framesize":
				pt, size, ok := strings.Cut(rest, " ")
				if !ok {
					continue
				}
				n, err := strconv.Atoi(strings.TrimSpace(pt))
				if err != nil {
					continue
				}
				framesz[n] = strings.TrimSpace(size)
			case "framerate":
				// Cameras write this two ways: a=framerate:25 with no payload
				// type at all, and a=framerate:96 25 when they want it scoped.
				// Both are common, and reading only the second is how a client
				// reports 0 fps for half the cameras in the world.
				rate, _, ok := strings.Cut(rest, " ")
				if !ok {
					rate = rest
				}
				fps[desc.PayloadType] = strings.TrimSpace(rate)
			}
		}
	}
	if !sawVideo {
		return nil, ErrNoMediaSection
	}

	// A description that positively names a codec other than H.264 is refused.
	// One that names no codec is allowed through, because cameras that describe
	// their stream only well enough to be played by their own client are common
	// and the depacketiser will find out within a second of PLAY.
	if codec := rtpmap[desc.PayloadType]; codec != "" && !strings.Contains(strings.ToUpper(codec), "H264") {
		return nil, fmt.Errorf("%w: payload type %d is %q", ErrNoH264, desc.PayloadType, codec)
	}
	if name, rate, ok := strings.Cut(rtpmap[desc.PayloadType], "/"); ok && name != "" {
		if n, err := strconv.Atoi(rate); err == nil && n > 0 {
			desc.ClockRate = n
		}
	}
	if desc.ClockRate == 0 {
		desc.ClockRate = 90000
	}

	desc.Control = control
	if desc.Control == "" {
		desc.Control = "trackID=0"
	}
	if v, ok := framesz[desc.PayloadType]; ok {
		if w, h, ok := strings.Cut(v, "-"); ok {
			desc.Width, _ = strconv.Atoi(strings.TrimSpace(w))
			desc.Height, _ = strconv.Atoi(strings.TrimSpace(h))
		}
	}
	if v, ok := fps[desc.PayloadType]; ok {
		desc.FPS, _ = strconv.Atoi(strings.TrimSpace(v))
	}

	// A camera that describes its stream with no packetization-mode at all is
	// mode 1, which is the mode every camera that fragments at all uses.
	desc.PacketizationMode = 1
	if params, ok := fmtp[desc.PayloadType]; ok {
		for _, kv := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "packetization-mode":
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					desc.PacketizationMode = n
				}
			case "sprop-parameter-sets":
				sps, pps, ok := strings.Cut(v, ",")
				if !ok {
					continue
				}
				if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sps)); err == nil {
					desc.SPS = b
				}
				if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pps)); err == nil {
					desc.PPS = b
				}
			}
		}
	}
	return &desc, nil
}
