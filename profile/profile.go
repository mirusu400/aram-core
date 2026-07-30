package profile

import (
	"encoding/hex"
	"fmt"
	"math/bits"
	"strings"
)

type Version string

const (
	Version121 Version = "1.2.1"
	Version20  Version = "2.0"
	Version220 Version = "2.2.0"
)

func (v Version) Validate() error {
	switch v {
	case Version121, Version20, Version220:
		return nil
	default:
		return fmt.Errorf("unsupported WIPI version %q", v)
	}
}

type Carrier string

const (
	CarrierUnknown Carrier = "unknown"
	CarrierKTF     Carrier = "ktf"
	CarrierSKT     Carrier = "skt"
	CarrierLGT     Carrier = "lgt"
)

func (c Carrier) Validate() error {
	switch c {
	case CarrierUnknown, CarrierKTF, CarrierSKT, CarrierLGT:
		return nil
	default:
		return fmt.Errorf("unsupported carrier %q", c)
	}
}

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
	if s.Width <= 0 || s.Height <= 0 {
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
	minimumBytesPerLine := (s.Width*s.BitsPerPixel + 7) / 8
	if s.BytesPerLine < minimumBytesPerLine {
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

// KeyCode is the signed 32-bit MH_KeyCode value delivered by the WIPI HAL.
type KeyCode int32

const (
	KeyInvalid  KeyCode = 0
	Key0        KeyCode = '0'
	Key1        KeyCode = '1'
	Key2        KeyCode = '2'
	Key3        KeyCode = '3'
	Key4        KeyCode = '4'
	Key5        KeyCode = '5'
	Key6        KeyCode = '6'
	Key7        KeyCode = '7'
	Key8        KeyCode = '8'
	Key9        KeyCode = '9'
	KeyAsterisk KeyCode = '*'
	KeyPound    KeyCode = '#'

	KeyUp       KeyCode = -1
	KeyDown     KeyCode = -2
	KeyLeft     KeyCode = -3
	KeyRight    KeyCode = -4
	KeySelect   KeyCode = -5
	KeySoft1    KeyCode = -6
	KeySoft2    KeyCode = -7
	KeySoft3    KeyCode = -8
	KeySend     KeyCode = -10
	KeyEnd      KeyCode = -11
	KeyPower    KeyCode = -12
	KeySideUp   KeyCode = -13
	KeySideDown KeyCode = -14
	KeySideSel  KeyCode = -15
	KeyClear    KeyCode = -16
	KeyFlipDown KeyCode = -17
	KeyFlipUp   KeyCode = -18
)

func (k KeyCode) Valid() bool {
	switch k {
	case KeyInvalid,
		Key0, Key1, Key2, Key3, Key4, Key5, Key6, Key7, Key8, Key9,
		KeyAsterisk, KeyPound,
		KeyUp, KeyDown, KeyLeft, KeyRight, KeySelect,
		KeySoft1, KeySoft2, KeySoft3, KeySend, KeyEnd, KeyPower,
		KeySideUp, KeySideDown, KeySideSel, KeyClear, KeyFlipDown, KeyFlipUp:
		return true
	default:
		return false
	}
}

type VirtualKey int32

const (
	VirtualUp         VirtualKey = 1
	VirtualLeft       VirtualKey = 2
	VirtualRight      VirtualKey = 5
	VirtualDown       VirtualKey = 6
	VirtualFire       VirtualKey = 8
	VirtualGameA      VirtualKey = 9
	VirtualGameB      VirtualKey = 10
	VirtualGameC      VirtualKey = 11
	VirtualGameD      VirtualKey = 12
	VirtualSideUp     VirtualKey = 96
	VirtualSideDown   VirtualKey = 97
	VirtualSideSelect VirtualKey = 98
	VirtualSideClear  VirtualKey = 99
)

func (k VirtualKey) Valid() bool {
	switch k {
	case VirtualUp, VirtualLeft, VirtualRight, VirtualDown, VirtualFire,
		VirtualGameA, VirtualGameB, VirtualGameC, VirtualGameD,
		VirtualSideUp, VirtualSideDown, VirtualSideSelect, VirtualSideClear:
		return true
	default:
		return false
	}
}

type KeyMap map[VirtualKey]KeyCode

func (m KeyMap) Validate() error {
	physical := make(map[KeyCode]VirtualKey, len(m))
	for virtual, key := range m {
		if !virtual.Valid() {
			return fmt.Errorf("invalid virtual key %d", virtual)
		}
		if !key.Valid() || key == KeyInvalid {
			return fmt.Errorf("invalid physical key %d for virtual key %d", key, virtual)
		}
		if previous, exists := physical[key]; exists {
			return fmt.Errorf(
				"physical key %d maps to virtual keys %d and %d",
				key,
				previous,
				virtual,
			)
		}
		physical[key] = virtual
	}
	return nil
}

type Layer struct {
	ID         string            `json:"id"`
	Properties map[string]string `json:"properties,omitempty"`
	Quirks     map[string]bool   `json:"quirks,omitempty"`
	Keys       KeyMap            `json:"keys,omitempty"`
}

func (l Layer) validate(kind string) error {
	if strings.TrimSpace(l.ID) == "" {
		return fmt.Errorf("%s layer ID is empty", kind)
	}
	for key := range l.Properties {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s layer %q has an empty property name", kind, l.ID)
		}
	}
	for key := range l.Quirks {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s layer %q has an empty quirk name", kind, l.ID)
		}
	}
	if err := l.Keys.Validate(); err != nil {
		return fmt.Errorf("%s layer %q: %w", kind, l.ID, err)
	}
	return nil
}

