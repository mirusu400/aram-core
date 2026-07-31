package application

import (
	"context"
	"encoding/binary"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/profile"
	"github.com/mirusu400/aram-core/wipi"
)

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
		211:  "MC_grpFillRect",
		222:  "MC_grpFlushLcd",
		223:  "MC_grpGetPixelFromRGB",
		225:  "MC_grpGetDisplayInfo",
		227:  "MC_grpGetFont",
		233:  "MC_grpCreateImage",
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

func TestRaptorVolumeImportsExposeLGTVolumeRoots(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &raptorRuntime{
		cpu:    public.cpu,
		public: public,
	}
	if err := runtime.installInterfaces(); err != nil {
		t.Fatal(err)
	}

	count, name, handled, err := runtime.dispatchPrivateImport(300)
	if err != nil || !handled || name != "RAPTOR.fsGetVolumeCount" ||
		count.low != 2 {
		t.Fatalf(
			"volume count = %#v, %q, handled=%t, err=%v",
			count,
			name,
			handled,
			err,
		)
	}
	list, name, handled, err := runtime.dispatchPrivateImport(301)
	if err != nil || !handled || name != "RAPTOR.fsGetVolumeList" ||
		list.low != raptorVolumeTable {
		t.Fatalf(
			"volume list = %#v, %q, handled=%t, err=%v",
			list,
			name,
			handled,
			err,
		)
	}
	var pointers [8]byte
	if err := runtime.cpu.ReadMemory(list.low, pointers[:]); err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"/L", "/S"} {
		address := binary.LittleEndian.Uint32(pointers[index*4:])
		got, err := public.readCString(address)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("volume %d = %q, want %q", index, got, want)
		}
	}

	selected, name, handled, err := runtime.dispatchPrivateImport(302)
	if err != nil || !handled || name != "RAPTOR.fsSelectVolume" ||
		selected != (wipiReturn{}) {
		t.Fatalf(
			"select volume = %#v, %q, handled=%t, err=%v",
			selected,
			name,
			handled,
			err,
		)
	}
}

func TestRaptorFramebufferImportsExposeLGTGeometry(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &raptorRuntime{
		cpu:    public.cpu,
		public: public,
	}
	handle, err := public.ensureScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := public.framebuffers[handle]
	tests := []struct {
		ordinal uint32
		input   uint32
		want    uint32
		name    string
	}{
		{50, handle, framebuffer.pixels, "RAPTOR.grpGetFrameBufferPixels"},
		{51, handle, uint32(framebuffer.width), "RAPTOR.grpGetFrameBufferWidth"},
		{52, handle, uint32(framebuffer.height), "RAPTOR.grpGetFrameBufferHeight"},
		{
			53,
			handle,
			uint32(framebuffer.width * framebuffer.bitsPerPixel / 8),
			"RAPTOR.grpGetFrameBufferBytesPerLine",
		},
		{
			54,
			framebuffer.pixels,
			uint32(framebuffer.bitsPerPixel),
			"RAPTOR.grpGetFrameBufferBitsPerPixel",
		},
	}
	for _, test := range tests {
		if err := runtime.cpu.WriteRegister(cpu.RegisterR0, test.input); err != nil {
			t.Fatal(err)
		}
		got, name, handled, err := runtime.dispatchPrivateImport(test.ordinal)
		if err != nil || !handled || name != test.name || got.low != test.want {
			t.Errorf(
				"ordinal %d = %#v, %q, handled=%t, err=%v; want 0x%08x, %q",
				test.ordinal,
				got,
				name,
				handled,
				err,
				test.want,
				test.name,
			)
		}
	}
}

