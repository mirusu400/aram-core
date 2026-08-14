package raptor

import (
	"encoding/binary"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
	"github.com/mirusu400/aram-core/profile"
	"github.com/mirusu400/aram-core/wipi"
)

func TestRaptorRVCTImportVeneerSectionIsExecutable(t *testing.T) {
	section := raptorloader.Section{Data: make([]byte, 8)}
	binary.LittleEndian.PutUint32(section.Data[0:4], 0xe52de004)
	binary.LittleEndian.PutUint32(section.Data[4:8], 0xeb000000)
	if !raptorSectionExecutable(section) {
		t.Fatal("RVCT import veneer section is not executable")
	}
	section.Data[0] = 0
	if raptorSectionExecutable(section) {
		t.Fatal("ordinary writable data section is executable")
	}
}

func TestRaptorWIPIImportsResolveToPublicCatalog(t *testing.T) {
	expected := map[uint32]string{
		100:  "MC_knlPrintk",
		101:  "MC_knlSprintk",
		120:  "MC_knlGetTotalMemory",
		121:  "MC_knlGetFreeMemory",
		125:  "MC_knlCurrentTime",
		127:  "MC_knlSetSystemProperty",
		200:  "MC_grpGetImageProperty",
		201:  "MC_grpGetImageFrameBuffer",
		202:  "MC_grpGetScreenFrameBuffer",
		204:  "MC_grpCreateOffScreenFrameBuffer",
		205:  "MC_grpInitContext",
		206:  "MC_grpSetContext",
		209:  "MC_grpDrawLine",
		210:  "MC_grpDrawRect",
		211:  "MC_grpFillRect",
		222:  "MC_grpFlushLcd",
		223:  "MC_grpGetPixelFromRGB",
		225:  "MC_grpGetDisplayInfo",
		227:  "MC_grpGetFont",
		233:  "MC_grpCreateImage",
		400:  "MC_fsOpen",
		401:  "MC_fsRead",
		402:  "MC_fsWrite",
		403:  "MC_fsClose",
		404:  "MC_fsSeek",
		405:  "MC_fsFileAttribute",
		117:  "MC_knlAlloc",
		118:  "MC_knlCalloc",
		119:  "MC_knlFree",
		122:  "MC_knlDefTimer",
		123:  "MC_knlSetTimer",
		128:  "MC_knlGetResourceID",
		129:  "MC_knlGetResource",
		1029: "strcpy",
		1030: "strncpy",
		1031: "strcat",
		1040: "strstr",
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
			callback, ok := InputCallback(procedure, machinecore.InputEvent{
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
			if callback.Procedure != procedure || callback.Args != want {
				t.Fatalf(
					"callback = %#v, want procedure 0x%x args %#v",
					callback,
					procedure,
					want,
				)
			}

			callback, ok = InputCallback(procedure, machinecore.InputEvent{
				Control: test.control,
			})
			if !ok || callback.Args[0] != raptorKeyReleaseEvent {
				t.Fatalf("release callback = %#v, ok=%t", callback, ok)
			}
		})
	}
	if _, ok := InputCallback(0x12345, machinecore.InputEvent{
		Control: "unknown",
		Pressed: true,
	}); ok {
		t.Fatal("unknown control was mapped")
	}
}