type StandardLayer struct {
	Layer
	Version Version `json:"version"`
}

type CarrierLayer struct {
	Layer
	Carrier Carrier `json:"carrier"`
}

type ManufacturerLayer struct {
	Layer
	Manufacturer string `json:"manufacturer"`
}

type DeviceLayer struct {
	Layer
	Model  string `json:"model"`
	Screen Screen `json:"screen"`
}

type TitleLayer struct {
	Layer
	SHA256 string `json:"sha256"`
}

type Stack struct {
	Standard     StandardLayer      `json:"standard"`
	Carrier      *CarrierLayer      `json:"carrier,omitempty"`
	Manufacturer *ManufacturerLayer `json:"manufacturer,omitempty"`
	Device       *DeviceLayer       `json:"device,omitempty"`
	Title        *TitleLayer        `json:"title,omitempty"`
}

type Profile struct {
	ID           string            `json:"id"`
	Version      Version           `json:"wipi_version,omitempty"`
	Manufacturer string            `json:"manufacturer,omitempty"`
	Model        string            `json:"model,omitempty"`
	Carrier      Carrier           `json:"carrier"`
	Screen       Screen            `json:"screen"`
	TitleSHA256  string            `json:"title_sha256,omitempty"`
	Properties   map[string]string `json:"properties,omitempty"`
	Quirks       map[string]bool   `json:"quirks,omitempty"`
	Keys         KeyMap            `json:"keys,omitempty"`
	Layers       []string          `json:"layers,omitempty"`
}

func (p Profile) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("profile ID is empty")
	}
	if p.Version != "" {
		if err := p.Version.Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", p.ID, err)
		}
	}
	if p.Carrier == "" {
		p.Carrier = CarrierUnknown
	}
	if err := p.Carrier.Validate(); err != nil {
		return fmt.Errorf("profile %q: %w", p.ID, err)
	}
	if err := p.Screen.Validate(); err != nil {
		return fmt.Errorf("profile %q: %w", p.ID, err)
	}
	if p.TitleSHA256 != "" {
		if err := validateSHA256(p.TitleSHA256); err != nil {
			return fmt.Errorf("profile %q: %w", p.ID, err)
		}
	}
	for key := range p.Properties {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("profile %q has an empty property name", p.ID)
		}
	}
	for key := range p.Quirks {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("profile %q has an empty quirk name", p.ID)
		}
	}
	if err := p.Keys.Validate(); err != nil {
		return fmt.Errorf("profile %q: %w", p.ID, err)
	}
	seen := make(map[string]struct{}, len(p.Layers))
	for _, layer := range p.Layers {
		if _, exists := seen[layer]; exists {
			return fmt.Errorf("profile %q repeats layer %q", p.ID, layer)
		}
		seen[layer] = struct{}{}
	}
	return nil
}