func TestRaptorTimerImportsUseFourByteLGTABI(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &raptorRuntime{
		cpu:    public.cpu,
		public: public,
	}
	timer, err := public.heap.allocate(32, true)
	if err != nil || timer == 0 {
		t.Fatalf("allocate timer = 0x%08x, %v", timer, err)
	}
	const (
		callback  = uint32(0x12345)
		timeout   = uint32(47)
		parameter = uint32(0x89abcdef)
		sentinel  = uint32(0xfeedface)
	)
	if err := public.writeU32(timer+4, sentinel); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{timer, callback} {
		if err := runtime.cpu.WriteRegister(uint32(register), value); err != nil {
			t.Fatal(err)
		}
	}
	result, name, handled, err := runtime.dispatchPrivateImport(122)
	if err != nil || !handled || name != "RAPTOR.knlDefTimer" ||
		result != (wipiReturn{}) {
		t.Fatalf(
			"define timer = %#v, %q, handled=%t, err=%v",
			result,
			name,
			handled,
			err,
		)
	}
	if got, err := public.readU32(timer); err != nil || got != callback {
		t.Fatalf("timer callback = 0x%08x, %v", got, err)
	}
	if got, err := public.readU32(timer + 4); err != nil || got != sentinel {
		t.Fatalf("define timer overwrote adjacent word = 0x%08x, %v", got, err)
	}
	for register, value := range []uint32{timer, timeout, 0, parameter} {
		if err := runtime.cpu.WriteRegister(uint32(register), value); err != nil {
			t.Fatal(err)
		}
	}
	result, name, handled, err = runtime.dispatchPrivateImport(123)
	if err != nil || !handled || name != "RAPTOR.knlSetTimer" ||
		result != (wipiReturn{}) {
		t.Fatalf(
			"set timer = %#v, %q, handled=%t, err=%v",
			result,
			name,
			handled,
			err,
		)
	}
	got, ok := public.timers[timer]
	if !ok || got.callback != callback || got.parameter != parameter ||
		got.deadline != uint64(timeout) {
		t.Fatalf("timer = %+v, present=%t", got, ok)
	}
	if got, err := public.readU32(timer + 4); err != nil || got != sentinel {
		t.Fatalf("set timer overwrote adjacent word = 0x%08x, %v", got, err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, timer); err != nil {
		t.Fatal(err)
	}
	result, name, handled, err = runtime.dispatchPrivateImport(124)
	if err != nil || !handled || name != "RAPTOR.knlUnsetTimer" ||
		result != (wipiReturn{}) {
		t.Fatalf(
			"unset timer = %#v, %q, handled=%t, err=%v",
			result,
			name,
			handled,
			err,
		)
	}
	if _, ok := public.timers[timer]; ok {
		t.Fatal("timer remains active after unset")
	}
	if got, err := public.readU32(timer + 4); err != nil || got != sentinel {
		t.Fatalf("unset timer overwrote adjacent word = 0x%08x, %v", got, err)
	}
}

func TestRaptorTimerExpiryPreservesAdjacentGuestMemory(t *testing.T) {
	machine := newSyntheticMachine(t)
	const callback = uint32(0x04000000)
	if err := machine.cpu.Map(
		callback,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(callback, []byte{
		0x2a, 0x22, // movs r2, #42
		0x0a, 0x60, // str r2, [r1]
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	timer, err := machine.wipi.heap.allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := machine.wipi.heap.allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	const sentinel = uint32(0xfeedface)
	if err := machine.wipi.writeU32(timer, callback|1); err != nil {
		t.Fatal(err)
	}
	if err := machine.wipi.writeU32(timer+24, sentinel); err != nil {
		t.Fatal(err)
	}
	if result, err := machine.wipi.setTimer(timer, 16, marker, false); err != nil ||
		result != (wipiReturn{}) {
		t.Fatalf("set timer = %#v, %v", result, err)
	}
	machine.raptor = &raptorRuntime{}
	if _, stopped, err := machine.pumpWIPICallbacks(
		context.Background(),
		16,
	); err != nil || stopped {
		t.Fatalf("pump callbacks: stopped=%t, err=%v", stopped, err)
	}
	if got, err := machine.wipi.readU32(marker); err != nil || got != 42 {
		t.Fatalf("callback marker = %d, %v", got, err)
	}
	if got, err := machine.wipi.readU32(timer + 24); err != nil || got != sentinel {
		t.Fatalf("timer expiry overwrote adjacent word = 0x%08x, %v", got, err)
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
