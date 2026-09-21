package skvmhost

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/j2me"
	shared "github.com/mirusu400/aram-core/runtime"
	engine "github.com/mirusu400/aram-core/skvm"
)

func TestLGTFramebufferUsesAvailableMediumAssets(t *testing.T) {
	fallback := image.Pt(240, 320)
	for _, test := range []struct {
		name      string
		resources map[string][]byte
		want      image.Point
	}{
		{"medium only", map[string][]byte{"imgM/menu.png": {1}}, image.Pt(176, 220)},
		{"large available", map[string][]byte{"imgM/menu.png": {1}, "imgL/menu.png": {1}}, fallback},
		{"unrelated assets", map[string][]byte{"menu.png": {1}}, fallback},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := inferLGTFramebufferSize(fallback, test.resources); got != test.want {
				t.Fatalf("size = %v, want %v", got, test.want)
			}
		})
	}
	if got := inferLGTFramebufferSize(image.Pt(176, 208), map[string][]byte{"imgM/menu.png": {1}}); got != image.Pt(176, 208) {
		t.Fatalf("explicit medium size changed to %v", got)
	}
}

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
