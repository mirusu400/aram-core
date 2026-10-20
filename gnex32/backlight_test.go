package gnex32

import "testing"

func TestGNEX32BacklightModesAndResetDefault(t *testing.T) {
	newRuntime := func() *Runtime {
		runtime, err := NewRuntime(
			gnex32AudioTestImage(nil),
			gnex32TestDeviceProfile(),
		)
		if err != nil {
			t.Fatal(err)
		}
		return runtime
	}

	runtime := newRuntime()
	if runtime.BacklightMode() != BacklightMMI || !runtime.BacklightEnabled() {
		t.Fatalf("default backlight = mode %d enabled %v",
			runtime.BacklightMode(), runtime.BacklightEnabled())
	}
	for _, test := range []struct {
		mode    uint32
		enabled bool
	}{
		{mode: uint32(BacklightOff), enabled: false},
		{mode: uint32(BacklightOn), enabled: true},
		{mode: uint32(BacklightMMI), enabled: true},
	} {
		result, err := runtime.service(0x91, []uint32{test.mode})
		if err != nil {
			t.Fatal(err)
		}
		if result.Pop != 1 || result.Push != nil {
			t.Fatalf("SetBackLight(%d) result = %+v", test.mode, result)
		}
		if runtime.BacklightMode() != BacklightMode(test.mode) || runtime.BacklightEnabled() != test.enabled {
			t.Fatalf("SetBackLight(%d) = mode %d enabled %v",
				test.mode, runtime.BacklightMode(), runtime.BacklightEnabled())
		}
	}

	if _, err := runtime.service(0x91, []uint32{256}); err == nil {
		t.Fatal("SetBackLight accepted a truncating mode")
	}
	if runtime.BacklightMode() != BacklightMMI || !runtime.BacklightEnabled() {
		t.Fatalf("invalid mode changed backlight = mode %d enabled %v",
			runtime.BacklightMode(), runtime.BacklightEnabled())
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	reset := newRuntime()
	defer reset.Close()
	if reset.BacklightMode() != BacklightMMI || !reset.BacklightEnabled() {
		t.Fatalf("reset default = mode %d enabled %v",
			reset.BacklightMode(), reset.BacklightEnabled())
	}
}
