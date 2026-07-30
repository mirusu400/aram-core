package application

import (
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/profile"
	"github.com/mirusu400/aram-core/wipi"
)

func TestRaptorWIPIImportsResolveToPublicCatalog(t *testing.T) {
	expected := map[uint32]string{
		101:  "MC_knlSprintk",
		200:  "MC_grpGetImageProperty",
		201:  "MC_grpGetImageFrameBuffer",
		202:  "MC_grpGetScreenFrameBuffer",
		204:  "MC_grpCreateOffScreenFrameBuffer",
		205:  "MC_grpInitContext",
		206:  "MC_grpSetContext",
		209:  "MC_grpDrawLine",
		211:  "MC_grpFillRect",
		222:  "MC_grpFlushLcd",
		223:  "MC_grpGetPixelFromRGB",
		233:  "MC_grpCreateImage",
		118:  "MC_knlAlloc",
		119:  "MC_knlFree",
		122:  "MC_knlDefTimer",
		123:  "MC_knlSetTimer",
		128:  "MC_knlGetResourceID",
		129:  "MC_knlGetResource",
		1031: "strcat",
		1041: "strlen",
		1044: "memcpy",
		1048: "memset",
	}
	for ordinal, want := range expected {
		got, ok := raptorWIPIImportName(ordinal)
		if !ok || got != want {
			t.Errorf("Raptor import %d = %q, %v; want %q, true", ordinal, got, ok, want)
			continue
		}
		if _, ok := wipi.Lookup(got); !ok {
			t.Errorf("Raptor import %d resolves to missing public API %q", ordinal, got)
		}
	}
	if got, ok := raptorWIPIImportName(0); ok || got != "" {
		t.Errorf("unknown Raptor import = %q, %v; want empty, false", got, ok)
	}
}

func TestRaptorInputCallbackMapsFrontendControls(t *testing.T) {
	tests := []struct {
		control string
		key     profile.KeyCode
	}{
		{control: "up", key: profile.KeyUp},
		{control: "down", key: profile.KeyDown},
		{control: "left", key: profile.KeyLeft},
		{control: "right", key: profile.KeyRight},
		{control: "ok", key: profile.KeySelect},
		{control: "fire", key: profile.KeySelect},
		{control: "soft-left", key: profile.KeySoft1},
		{control: "soft-right", key: profile.KeySoft2},
		{control: "menu", key: profile.KeySoft3},
		{control: "back", key: profile.KeyClear},
		{control: "star", key: profile.KeyAsterisk},
		{control: "hash", key: profile.KeyPound},
		{control: "num0", key: profile.Key0},
		{control: "num5", key: profile.Key5},
		{control: "num9", key: profile.Key9},
	}
	const procedure = uint32(0x12345)
	for _, test := range tests {
		t.Run(test.control, func(t *testing.T) {
			callback, ok := raptorInputCallback(procedure, machinecore.InputEvent{
				Control: test.control,
				Pressed: true,
			})
			if !ok {
				t.Fatal("input was not mapped")
			}
			want := [4]uint32{
				raptorKeyPressEvent,
				uint32(int32(test.key)),
				0,
				0,
			}
			if callback.procedure != procedure || callback.args != want {
				t.Fatalf(
					"callback = %#v, want procedure 0x%x args %#v",
					callback,
					procedure,
					want,
				)
			}

			callback, ok = raptorInputCallback(procedure, machinecore.InputEvent{
				Control: test.control,
			})
			if !ok || callback.args[0] != raptorKeyReleaseEvent {
				t.Fatalf("release callback = %#v, ok=%t", callback, ok)
			}
		})
	}
	if _, ok := raptorInputCallback(0x12345, machinecore.InputEvent{
		Control: "unknown",
		Pressed: true,
	}); ok {
		t.Fatal("unknown control was mapped")
	}
}
