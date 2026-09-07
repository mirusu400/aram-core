package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

// TestSKVMAudioClipPlayBlocksItsWorkerThread covers 닥터K, which was silent:
// its audio worker is a Runnable that opens the clip, loops it, and closes it
// on the next line. com/skt/m/AudioClip.play and loop block the calling thread
// until the clip stops, so the close only runs once the sound is over. A
// play that returned straight away let the same run() destroy the clip in the
// instant it started, and the title produced no samples at all.
func TestSKVMAudioClipPlayBlocksItsWorkerThread(t *testing.T) {
	vm, clip, clipReference := newAudioWorkerVM(t)
	worker := vm.NewObject("Worker", nil)
	thread := vm.NewObject("java/lang/Thread", nil)
	invokeTestNative(
		t, vm,
		"java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V",
		thread, ReferenceValue(worker),
	)
	invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", thread)

	if clip.clip == 0 {
		t.Fatal("the worker closed its clip before the sound played")
	}
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	check(t, err)
	if info.State != shared.ClipPlaying {
		t.Fatalf("clip state = %v, want playing", info.State)
	}
	state, err := vm.thread(thread)
	check(t, err)
	if state.blockedClip != clip.clip {
		t.Fatalf("worker waits on clip %s, want %s", state.blockedClip, clip.clip)
	}

	// A session saved mid-song has to come back still waiting: the worker's
	// continuation and the clip it waits on are both part of the state.
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	// The restore rebuilds the heap, so every handle has to be taken again.
	clip, err = vm.audioClip(clipReference)
	check(t, err)
	state, err = vm.thread(thread)
	check(t, err)
	if state.blockedClip != clip.clip {
		t.Fatalf("restored worker waits on clip %s, want %s",
			state.blockedClip, clip.clip)
	}

	// Time passing on its own must not release a looping clip's waiter.
	check(t, vm.Advance(context.Background(), 5*time.Second, nil))
	if clip.clip == 0 {
		t.Fatal("the worker resumed while its clip was still looping")
	}

	// Stopping the clip is what the title's own code does to end the music,
	// and the waiter has to resume there so its close runs.
	invokeTestNative(t, vm, "com/skt/m/AudioClip", "stop", "()V", clipReference)
	if clip.clip != 0 {
		t.Fatalf("stopped clip = %s, want the worker to have closed it", clip.clip)
	}
	if state.active {
		t.Fatal("the worker thread is still running after its clip stopped")
	}
}

// TestSKVMAudioClipRestartOutlivesTheStoppedWorker covers the track change the
// same title makes: it stops the clip, then starts a fresh worker that reopens
// and loops the next song. The stopped worker's close has to land before the
// new one opens, otherwise it destroys the clip the new song is already
// playing and the music dies at the first track change.
func TestSKVMAudioClipRestartOutlivesTheStoppedWorker(t *testing.T) {
	vm, clip, clipReference := newAudioWorkerVM(t)
	worker := vm.NewObject("Worker", nil)
	for _, reference := range []uint32{
		vm.NewObject("java/lang/Thread", nil),
		vm.NewObject("java/lang/Thread", nil),
	} {
		invokeTestNative(
			t, vm,
			"java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V",
			reference, ReferenceValue(worker),
		)
		invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", reference)
		if clip.clip == 0 {
			t.Fatal("a worker closed its clip while the sound was playing")
		}
		invokeTestNative(t, vm, "com/skt/m/AudioClip", "stop", "()V", clipReference)
	}
	// The second worker reopened the clip after the first had been released,
	// so nothing was left parked on the service the new song used.
	check(t, vm.Advance(context.Background(), time.Second, nil))
	for reference, object := range vm.heap {
		state, ok := object.Native.(*threadState)
		if ok && (state.active || state.blockedClip != 0) {
			t.Fatalf("thread %d is still parked on clip %s", reference, state.blockedClip)
		}
	}
}

// TestSKVMAudioClipPlayStaysSynchronousOffThread keeps a play made from the
// MIDlet's own callback non-blocking: there is no worker to suspend there, and
// parking the interpreter itself would stop the title.
func TestSKVMAudioClipPlayStaysSynchronousOffThread(t *testing.T) {
	vm, clip, clipReference := newAudioWorkerVM(t)
	payload := pcmWaveScore()
	data := vm.NewByteArray(payload)
	invokeTestNative(
		t, vm,
		"com/skt/m/AudioClip", "open", "([BII)V",
		clipReference,
		ReferenceValue(data), IntValue(0), IntValue(int32(len(payload))),
	)
	invokeTestNative(t, vm, "com/skt/m/AudioClip", "play", "()V", clipReference)
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	check(t, err)
	if info.State != shared.ClipPlaying {
		t.Fatalf("clip state = %v, want playing", info.State)
	}
}

