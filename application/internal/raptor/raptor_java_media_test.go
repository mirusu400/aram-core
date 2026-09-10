package raptor

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

// The embedded KTF Java host does not run through the KTF machine loop. Its
// clips therefore have to use the public Raptor mixer, which is advanced and
// published once per machine frame (issue #256).
func TestRaptorJavaAudioUsesPublicMediaMixer(t *testing.T) {
	public, runtime, java := newRaptorJavaMediaRuntime(t)
	startRaptorJavaTestClip(t, runtime, java, false)
	check(t, public.Services.Advance(public.ServiceOwner, 20*time.Millisecond))

	output := public.Services.Media.Drain()
	if len(output.PCM16) == 0 {
		t.Fatal("public frame advance produced no Raptor Java audio")
	}
	for _, sample := range output.PCM16 {
		if sample != 0 {
			return
		}
	}
	t.Fatal("public frame advance produced only silence for a non-silent Java clip")
}

func TestRaptorJavaAudioCompletionReturnsToAOTListener(t *testing.T) {
	public, runtime, java := newRaptorJavaMediaRuntime(t)
	fixture := startRaptorJavaTestClip(t, runtime, java, true)
	check(t, public.Services.Advance(public.ServiceOwner, 120*time.Millisecond))
	_ = public.Services.Media.Drain()

	var completion shared.Event
	for {
		event, ready := public.Services.Events.PopReady(public.Services.Clock.Monotonic())
		if !ready {
			t.Fatal("Raptor Java clip produced no completion event")
		}
		if event.Kind == shared.EventAudioComplete && event.ServiceID == fixture.serviceID {
			completion = event
			break
		}
	}
	callback, handled := runtime.JavaMediaCompletionCallback(completion.ServiceID)
	if !handled {
		t.Fatal("Raptor Java clip completion was not claimed by the embedded host")
	}
	if callback.Procedure != raptorJavaTestListenerBody ||
		callback.Args != [4]uint32{
			fixture.listener,
			fixture.clip,
			uint32(guest.WIPIMediaEnd),
			0,
		} {
		t.Fatalf("Raptor Java completion callback = %+v", callback)
	}
}

func TestDestroyRaptorJavaReleasesSharedMediaClips(t *testing.T) {
	public, runtime, java := newRaptorJavaMediaRuntime(t)
	startRaptorJavaTestClip(t, runtime, java, false)
	if got := len(public.Services.Media.Snapshot().Clips); got != 1 {
		t.Fatalf("shared clips before destroy = %d, want 1", got)
	}
	check(t, runtime.DestroyRaptorJava())
	if got := len(public.Services.Media.Snapshot().Clips); got != 0 {
		t.Fatalf("shared clips after destroy = %d, want 0", got)
	}
	check(t, public.Services.Advance(public.ServiceOwner, 20*time.Millisecond))
	if got := len(public.Services.Media.Drain().PCM16); got != 0 {
		t.Fatalf("destroyed Raptor Java clip still produced %d samples", got)
	}

	replacement, err := runtime.ensureJavaRuntime()
	check(t, err)
	startRaptorJavaTestClip(t, runtime, replacement, false)
	if got := len(public.Services.Media.Snapshot().Clips); got != 1 {
		t.Fatalf("shared clips after Java host replacement = %d, want 1", got)
	}
	check(t, runtime.DestroyRaptorJava())
}

type raptorJavaMediaFixture struct {
	clip      uint32
	listener  uint32
	serviceID shared.ServiceID
}

const raptorJavaTestListenerBody = uint32(0x00123451)

func newRaptorJavaMediaRuntime(
	t *testing.T,
) (*wipirt.Runtime, *Runtime, *JavaRuntime) {
	t.Helper()
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	if java.Host.Services.Media != public.Services.Media {
		t.Fatal("Raptor Java host has a mixer the public frame loop cannot advance")
	}
	return public, runtime, java
}

func startRaptorJavaTestClip(
	t *testing.T,
	runtime *Runtime,
	java *JavaRuntime,
	withListener bool,
) raptorJavaMediaFixture {
	t.Helper()
	clipClass, err := runtime.ensureRaptorHostClass(java, "org/kwis/msp/media/Clip")
	check(t, err)
	clip, err := runtime.NewRaptorJavaObject(clipClass.Holder)
	check(t, err)
	mediaType, err := runtime.NewRaptorJavaString("audio/wav")
	check(t, err)
	wave := raptorJavaTestWave()
	array, err := runtime.newRaptorJavaArray('B', uint32(len(wave)))
	check(t, err)
	body, err := runtime.Public.ReadU32(array + 8)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(body+4, wave))

	writeRaptorJavaTestArguments(t, runtime, clip, mediaType, array)
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className:  "org/kwis/msp/media/Clip",
		Name:       "<init>",
		descriptor: "(Ljava/lang/String;[B)V",
	})
	check(t, err)

	listener := uint32(0)
	if withListener {
		listenerClass, classErr := runtime.ensureRaptorHostClass(
			java,
			"org/kwis/msp/media/PlayListener",
		)
		check(t, classErr)
		listenerMethodFound := false
		for index := range listenerClass.methods {
			method := &listenerClass.methods[index]
			if method.Name == "playUpdate" &&
				method.descriptor == "(Lorg/kwis/msp/media/Clip;II)V" {
				method.Body = raptorJavaTestListenerBody
				listenerMethodFound = true
				break
			}
		}
		if !listenerMethodFound {
			listenerClass.methods = append(listenerClass.methods, raptorJavaDeclaredMethod{
				Name:       "playUpdate",
				descriptor: "(Lorg/kwis/msp/media/Clip;II)V",
				Body:       raptorJavaTestListenerBody,
			})
		}
		listener, err = runtime.NewRaptorJavaObject(listenerClass.Holder)
		check(t, err)
		writeRaptorJavaTestArguments(t, runtime, clip, listener)
		_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
			className:  "org/kwis/msp/media/Clip",
			Name:       "setListener",
			descriptor: "(Lorg/kwis/msp/media/PlayListener;)V",
		})
		check(t, err)
	}

	writeRaptorJavaTestArguments(t, runtime, clip, 0)
	result, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className:  "org/kwis/msp/media/Player",
		Name:       "play",
		descriptor: "(Lorg/kwis/msp/media/Clip;Z)Z",
		isStatic:   true,
	})
	check(t, err)
	if result.Low != 1 {
		t.Fatalf("Player.play result = %d, want 1", result.Low)
	}
	clips := runtime.Public.Services.Media.Snapshot().Clips
	if len(clips) != 1 {
		t.Fatalf("shared mixer clips = %d, want 1", len(clips))
	}
	return raptorJavaMediaFixture{
		clip:      clip,
		listener:  listener,
		serviceID: clips[0].ID,
	}
}

func writeRaptorJavaTestArguments(t *testing.T, runtime *Runtime, arguments ...uint32) {
	t.Helper()
	for index, argument := range arguments {
		if index >= 4 {
			t.Fatalf("test Java argument %d needs stack setup", index)
		}
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR0+uint32(index), argument))
	}
}

func raptorJavaTestWave() []byte {
	const sampleCount = 800
	data := make([]byte, 44+sampleCount*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8_000)
	binary.LittleEndian.PutUint32(data[28:32], 16_000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], sampleCount*2)
	for index := 0; index < sampleCount; index++ {
		binary.LittleEndian.PutUint16(data[44+index*2:], 400)
	}
	return data
}
