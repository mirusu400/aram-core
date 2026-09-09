package ktf

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestKTFPlayerPauseAndResumePreservePosition(t *testing.T) {
	runtime := newTestRuntime(t)
	clip := newHostObject(t, runtime, "org/kwis/msp/media/Clip")
	state := runtime.ensureKTFClip(clip)
	state.data = ktfTestWave(make([]int16, 400))
	state.listener = 0x10002000
	check(t, runtime.syncKTFClip(clip))
	runtime.Tasks = make([]*Task, MaxTasks)
	for index := range runtime.Tasks {
		runtime.Tasks[index] = &Task{}
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	_, err := runtime.handleMediaMethod(
		"play",
		"(Lorg/kwis/msp/media/Clip;Z)Z",
	)
	check(t, err)
	if got := runtime.PendingJavaCalls[len(runtime.PendingJavaCalls)-1].args[1]; got != 2 {
		t.Fatalf("play listener event = %d, want START(2)", got)
	}
	check(t, runtime.Services.Advance(runtime.ServiceOwner, 25*time.Millisecond))

	serviceID := runtime.clipServices[clip]
	beforePause, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID)
	check(t, err)
	if beforePause.Position < 24*time.Millisecond || beforePause.Position > 25*time.Millisecond ||
		beforePause.State != shared.ClipPlaying {
		t.Fatalf("before pause = %+v", beforePause)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	_, err = runtime.handleMediaMethod(
		"pause",
		"(Lorg/kwis/msp/media/Clip;)Z",
	)
	check(t, err)
	if got := runtime.PendingJavaCalls[len(runtime.PendingJavaCalls)-1].args[1]; got != 4 {
		t.Fatalf("pause listener event = %d, want PAUSE(4)", got)
	}
	paused, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID)
	check(t, err)
	if paused.Position != beforePause.Position || paused.State != shared.ClipPaused {
		t.Fatalf("paused clip = %+v, before = %+v", paused, beforePause)
	}
	if !state.playing {
		t.Fatal("paused clip was marked as an idle recycling candidate")
	}

	check(t, runtime.Services.Advance(runtime.ServiceOwner, 15*time.Millisecond))
	stillPaused, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID)
	check(t, err)
	if stillPaused.Position != beforePause.Position ||
		stillPaused.State != shared.ClipPaused {
		t.Fatalf("paused clip advanced = %+v", stillPaused)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	_, err = runtime.handleMediaMethod(
		"resume",
		"(Lorg/kwis/msp/media/Clip;)Z",
	)
	check(t, err)
	if got := runtime.PendingJavaCalls[len(runtime.PendingJavaCalls)-1].args[1]; got != 5 {
		t.Fatalf("resume listener event = %d, want RESUME(5)", got)
	}
	resumed, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID)
	check(t, err)
	if resumed.Position != beforePause.Position || resumed.State != shared.ClipPlaying {
		t.Fatalf("resumed clip = %+v, before = %+v", resumed, beforePause)
	}

	check(t, runtime.Services.Advance(runtime.ServiceOwner, 10*time.Millisecond))
	advanced, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID)
	check(t, err)
	if advanced.Position < 34*time.Millisecond || advanced.Position > 35*time.Millisecond {
		t.Fatalf("resumed position = %s, want about 35ms", advanced.Position)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	result, err := runtime.handleMediaMethod("stop", "(Lorg/kwis/msp/media/Clip;)Z")
	check(t, err)
	if result != 1 || runtime.PendingJavaCalls[len(runtime.PendingJavaCalls)-1].args[1] != 3 {
		t.Fatalf("stop result/event = %d/%+v", result, runtime.PendingJavaCalls)
	}
}

func TestKTFGlobalVolumeSetUpdatesSharedMixer(t *testing.T) {
	runtime := newTestRuntime(t)
	call, ok := ktfJavaNativeOverride("org/kwis/msp/media/Volume.set(I)V")
	if !ok {
		t.Fatal("Volume.set native override is missing")
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, 3))
	if _, err := call.handler(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Services.Media.Snapshot().GlobalVolume; got != 60 {
		t.Fatalf("global volume = %d, want 60", got)
	}
}

