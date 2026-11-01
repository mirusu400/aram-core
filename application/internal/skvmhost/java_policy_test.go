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
		{"medium logo only", map[string][]byte{"logo/m_ezi.png": {1}, "logo/s_ezi.png": {1}}, image.Pt(176, 220)},
		{"large available", map[string][]byte{"imgM/menu.png": {1}, "imgL/menu.png": {1}}, fallback},
		{"large logo available", map[string][]byte{"logo/m_ezi.png": {1}, "logo/l_ezi.png": {1}}, fallback},
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

// 75 of the corpus's 105 LGT MIDlets declare MIDletX-LCD-Size: 176,200. On
// the default 240x320 canvas 테트리스2005 picked its absent 240x320 assets
// and drew a null image, and 화재진압119 stopped at "Bad Download.".
func TestLGTCanvasHonoursDeclaredLCDSize(t *testing.T) {
	fallback := image.Pt(240, 320)
	pkg := func(lcd string, resources map[string][]byte) j2me.Package {
		raw := map[string]string{}
		if lcd != "" {
			raw["MIDletX-LCD-Size"] = lcd
		}
		return j2me.Package{Descriptor: j2me.Descriptor{MainClass: "M", Raw: raw}, Resources: resources}
	}
	for _, test := range []struct {
		name string
		pkg  j2me.Package
		want image.Point
	}{
		{"declared medium", pkg("176,200", nil), image.Pt(176, 200)},
		{"declared with spaces", pkg(" 120 , 143 ", nil), image.Pt(120, 143)},
		{"declared large keeps canvas", pkg("240,290", nil), fallback},
		{"no declaration", pkg("", nil), fallback},
		{"malformed", pkg("176x200", nil), fallback},
		{"out of range", pkg("17,20", nil), fallback},
		// Asset inference already sized the canvas; a same-width
		// declaration does not narrow it further.
		{"inferred medium wins", pkg("176,200", map[string][]byte{"imgM/menu.png": {1}}), image.Pt(176, 220)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := lgtCanvas("not-a-listed-package", test.pkg, fallback); got != test.want {
				t.Fatalf("canvas = %v, want %v", got, test.want)
			}
		})
	}
	// An exact-package entry still decides its own canvas: Churining declares
	// 176,200 and keeps the 176x200 its entry names; Do declares 176,200 and
	// keeps its entry's 176x220.
	churining := pkg("176,200", nil)
	churining.Descriptor.MainClass = "Churining"
	if got := lgtCanvas("cd18ad615cbf2bd2f14c3e711002019582a2512247f2ce10b7b8eef2abb387c3", churining, fallback); got != image.Pt(176, 200) {
		t.Fatalf("Churining canvas = %v", got)
	}
	do := pkg("176,200", nil)
	do.Descriptor.MainClass = "Do"
	if got := lgtCanvas("b7b2e9579545bf8e0ae06c48b57ad05187dcbcb423a4838ef811edf51df58b52", do, fallback); got != image.Pt(176, 220) {
		t.Fatalf("Do canvas = %v", got)
	}
}