// Resolve applies profile behavior from least to most specific:
// WIPI standard, carrier, manufacturer, device, then title.
func (s Stack) Resolve() (Profile, error) {
	if err := s.Standard.Layer.validate("standard"); err != nil {
		return Profile{}, err
	}
	if err := s.Standard.Version.Validate(); err != nil {
		return Profile{}, fmt.Errorf("standard layer %q: %w", s.Standard.ID, err)
	}

	result := Profile{
		ID:         s.Standard.ID,
		Version:    s.Standard.Version,
		Carrier:    CarrierUnknown,
		Properties: make(map[string]string),
		Quirks:     make(map[string]bool),
		Keys:       make(KeyMap),
	}
	applyLayer(&result, s.Standard.Layer)

	if s.Carrier != nil {
		if err := s.Carrier.Layer.validate("carrier"); err != nil {
			return Profile{}, err
		}
		if err := s.Carrier.Carrier.Validate(); err != nil {
			return Profile{}, fmt.Errorf("carrier layer %q: %w", s.Carrier.ID, err)
		}
		if s.Carrier.Carrier == CarrierUnknown {
			return Profile{}, fmt.Errorf("carrier layer %q cannot select unknown carrier", s.Carrier.ID)
		}
		result.Carrier = s.Carrier.Carrier
		applyLayer(&result, s.Carrier.Layer)
	}

	if s.Manufacturer != nil {
		if err := s.Manufacturer.Layer.validate("manufacturer"); err != nil {
			return Profile{}, err
		}
		if strings.TrimSpace(s.Manufacturer.Manufacturer) == "" {
			return Profile{}, fmt.Errorf("manufacturer layer %q has an empty manufacturer", s.Manufacturer.ID)
		}
		result.Manufacturer = s.Manufacturer.Manufacturer
		applyLayer(&result, s.Manufacturer.Layer)
	}

	if s.Device != nil {
		if err := s.Device.Layer.validate("device"); err != nil {
			return Profile{}, err
		}
		if strings.TrimSpace(s.Device.Model) == "" {
			return Profile{}, fmt.Errorf("device layer %q has an empty model", s.Device.ID)
		}
		if err := s.Device.Screen.Validate(); err != nil {
			return Profile{}, fmt.Errorf("device layer %q: %w", s.Device.ID, err)
		}
		result.Model = s.Device.Model
		result.Screen = s.Device.Screen
		applyLayer(&result, s.Device.Layer)
	}

	if s.Title != nil {
		if err := s.Title.Layer.validate("title"); err != nil {
			return Profile{}, err
		}
		if err := validateSHA256(s.Title.SHA256); err != nil {
			return Profile{}, fmt.Errorf("title layer %q: %w", s.Title.ID, err)
		}
		result.TitleSHA256 = strings.ToLower(s.Title.SHA256)
		applyLayer(&result, s.Title.Layer)
	}

	if err := result.Validate(); err != nil {
		return Profile{}, err
	}
	return result, nil
}

func applyLayer(profile *Profile, layer Layer) {
	profile.ID = layer.ID
	profile.Layers = append(profile.Layers, layer.ID)
	for key, value := range layer.Properties {
		profile.Properties[key] = value
	}
	for key, value := range layer.Quirks {
		profile.Quirks[key] = value
	}
	for virtual, physical := range layer.Keys {
		profile.Keys[virtual] = physical
	}
}

func validateSHA256(value string) error {
	if len(value) != 64 {
		return fmt.Errorf("SHA-256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid SHA-256: %w", err)
	}
	return nil
}
