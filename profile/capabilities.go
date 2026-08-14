package profile

import (
	"fmt"
	"math"
	"math/bits"
	"strings"
)

type Orientation string

const (
	OrientationUnknown   Orientation = ""
	OrientationPortrait  Orientation = "portrait"
	OrientationLandscape Orientation = "landscape"
)

type ColorType uint8

const (
	ColorDirect ColorType = 1 << iota
	ColorGray
	ColorPalette
)

type Screen struct {
	Width        int         `json:"width"`
	Height       int         `json:"height"`
	Orientation  Orientation `json:"orientation,omitempty"`
	BitsPerPixel int         `json:"bits_per_pixel,omitempty"`
	Depth        int         `json:"depth,omitempty"`
	BytesPerLine int         `json:"bytes_per_line,omitempty"`
	ColorType    ColorType   `json:"color_type,omitempty"`
	RedMask      uint32      `json:"red_mask,omitempty"`
	GreenMask    uint32      `json:"green_mask,omitempty"`
	BlueMask     uint32      `json:"blue_mask,omitempty"`
}

func (s Screen) Validate() error {
	if s.Width <= 0 || s.Height <= 0 ||
		int64(s.Width) > math.MaxInt32 || int64(s.Height) > math.MaxInt32 {
		return fmt.Errorf("invalid screen geometry %dx%d", s.Width, s.Height)
	}
	switch s.Orientation {
	case OrientationUnknown, OrientationPortrait, OrientationLandscape:
	default:
		return fmt.Errorf("invalid screen orientation %q", s.Orientation)
	}

	formatSpecified := s.BitsPerPixel != 0 || s.Depth != 0 ||
		s.BytesPerLine != 0 || s.ColorType != 0 ||
		s.RedMask != 0 || s.GreenMask != 0 || s.BlueMask != 0
	if !formatSpecified {
		return nil
	}
	if s.BitsPerPixel <= 0 || s.BitsPerPixel > 32 {
		return fmt.Errorf("invalid bits per pixel %d", s.BitsPerPixel)
	}
	if s.Depth <= 0 || s.Depth > s.BitsPerPixel {
		return fmt.Errorf("invalid color depth %d for %d bits per pixel", s.Depth, s.BitsPerPixel)
	}
	rowBits := int64(s.Width) * int64(s.BitsPerPixel)
	minimumBytesPerLine := (rowBits + 7) / 8
	if int64(s.BytesPerLine) < minimumBytesPerLine ||
		int64(s.BytesPerLine) > math.MaxInt32 {
		return fmt.Errorf(
			"bytes per line %d is smaller than packed width %d",
			s.BytesPerLine,
			minimumBytesPerLine,
		)
	}
	switch s.ColorType {
	case ColorDirect, ColorGray, ColorPalette:
	default:
		return fmt.Errorf("invalid color type 0x%x", s.ColorType)
	}
	if s.BitsPerPixel < 32 {
		validMask := uint32(1)<<s.BitsPerPixel - 1
		if (s.RedMask|s.GreenMask|s.BlueMask)&^validMask != 0 {
			return fmt.Errorf("color mask exceeds %d-bit pixel", s.BitsPerPixel)
		}
	}
	if s.RedMask&s.GreenMask != 0 ||
		s.RedMask&s.BlueMask != 0 ||
		s.GreenMask&s.BlueMask != 0 {
		return fmt.Errorf("color masks overlap")
	}
	combinedMask := s.RedMask | s.GreenMask | s.BlueMask
	if s.ColorType == ColorDirect {
		if combinedMask == 0 {
			return fmt.Errorf("direct-color screen has no color masks")
		}
		if bits.OnesCount32(combinedMask) != s.Depth {
			return fmt.Errorf(
				"color masks contain %d effective bits, want depth %d",
				bits.OnesCount32(combinedMask),
				s.Depth,
			)
		}
	} else if combinedMask != 0 {
		return fmt.Errorf("non-direct-color screen has RGB masks")
	}
	return nil
}

// Capability names are stable profile data. Unknown non-empty capability
// names are retained so carrier or manufacturer extensions do not require
// code changes in the profile composer.
type Capability string

const (
	CapabilityGraphics  Capability = "graphics"
	CapabilityImages    Capability = "images"
	CapabilityText      Capability = "text"
	CapabilityAudio     Capability = "audio"
	CapabilityVibration Capability = "vibration"
	CapabilityBacklight Capability = "backlight"
	CapabilityLED       Capability = "led"
	CapabilityNetwork   Capability = "network"
	CapabilityHTTP      Capability = "http"
	CapabilitySerial    Capability = "serial"
	CapabilityPhone     Capability = "phone"
	CapabilitySMS       Capability = "sms"
	CapabilityBrowser   Capability = "browser"
)

type Capabilities map[Capability]bool

func (c Capabilities) Validate() error {
	for capability := range c {
		value := string(capability)
		if strings.TrimSpace(value) == "" || len(value) > 64 ||
			strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("invalid capability name %q", capability)
		}
	}
	return nil
}

// Limit names are stable profile data. Values are byte, object, descriptor,
// or duration counts according to the named service contract.
type Limit string

const (
	LimitGuestMemory      Limit = "guest-memory-bytes"
	LimitSurfaceCount     Limit = "surface-count"
	LimitSurfacePixels    Limit = "surface-pixels"
	LimitDecodedAssetSize Limit = "decoded-asset-bytes"
	LimitEventCount       Limit = "event-count"
	LimitTraceCount       Limit = "trace-count"
	LimitStorageBytes     Limit = "storage-bytes"
	LimitOpenFiles        Limit = "open-files"
	LimitRecordStores     Limit = "record-stores"
	LimitAudioBytes       Limit = "audio-queued-bytes"
	LimitMediaClips       Limit = "media-clips"
	LimitSockets          Limit = "sockets"
	LimitHTTPRequests     Limit = "http-requests"
	LimitSerialPorts      Limit = "serial-ports"
	LimitNetworkBytes     Limit = "network-buffer-bytes"
	LimitDeviceRequests   Limit = "device-requests"
	LimitTimers           Limit = "timers"
	LimitServiceObjects   Limit = "service-objects"
	LimitAssets           Limit = "decoded-assets"
)

type Limits map[Limit]uint64

func (l Limits) Validate() error {
	for limit, value := range l {
		name := string(limit)
		if strings.TrimSpace(name) == "" || len(name) > 64 ||
			strings.IndexByte(name, 0) >= 0 {
			return fmt.Errorf("invalid service limit name %q", limit)
		}
		if value == 0 {
			return fmt.Errorf("service limit %q is zero", limit)
		}
	}
	return nil
}
