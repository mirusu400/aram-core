package skvmhost

import (
	"errors"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/j2me"
	shared "github.com/mirusu400/aram-core/runtime"
	engine "github.com/mirusu400/aram-core/skvm"
)

func TestJavaProfileCompatibilityIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, profile, wantProfile, carrier, runtime, magic string
		legacy                                              bool
		policy                                              engine.NativePolicy
	}{
		{"legacy default", "", ProfileID, "skt", "skvm", skvmMachineStateMagic, true, engine.NativePolicySKT},
		{"legacy explicit", ProfileID, ProfileID, "skt", "skvm", skvmMachineStateMagic, true, engine.NativePolicySKT},
		{"java default", "", j2me.ProfileID, "unknown", "j2me", j2meMachineStateMagic, false, engine.NativePolicyJ2ME},
		{"java explicit", j2me.ProfileID, j2me.ProfileID, "unknown", "j2me", j2meMachineStateMagic, false, engine.NativePolicyJ2ME},
		{"java lgt", j2me.LGTProfileID, j2me.LGTProfileID, "lgt", "j2me", j2meMachineStateMagic, false, engine.NativePolicyLGT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := shared.DefaultConfig()
			version := config.Device.WIPIVersion
			runtime, magic, policy, err := configureJavaIdentity(&config, machinecore.Source{ProfileID: tc.profile}, tc.legacy)
			if err != nil || runtime != tc.runtime || magic != tc.magic || policy != tc.policy || config.Device.ProfileID != tc.wantProfile || config.Device.Carrier != tc.carrier {
				t.Fatalf("identity: runtime=%q magic=%q policy=%v device=%+v err=%v", runtime, magic, policy, config.Device, err)
			}
			if !tc.legacy {
				version = ""
			}
			if config.Device.WIPIVersion != version {
				t.Fatalf("WIPI version=%q, want %q", config.Device.WIPIVersion, version)
			}
		})
	}
	for _, legacy := range []bool{true, false} {
		config := shared.DefaultConfig()
		_, _, _, err := configureJavaIdentity(&config, machinecore.Source{ProfileID: "unknown/profile"}, legacy)
		if !errors.Is(err, ErrUnsupportedProfile) {
			t.Fatalf("legacy=%v error=%v, want ErrUnsupportedProfile", legacy, err)
		}
	}
}