// newAudioWorkerVM builds a VM holding the Worker class below plus an
// AudioClip and the score bytes it opens, wired into Worker's static fields.
func newAudioWorkerVM(t *testing.T) (*VM, *audioClipState, uint32) {
	t.Helper()
	vm, err := New(map[string][]byte{"Worker": syntheticAudioWorkerClass(t)})
	check(t, err)
	name := vm.NewString("mmf")
	clipValue := invokeTestNative(
		t, vm,
		"com/skt/m/AudioSystem", "getAudioClip", "(Ljava/lang/String;)Lcom/skt/m/AudioClip;",
		0, ReferenceValue(name),
	)
	clipReference, err := clipValue.Reference()
	check(t, err)
	clip, err := vm.audioClip(clipReference)
	check(t, err)
	runtimeClass := vm.classes["Worker"]
	runtimeClass.static[fieldStorageKey("Worker", "clip", "Lcom/skt/m/AudioClip;")] =
		ReferenceValue(clipReference)
	runtimeClass.static[fieldStorageKey("Worker", "data", "[B")] =
		ReferenceValue(vm.NewByteArray(pcmWaveScore()))
	return vm, clip, clipReference
}

// pcmWaveScore is a quarter second of audible PCM, long enough that a clip
// only stops because something stopped it.
func pcmWaveScore() []byte {
	samples := make([]int16, 4_000)
	for index := range samples {
		if index%2 == 0 {
			samples[index] = 8_000
		} else {
			samples[index] = -8_000
		}
	}
	dataSize := len(samples) * 2
	result := make([]byte, 44+dataSize)
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(result)-8))
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], 1)
	binary.LittleEndian.PutUint32(result[24:28], 16_000)
	binary.LittleEndian.PutUint32(result[28:32], 32_000)
	binary.LittleEndian.PutUint16(result[32:34], 2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], uint32(dataSize))
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(result[44+index*2:], uint16(sample))
	}
	return result
}

// syntheticAudioWorkerClass mirrors the shape every SK-VM title uses for
// music: a Runnable whose run opens the clip, loops it, and closes it, all
// through invokeinterface on com/skt/m/AudioClip.
func syntheticAudioWorkerClass(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	u2 := func(value uint16) {
		t.Helper()
		check(t, binary.Write(&output, binary.BigEndian, value))
	}
	u4 := func(value uint32) {
		t.Helper()
		check(t, binary.Write(&output, binary.BigEndian, value))
	}
	utf := func(value string) {
		output.WriteByte(constantUTF8)
		u2(uint16(len(value)))
		output.WriteString(value)
	}
	class := func(name uint16) {
		output.WriteByte(constantClass)
		u2(name)
	}
	nameAndType := func(name, descriptor uint16) {
		output.WriteByte(constantNameAndType)
		u2(name)
		u2(descriptor)
	}
	member := func(tag byte, owner, descriptor uint16) {
		output.WriteByte(tag)
		u2(owner)
		u2(descriptor)
	}

	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(28)
	utf("Worker")                              // 1
	class(1)                                   // 2
	utf("java/lang/Object")                    // 3
	class(3)                                   // 4
	utf("run")                                 // 5
	utf("()V")                                 // 6
	utf("Code")                                // 7
	utf("clip")                                // 8
	utf("Lcom/skt/m/AudioClip;")               // 9
	nameAndType(8, 9)                          // 10
	member(constantFieldref, 2, 10)            // 11
	utf("data")                                // 12
	utf("[B")                                  // 13
	nameAndType(12, 13)                        // 14
	member(constantFieldref, 2, 14)            // 15
	utf("com/skt/m/AudioClip")                 // 16
	class(16)                                  // 17
	utf("open")                                // 18
	utf("([BII)V")                             // 19
	nameAndType(18, 19)                        // 20
	member(constantInterfaceMethodref, 17, 20) // 21
	utf("loop")                                // 22
	nameAndType(22, 6)                         // 23
	member(constantInterfaceMethodref, 17, 23) // 24
	utf("close")                               // 25
	nameAndType(25, 6)                         // 26
	member(constantInterfaceMethodref, 17, 26) // 27

	u2(AccessPublic)
	u2(2)
	u2(4)
	u2(0) // interfaces
	u2(2) // fields
	u2(AccessPublic | AccessStatic)
	u2(8)
	u2(9)
	u2(0)
	u2(AccessPublic | AccessStatic)
	u2(12)
	u2(13)
	u2(0)
	u2(1) // methods
	code := []byte{
		0xb2, 0, 11, // getstatic Worker.clip
		0xb2, 0, 15, // getstatic Worker.data
		0x03,        // iconst_0
		0xb2, 0, 15, // getstatic Worker.data
		0xbe,              // arraylength
		0xb9, 0, 21, 4, 0, // invokeinterface AudioClip.open([BII)V
		0xb2, 0, 11, // getstatic Worker.clip
		0xb9, 0, 24, 1, 0, // invokeinterface AudioClip.loop()V
		0xb2, 0, 11, // getstatic Worker.clip
		0xb9, 0, 27, 1, 0, // invokeinterface AudioClip.close()V
		0xb1, // return
	}
	u2(AccessPublic)
	u2(5)
	u2(6)
	u2(1)
	u2(7)
	u4(uint32(2 + 2 + 4 + len(code) + 2 + 2))
	u2(4) // max stack
	u2(1) // max locals
	u4(uint32(len(code)))
	output.Write(code)
	u2(0) // handlers
	u2(0) // code attributes
	u2(0) // class attributes
	return output.Bytes()
}