func TestKTFClipSetPositionSeeksSharedClip(t *testing.T) {
	runtime := newTestRuntime(t)
	clip := newHostObject(t, runtime, "org/kwis/msp/media/Clip")
	state := runtime.ensureKTFClip(clip)
	state.data = ktfTestWave(make([]int16, 400))
	check(t, runtime.syncKTFClip(clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 10))
	result, err := runtime.handleMediaMethod("setPosition", "(I)Z")
	check(t, err)
	if result != 1 {
		t.Fatalf("setPosition result = %d", result)
	}
	info, err := runtime.Services.Media.Info(runtime.ServiceOwner, runtime.clipServices[clip])
	check(t, err)
	if info.Position != 10*time.Millisecond {
		t.Fatalf("setPosition shared position = %s", info.Position)
	}
}

func TestKTFMediaCompletionQueuesPlayListener(t *testing.T) {
	runtime := newTestRuntime(t)
	const (
		clip      = uint32(0x10001000)
		listener  = uint32(0x10002000)
		serviceID = shared.ServiceID(7)
		endOfData = uint32(1)
	)
	runtime.clips[clip] = &ktfClip{listener: listener, playing: true}
	runtime.clipServices[clip] = serviceID
	runtime.Tasks = make([]*Task, MaxTasks)
	for index := range runtime.Tasks {
		runtime.Tasks[index] = &Task{}
	}
	_, err := runtime.Services.Events.Enqueue(shared.Event{
		Kind:      shared.EventAudioComplete,
		Owner:     runtime.ServiceOwner,
		ServiceID: serviceID,
	})
	check(t, err)

	check(t, runtime.DrainServiceEvents(0))
	if runtime.clips[clip].playing {
		t.Fatal("completed clip is still marked playing")
	}
	if len(runtime.PendingJavaCalls) != 1 {
		t.Fatalf("pending Java calls = %d, want 1", len(runtime.PendingJavaCalls))
	}
	call := runtime.PendingJavaCalls[0]
	if call.instance != listener || call.name != "playUpdate" ||
		call.descriptor != "(Lorg/kwis/msp/media/Clip;II)V" ||
		len(call.args) != 3 || call.args[0] != clip ||
		call.args[1] != endOfData || call.args[2] != 0 {
		t.Fatalf("queued PlayListener call = %+v", call)
	}
	if runtime.Services.Events.Len() != 0 {
		t.Fatal("audio completion event was not consumed")
	}
}

func TestKTFPlayerHostSpecIncludesPauseAndResume(t *testing.T) {
	spec := HostJavaClassSpecs["org/kwis/msp/media/Player"]
	for _, name := range []string{"pause", "resume"} {
		found := false
		for _, method := range spec.methods {
			if method.name == name &&
				method.descriptor == "(Lorg/kwis/msp/media/Clip;)Z" &&
				method.access&0x0008 != 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Player.%s(Clip) is absent from the host spec", name)
		}
	}
}

func TestKTFPlayerNullClipReturnsFailure(t *testing.T) {
	runtime := newTestRuntime(t)
	for _, test := range []struct {
		name       string
		descriptor string
	}{
		{"play", "(Lorg/kwis/msp/media/Clip;Z)Z"},
		{"stop", "(Lorg/kwis/msp/media/Clip;)Z"},
		{"pause", "(Lorg/kwis/msp/media/Clip;)Z"},
		{"resume", "(Lorg/kwis/msp/media/Clip;)Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 0))
			result, err := runtime.handleMediaMethod(test.name, test.descriptor)
			check(t, err)
			if result != 0 {
				t.Fatalf("result = %d, want false", result)
			}
			if len(runtime.clips) != 0 || len(runtime.clipServices) != 0 {
				t.Fatalf("null call allocated clip state: clips=%v services=%v", runtime.clips, runtime.clipServices)
			}
		})
	}
}

func TestKTFClipFixedBufferLimitsPutData(t *testing.T) {
	runtime := newTestRuntime(t)
	clip := newHostObject(t, runtime, "org/kwis/msp/media/Clip")
	mediaType := newJavaString(t, runtime, "audio/mmf")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, mediaType))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 3))
	_, err := runtime.handleMediaMethod("<init>", "(Ljava/lang/String;I)V")
	check(t, err)

	data, err := runtime.newJavaByteArray([]byte{1, 2, 3, 4, 5})
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, data))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 0))
	stack := allocWords(t, runtime, 1)
	check(t, runtime.writeWords(stack, []uint32{5}))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	written, err := runtime.handleMediaMethod("putData", "([BII)I")
	check(t, err)
	if written != 3 || !bytes.Equal(runtime.clips[clip].data, []byte{1, 2, 3}) {
		t.Fatalf("bounded putData = %d/%v, want 3/[1 2 3]", written, runtime.clips[clip].data)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, data))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 2))
	result, err := runtime.handleMediaMethod("setBuffer", "([BI)Z")
	check(t, err)
	if result != 0 {
		t.Fatal("setBuffer replaced the constructor-created fixed buffer")
	}
}

