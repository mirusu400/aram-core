package application

import (
	"image"
	"testing"
)

// The canvas of a package without an exact policy follows what the title
// draws in its opening (issues #565, #567). 신시티 and 챔피언쉽야구2006 lay
// their menus and HUDs across 176 columns and were cut off on 120x160;
// 건버드+'s small-handset build keeps its logo, menu and scenery within 120
// columns and only fills the rest with its sky colour.
func TestBREWCanvasProbeFollowsTitleLayout(t *testing.T) {
	for _, test := range []struct {
		name, digest string
		want         image.Rectangle
	}{
		{"신시티", "cdb05619d4cdd8a7f5bcfbe68c8e82801d326b4b247ad4f0083c76100a7ecc92", image.Rect(0, 0, 176, 220)},
		{"챔피언쉽야구2006", "f6e05590a961cd8742012731a856110c291a57bc087e97299e2ec592958815a5", image.Rect(0, 0, 176, 220)},
		{"건버드+", "c23aac3be59e08d0b4973743b4c9ec3172b69f078df322464a5b3f6d8799c223", image.Rect(0, 0, 120, 160)},
	} {
		path, data := findAuthorizedPackage(t, test.digest)
		machine := newBREWReferenceMachine(t, path, data)
		if got := machine.Framebuffer().Bounds(); got != test.want {
			t.Errorf("%s canvas = %v, want %v", test.name, got, test.want)
		}
	}
}
