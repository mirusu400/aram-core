package skvmhost

import (
	"context"
	"fmt"
	"image"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/j2me"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	engine "github.com/mirusu400/aram-core/skvm"
)

// Application is the carrier-neutral Java host input. Installer-provided record
// stores are optional. J2ME never interprets SKT installer records as LGT data.
type Application struct {
	MainClass          string
	Properties         map[string]string
	Classes, Resources map[string][]byte
	RecordStores       []skloader.RecordStore
}

const j2meMachineStateMagic = "ARAMJ2M\x00"

func NewJ2ME(ctx context.Context, source machinecore.Source, pkg j2me.Package,
	size image.Point, sampleRate uint32, channels uint8) (*Machine, error) {
	return newJavaMachine(ctx, source, Application{MainClass: pkg.Descriptor.MainClass,
		Properties: pkg.Descriptor.Raw, Classes: pkg.Classes, Resources: pkg.Resources}, nil, size, sampleRate, channels)
}

func configureJavaIdentity(config *shared.Config, source machinecore.Source, legacy bool) (string, string, engine.NativePolicy, error) {
	if legacy {
		config.Device.ProfileID = ProfileID
		if source.ProfileID != "" {
			config.Device.ProfileID = source.ProfileID
		}
		config.Device.Carrier = "skt"
		return "skvm", skvmMachineStateMagic, engine.NativePolicySKT, nil
	}
	config.Device.ProfileID = j2me.ProfileID
	config.Device.WIPIVersion = ""
	config.Device.Carrier = "unknown"
	policy := engine.NativePolicyJ2ME
	switch source.ProfileID {
	case "", j2me.ProfileID:
	case j2me.LGTProfileID:
		config.Device.ProfileID = j2me.LGTProfileID
		config.Device.Carrier = "lgt"
		policy = engine.NativePolicyLGT
	default:
		return "", "", 0, fmt.Errorf("unsupported J2ME profile %q", source.ProfileID)
	}
	return "j2me", j2meMachineStateMagic, policy, nil
}

// SourceInfo reports Java identity without inventing ARM image/entry metadata.
// ReaderAt is intentionally omitted from this diagnostic contract.
func (m *Machine) SourceInfo() machinecore.Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return machinecore.Source{Name: m.source.Name, Size: m.source.Size, SHA256: m.source.SHA256, Format: m.runtimeID,
		ProfileID: m.services.Config.Device.ProfileID}
}
