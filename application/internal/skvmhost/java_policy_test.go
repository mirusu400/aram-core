package skvmhost

import (
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/j2me"
	shared "github.com/mirusu400/aram-core/runtime"
	engine "github.com/mirusu400/aram-core/skvm"
)

func TestExplicitLGTProfileSelectsIndependentNativePolicy(t *testing.T) {
	var generic, lgt, skt shared.Config
	_, _, genericPolicy, err := configureJavaIdentity(&generic, machinecore.Source{}, false)
	if err != nil || genericPolicy != engine.NativePolicyJ2ME || generic.Device.Carrier != "unknown" {
		t.Fatalf("default Java identity: %+v, policy=%v, err=%v", generic.Device, genericPolicy, err)
	}
	runtime, magic, lgtPolicy, err := configureJavaIdentity(&lgt, machinecore.Source{ProfileID: j2me.LGTProfileID}, false)
	if err != nil || lgtPolicy == genericPolicy || lgtPolicy == engine.NativePolicySKT {
		t.Fatalf("explicit LGT must select its own native policy: policy=%v, err=%v", lgtPolicy, err)
	}
	if runtime != "j2me" || magic != j2meMachineStateMagic || lgt.Device.ProfileID != j2me.LGTProfileID || lgt.Device.Carrier != "lgt" || lgt.Device.WIPIVersion != "" {
		t.Fatalf("LGT identity changed: runtime=%s magic=%q device=%+v", runtime, magic, lgt.Device)
	}
	_, _, sktPolicy, err := configureJavaIdentity(&skt, machinecore.Source{}, true)
	if err != nil || sktPolicy != engine.NativePolicySKT || skt.Device.Carrier != "skt" {
		t.Fatalf("legacy identity changed: %+v, policy=%v, err=%v", skt.Device, sktPolicy, err)
	}
}