func TestKTFClipSetBufferIsOneShotAndUsesArrayCapacity(t *testing.T) {
	runtime := newTestRuntime(t)
	clip := newHostObject(t, runtime, "org/kwis/msp/media/Clip")
	mediaType := newJavaString(t, runtime, "audio/mmf")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, mediaType))
	_, err := runtime.handleMediaMethod("<init>", "(Ljava/lang/String;)V")
	check(t, err)

	data, err := runtime.newJavaByteArray([]byte{9, 8, 7, 6})
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, data))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 2))
	result, err := runtime.handleMediaMethod("setBuffer", "([BI)Z")
	check(t, err)
	state := runtime.clips[clip]
	if result != 1 || state.capacity != 4 || !state.bufferSet ||
		!bytes.Equal(state.data, []byte{9, 8}) {
		t.Fatalf("setBuffer state = result %d, %+v", result, state)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	result, err = runtime.handleMediaMethod("setBuffer", "([BI)Z")
	check(t, err)
	if result != 0 || !bytes.Equal(state.data, []byte{9, 8}) {
		t.Fatalf("second setBuffer = %d/%v", result, state.data)
	}
}

func TestKTFClipPlayerIDControlAndMediaWriteUseSharedService(t *testing.T) {
	runtime := newTestRuntime(t)
	clip := newHostObject(t, runtime, "org/kwis/msp/media/Clip")
	runtime.clips[clip] = &ktfClip{volume: 100, data: []byte{4, 3, 2, 1}}
	mediaType := newJavaString(t, runtime, "audio/mmf")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, mediaType))
	playerID, err := runtime.handleMediaMethod("getPlayerID", "(Ljava/lang/String;)I")
	check(t, err)
	if playerID == ^uint32(0) {
		t.Fatal("getPlayerID returned failure")
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	written, err := runtime.handleMediaMethod("mediaWriteData", "()I")
	check(t, err)
	serviceData, err := runtime.Services.Media.Source(
		runtime.ServiceOwner, runtime.clipServices[clip],
	)
	check(t, err)
	if written != 4 || !bytes.Equal(serviceData, []byte{4, 3, 2, 1}) {
		t.Fatalf("mediaWriteData = %d/%v", written, serviceData)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, playerID))
	controlled, err := runtime.handleMediaMethod(
		"control", "(IILjava/lang/Object;Ljava/lang/Object;)I",
	)
	check(t, err)
	if controlled != 0 {
		t.Fatalf("control known player = %d", controlled)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, playerID+1))
	controlled, err = runtime.handleMediaMethod(
		"control", "(IILjava/lang/Object;Ljava/lang/Object;)I",
	)
	check(t, err)
	if controlled != ^uint32(0) {
		t.Fatalf("control unknown player = %d", controlled)
	}
}

func TestKTFClipRejectsOutOfRangeVolume(t *testing.T) {
	runtime := newTestRuntime(t)
	clip := newHostObject(t, runtime, "org/kwis/msp/media/Clip")
	runtime.clips[clip] = &ktfClip{volume: 50}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clip))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, ^uint32(0)))
	result, err := runtime.handleMediaMethod("setVolume", "(I)Z")
	check(t, err)
	if result != 0 || runtime.clips[clip].volume != 50 {
		t.Fatalf("setVolume(-1) = %d, volume %d", result, runtime.clips[clip].volume)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 101))
	result, err = runtime.handleMediaMethod("setVolume", "(I)Z")
	check(t, err)
	if result != 0 || runtime.clips[clip].volume != 50 {
		t.Fatalf("setVolume(101) = %d, volume %d", result, runtime.clips[clip].volume)
	}
}

func TestKTFPlayListenerHostSpecDeclaresCallback(t *testing.T) {
	spec := HostJavaClassSpecs["org/kwis/msp/media/PlayListener"]
	for _, method := range spec.methods {
		if method.name == "playUpdate" &&
			method.descriptor == "(Lorg/kwis/msp/media/Clip;II)V" &&
			method.access&0x0400 != 0 {
			return
		}
	}
	t.Fatal("PlayListener.playUpdate(Clip,int,int) is absent from the host spec")
}
