package raptor

import (
	"encoding/binary"
	"slices"
	"testing"

	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"

	machinecore "github.com/mirusu400/aram-core/core"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
	"github.com/mirusu400/aram-core/profile"
	"github.com/mirusu400/aram-core/wipi"
)

func TestObservedPublicAPIsNormalizeAdapterNames(t *testing.T) {
	public := newPublicRuntime(t)
	public.Observed["MC_grpDrawString"] = 1
	public.Observed["RAPTOR.sndCreate"] = 1
	public.Observed["RAPTOR.knlSetTimer"] = 1
	public.Observed["RAPTOR.privateRuntimeInit1400"] = 1
	runtime := &Runtime{Public: public}

	if got := runtime.ObservedPublicAPIs(); !slices.Equal(got, []string{
		"MC_grpDrawString",
		"MC_knlSetTimer",
		"MC_mdaClipCreate",
	}) {
		t.Fatalf("observed public APIs = %v", got)
	}
}

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
		100:   "MC_knlPrintk",
		101:   "MC_knlSprintk",
		107:   "MC_knlExit",
		111:   "MC_knlGetProgramName",
		120:   "MC_knlGetTotalMemory",
		121:   "MC_knlGetFreeMemory",
		125:   "MC_knlCurrentTime",
		127:   "MC_knlSetSystemProperty",
		200:   "MC_grpGetImageProperty",
		201:   "MC_grpGetImageFrameBuffer",
		202:   "MC_grpGetScreenFrameBuffer",
		203:   "MC_grpDestroyOffScreenFrameBuffer",
		204:   "MC_grpCreateOffScreenFrameBuffer",
		205:   "MC_grpInitContext",
		206:   "MC_grpSetContext",
		209:   "MC_grpDrawLine",
		210:   "MC_grpDrawRect",
		211:   "MC_grpFillRect",
		222:   "MC_grpFlushLcd",
		223:   "MC_grpGetPixelFromRGB",
		225:   "MC_grpGetDisplayInfo",
		226:   "MC_grpRepaint",
		227:   "MC_grpGetFont",
		233:   "MC_grpCreateImage",
		400:   "MC_fsOpen",
		401:   "MC_fsRead",
		402:   "MC_fsWrite",
		403:   "MC_fsClose",
		404:   "MC_fsSeek",
		405:   "MC_fsFileAttribute",
		412:   "MC_fsAvailable",
		117:   "MC_knlAlloc",
		118:   "MC_knlCalloc",
		119:   "MC_knlFree",
		122:   "MC_knlDefTimer",
		123:   "MC_knlSetTimer",
		128:   "MC_knlGetResourceID",
		129:   "MC_knlGetResource",
		600:   "MC_netConnect",
		601:   "MC_netClose",
		606:   "MC_netSocketClose",
		1029:  "strcpy",
		1030:  "strncpy",
		1031:  "strcat",
		1040:  "strstr",
		1041:  "strlen",
		1044:  "memcpy",
		1048:  "memset",
		1233:  "MC_mdaSetMuteState",
		1234:  "MC_mdaGetMuteState",
		1400:  "MC_miscBackLight",
		0x4c1: "MC_mdaVibrator",
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

// 제노니아1 drives its sound through the private module-507 ordinals: it builds
// a clip, plays it, stops the clip a scene owns before asking for the next
// sound, and frees the handle once the clip reports that it finished. Leaving
// the stop and the free unimplemented left the title's sound object holding a
// clip it believed was still playing, and its allocator refuses to build a
// second clip while that handle is held (issue #49).
func TestRaptorPrivateSoundOrdinalsStopAndFreeClips(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	handle, err := public.RaptorCreateClip("Yamaha_MA3", 64, 0x02000001)
	if err != nil || handle == 0 {
		t.Fatalf("RaptorCreateClip = 0x%08x, err=%v", handle, err)
	}
	if !public.RaptorPlayClip(handle, true) {
		t.Fatal("RaptorPlayClip refused to start the clip")
	}
	public.PendingCallbacks = nil

	call := func(ordinal uint32) string {
		t.Helper()
		if err := runtime.CPU.WriteRegister(cpu.RegisterR0, handle); err != nil {
			t.Fatal(err)
		}
		_, name, handled, err := runtime.DispatchPrivateImport(ordinal)
		if err != nil || !handled {
			t.Fatalf("ordinal %d handled=%v err=%v", ordinal, handled, err)
		}
		return name
	}

	if name := call(1213); name != "RAPTOR.sndStop" {
		t.Fatalf("ordinal 1213 = %q", name)
	}
	if clip := public.MediaClips[handle]; clip == nil || clip.State != 0 {
		t.Fatalf("stopped clip = %+v", public.MediaClips[handle])
	}
	if len(public.PendingCallbacks) != 1 ||
		public.PendingCallbacks[0].Args !=
			[4]uint32{handle, wipirt.RaptorClipEndCode, 0, 0} {
		t.Fatalf("stop completion callback = %+v", public.PendingCallbacks)
	}

	public.MediaClips[handle].Data = []byte{1, 2, 3, 4}
	if name := call(1206); name != "RAPTOR.sndClearData" {
		t.Fatalf("ordinal 1206 = %q", name)
	}
	if clip := public.MediaClips[handle]; clip == nil || len(clip.Data) != 0 {
		t.Fatalf("cleared clip = %+v", public.MediaClips[handle])
	}
	if name, ok := raptorWIPIImportName(1206); !ok ||
		name != "MC_mdaClipClearData" {
		t.Fatalf("raptorWIPIImportName(1206) = %q, %v", name, ok)
	}

	if name := call(1201); name != "RAPTOR.sndFree" {
		t.Fatalf("ordinal 1201 = %q", name)
	}
	if _, live := public.MediaClips[handle]; live {
		t.Fatal("freed clip is still registered")
	}
}
