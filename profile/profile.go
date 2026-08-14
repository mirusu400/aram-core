package profile

import (
	"encoding/hex"
	"fmt"
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

type Layer struct {
	ID           string            `json:"id"`
	Properties   map[string]string `json:"properties,omitempty"`
	Quirks       map[string]bool   `json:"quirks,omitempty"`
	Keys         KeyMap            `json:"keys,omitempty"`
	Capabilities Capabilities      `json:"capabilities,omitempty"`
	Limits       Limits            `json:"limits,omitempty"`
}

func (l Layer) validate(kind string) error {
	if !validBoundedString(l.ID, 255, false) {
		return fmt.Errorf("%s layer ID is invalid", kind)
	}
	if len(l.Properties) > 1024 || len(l.Quirks) > 1024 ||
		len(l.Keys) > 1024 || len(l.Capabilities) > 1024 ||
		len(l.Limits) > 1024 {
		return fmt.Errorf("%s layer %q exceeds entry limits", kind, l.ID)
	}
	for key, value := range l.Properties {
		if !validBoundedString(key, 255, false) ||
			!validBoundedString(value, 4096, true) {
			return fmt.Errorf("%s layer %q has an invalid property", kind, l.ID)
		}
	}
	for key := range l.Quirks {
		if !validBoundedString(key, 255, false) {
			return fmt.Errorf("%s layer %q has an invalid quirk name", kind, l.ID)
		}
	}
	if err := l.Keys.Validate(); err != nil {
		return fmt.Errorf("%s layer %q: %w", kind, l.ID, err)
	}
	if err := l.Capabilities.Validate(); err != nil {
		return fmt.Errorf("%s layer %q: %w", kind, l.ID, err)
	}
	if err := l.Limits.Validate(); err != nil {
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
	Capabilities Capabilities      `json:"capabilities,omitempty"`
	Limits       Limits            `json:"limits,omitempty"`
	Layers       []string          `json:"layers,omitempty"`
}

func (p Profile) Validate() error {
	if !validBoundedString(p.ID, 255, false) {
		return fmt.Errorf("profile ID is invalid")
	}
	if !validBoundedString(p.Manufacturer, 127, true) ||
		!validBoundedString(p.Model, 127, true) ||
		len(p.Properties) > 1024 || len(p.Quirks) > 1024 ||
		len(p.Keys) > 1024 || len(p.Capabilities) > 1024 ||
		len(p.Limits) > 1024 || len(p.Layers) > 16 {
		return fmt.Errorf("profile %q exceeds metadata limits", p.ID)
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
	for key, value := range p.Properties {
		if !validBoundedString(key, 255, false) ||
			!validBoundedString(value, 4096, true) {
			return fmt.Errorf("profile %q has an invalid property", p.ID)
		}
	}
	for key := range p.Quirks {
		if !validBoundedString(key, 255, false) {
			return fmt.Errorf("profile %q has an invalid quirk name", p.ID)
		}
	}
	if err := p.Keys.Validate(); err != nil {
		return fmt.Errorf("profile %q: %w", p.ID, err)
	}
	if err := p.Capabilities.Validate(); err != nil {
		return fmt.Errorf("profile %q: %w", p.ID, err)
	}
	if err := p.Limits.Validate(); err != nil {
		return fmt.Errorf("profile %q: %w", p.ID, err)
	}
	seen := make(map[string]struct{}, len(p.Layers))
	for _, layer := range p.Layers {
		if !validBoundedString(layer, 255, false) {
			return fmt.Errorf("profile %q has an invalid layer ID", p.ID)
		}
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
		ID:           s.Standard.ID,
		Version:      s.Standard.Version,
		Carrier:      CarrierUnknown,
		Properties:   make(map[string]string),
		Quirks:       make(map[string]bool),
		Keys:         make(KeyMap),
		Capabilities: make(Capabilities),
		Limits:       make(Limits),
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
		if !validBoundedString(s.Manufacturer.Manufacturer, 127, false) {
			return Profile{}, fmt.Errorf("manufacturer layer %q has an invalid manufacturer", s.Manufacturer.ID)
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
		if !validBoundedString(s.Device.Model, 127, false) {
			return Profile{}, fmt.Errorf("device layer %q has an invalid model", s.Device.ID)
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
	for capability, enabled := range layer.Capabilities {
		profile.Capabilities[capability] = enabled
	}
	for limit, value := range layer.Limits {
		profile.Limits[limit] = value
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

func validBoundedString(value string, limit int, allowEmpty bool) bool {
	if len(value) > limit || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	return allowEmpty || strings.TrimSpace(value) != ""
}
