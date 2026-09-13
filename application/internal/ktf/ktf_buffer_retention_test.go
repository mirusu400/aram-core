package ktf

import (
	"bytes"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
	"testing"
	"time"
)

func call262(t *testing.T, r *Runtime, name, desc string, args ...uint32) uint32 {
	t.Helper()
	if len(args) > 0 {
		check(t, r.CPU.WriteRegister(cpu.RegisterR1, args[0]))
	}
	if len(args) > 1 {
		check(t, r.CPU.WriteRegister(cpu.RegisterR2, args[1]))
	}
	if len(args) > 2 {
		check(t, r.CPU.WriteRegister(cpu.RegisterR3, args[2]))
	}
	if len(args) > 3 {
		stack := allocWords(t, r, 1)
		check(t, r.writeWords(stack, args[3:]))
		check(t, r.CPU.WriteRegister(cpu.RegisterSP, stack))
	}
	v, e := r.handleMediaMethod(name, desc)
	check(t, e)
	return v
}
func clip262(t *testing.T, r *Runtime, data []byte) (uint32, uint32) {
	t.Helper()
	c := newHostObject(t, r, "org/kwis/msp/media/Clip")
	a, e := r.newJavaByteArray(data)
	check(t, e)
	call262(t, r, "<init>", "(Ljava/lang/String;[B)V", c, newJavaString(t, r, "audio/wav"), a)
	return c, a
}
func TestKTF262RetainedConstructorBufferPlay(t *testing.T) {
	r := newTestRuntime(t)
	wave := ktfTestWave([]int16{1000, -1000, 2000, -2000})
	c, a := clip262(t, r, make([]byte, len(wave)))
	check(t, r.writeJavaByteArrayRange(a, 0, wave))
	if got := call262(t, r, "putData", "([BII)I", c, a, 0, uint32(len(wave))); got != 0 {
		t.Fatalf("full-buffer put=%d", got)
	}
	if got := call262(t, r, "play", "(Lorg/kwis/msp/media/Clip;Z)Z", c, 0); got != 1 {
		t.Fatalf("late-populated constructor buffer play=%d", got)
	}
	source, e := r.Services.Media.Source(r.ServiceOwner, r.clipServices[c])
	check(t, e)
	if !bytes.Equal(source, wave) {
		t.Fatal("play did not use retained buffer")
	}
}
func TestKTF262RetainedBufferGetClear(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	out, e := r.newJavaByteArray(make([]byte, 4))
	check(t, e)
	check(t, r.writeJavaByteArrayRange(a, 0, []byte{9, 8, 7, 6}))
	if got := call262(t, r, "getData", "([BII)I", c, out, 0, 2); got != 2 {
		t.Fatalf("get=%d", got)
	}
	data, e := r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(data[:2], []byte{9, 8}) {
		t.Fatalf("retained get=%v", data)
	}
	check(t, r.writeJavaByteArrayRange(a, 2, []byte{5, 4}))
	call262(t, r, "getData", "([BII)I", c, out, 0, 2)
	data, e = r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(data[:2], []byte{5, 4}) {
		t.Fatalf("consumed window get=%v", data)
	}
	call262(t, r, "clearData", "()V", c)
	check(t, r.writeJavaByteArrayRange(a, 0, []byte{3, 3, 3, 3}))
	if got := call262(t, r, "availableDataSize", "()I", c); got != 0 {
		t.Fatalf("clear resurrected data=%d", got)
	}
	if got := call262(t, r, "getData", "([BII)I", c, out, 0, 4); got != 0 {
		t.Fatalf("clear get=%d", got)
	}
}
func TestKTF262RetainedSetBufferPutMirrorsBacking(t *testing.T) {
	r := newTestRuntime(t)
	c := newHostObject(t, r, "org/kwis/msp/media/Clip")
	call262(t, r, "<init>", "(Ljava/lang/String;)V", c, newJavaString(t, r, "audio/wav"))
	a, e := r.newJavaByteArray([]byte{1, 2, 0, 0})
	check(t, e)
	call262(t, r, "setBuffer", "([BI)Z", c, a, 2)
	in, e := r.newJavaByteArray([]byte{3, 4, 5})
	check(t, e)
	if got := call262(t, r, "putData", "([BII)I", c, in, 0, 3); got != 2 {
		t.Fatalf("bounded put=%d", got)
	}
	data, e := r.readJavaByteArray(a)
	check(t, e)
	if !bytes.Equal(data, []byte{1, 2, 3, 4}) {
		t.Fatalf("backing after put=%v", data)
	}
}

func TestKTF262RetainedRingWrap(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	out, e := r.newJavaByteArray(make([]byte, 8))
	check(t, e)
	call262(t, r, "getData", "([BII)I", c, out, 0, 3)
	in, e := r.newJavaByteArray([]byte{5, 6, 7, 8})
	check(t, e)
	if n := call262(t, r, "putData", "([BII)I", c, in, 0, 4); n != 3 {
		t.Fatalf("wrap put=%d", n)
	}
	b, e := r.readJavaByteArray(a)
	check(t, e)
	if !bytes.Equal(b, []byte{5, 6, 7, 4}) {
		t.Fatalf("ring backing=%v", b)
	}
	call262(t, r, "getData", "([BII)I", c, out, 0, 4)
	b, e = r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(b[:4], []byte{4, 5, 6, 7}) {
		t.Fatalf("FIFO=%v", b)
	}
	call262(t, r, "clearData", "()V", c)
	check(t, r.writeJavaByteArrayRange(a, 0, []byte{9, 9, 9, 9}))
	if n := call262(t, r, "play", "(Lorg/kwis/msp/media/Clip;Z)Z", c, 0); n != 0 {
		t.Fatal("clear revived source")
	}
	if n := call262(t, r, "putData", "([BII)I", c, in, 0, 4); n != 4 {
		t.Fatalf("put after clear=%d", n)
	}
	call262(t, r, "getData", "([BII)I", c, out, 0, 4)
	b, e = r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(b[:4], []byte{5, 6, 7, 8}) {
		t.Fatalf("clear/refill=%v", b)
	}
}
func TestKTF262RetainedPlayingPausedStoppedWatermark(t *testing.T) {
	r := newTestRuntime(t)
	wave := ktfTestWave(make([]int16, 800))
	c, a := clip262(t, r, wave)
	if n := call262(t, r, "play", "(Lorg/kwis/msp/media/Clip;Z)Z", c, 0); n != 1 {
		t.Fatal("play failed")
	}
	check(t, r.Services.Advance(r.ServiceOwner, 25*time.Millisecond))
	id := r.clipServices[c]
	before, e := r.Services.Media.Info(r.ServiceOwner, id)
	check(t, e)
	changed := append([]byte(nil), wave...)
	changed[len(changed)-1] = 5
	check(t, r.writeJavaByteArrayRange(a, 0, changed))
	for _, state := range []shared.ClipPlaybackState{shared.ClipPlaying, shared.ClipPaused} {
		if state == shared.ClipPaused {
			call262(t, r, "pause", "(Lorg/kwis/msp/media/Clip;)Z", c)
		}
		rev := r.Services.Media.OutputRevision()
		call262(t, r, "availableDataSize", "()I", c)
		if n := call262(t, r, "play", "(Lorg/kwis/msp/media/Clip;Z)Z", c, 0); n != 0 {
			t.Fatal("active play succeeded")
		}
		info, e := r.Services.Media.Info(r.ServiceOwner, id)
		check(t, e)
		if info.State != state || info.Position != before.Position || rev != r.Services.Media.OutputRevision() {
			t.Fatalf("active query changed state=%+v", info)
		}
	}
	call262(t, r, "stop", "(Lorg/kwis/msp/media/Clip;)Z", c)
	available, e := r.Services.Media.AvailableBytes(r.ServiceOwner, id)
	check(t, e)
	if n := call262(t, r, "availableDataSize", "()I", c); uint64(n) != available {
		t.Fatalf("stopped watermark=%d want%d", n, available)
	}
	out, e := r.newJavaByteArray(make([]byte, len(wave)))
	check(t, e)
	n := call262(t, r, "getData", "([BII)I", c, out, 0, uint32(len(wave)))
	got, e := r.readJavaByteArray(out)
	check(t, e)
	if uint64(n) != available || !bytes.Equal(got[:n], changed[len(changed)-int(available):]) {
		t.Fatalf("stopped get=%d want%d", n, available)
	}
}

func TestKTF262RetainedZeroCapacityAndRecycling(t *testing.T) {
	r := newTestRuntime(t)
	empty, a := clip262(t, r, nil)
	_ = a
	in, e := r.newJavaByteArray([]byte{1})
	check(t, e)
	if n := call262(t, r, "putData", "([BII)I", empty, in, 0, 1); n != 0 {
		t.Fatalf("zero-capacity put=%d", n)
	}
	wave := ktfTestWave([]int16{1000, -1000})
	c, buffer := clip262(t, r, make([]byte, len(wave)))
	// Retire the oldest empty service, then this clip's service.
	if !r.recycleKTFClipService() || !r.recycleKTFClipService() {
		t.Fatal("recycle failed")
	}
	check(t, r.writeJavaByteArrayRange(buffer, 0, wave))
	if n := call262(t, r, "play", "(Lorg/kwis/msp/media/Clip;Z)Z", c, 0); n != 1 {
		t.Fatalf("recycled alias play=%d", n)
	}
	call262(t, r, "stop", "(Lorg/kwis/msp/media/Clip;)Z", c)
}
func TestKTF262RetainedGetBoundsDoesNotConsume(t *testing.T) {
	r := newTestRuntime(t)
	c, _ := clip262(t, r, []byte{1, 2, 3, 4})
	out, e := r.newJavaByteArray(make([]byte, 1))
	check(t, e)
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, c))
	check(t, r.CPU.WriteRegister(cpu.RegisterR2, out))
	check(t, r.CPU.WriteRegister(cpu.RegisterR3, 0))
	stack := allocWords(t, r, 1)
	check(t, r.writeWords(stack, []uint32{3}))
	check(t, r.CPU.WriteRegister(cpu.RegisterSP, stack))
	if _, e := r.handleMediaMethod("getData", "([BII)I"); e == nil {
		t.Fatal("invalid destination accepted")
	}
	if n := call262(t, r, "availableDataSize", "()I", c); n != 4 {
		t.Fatalf("failed get consumed bytes=%d", n)
	}
}
func TestKTF262RetainedOverlap(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	// Destination overlaps bytes still logically buffered. Copy must snapshot
	// the requested prefix before mutating the backing array.
	if n := call262(t, r, "getData", "([BII)I", c, a, 2, 2); n != 2 {
		t.Fatalf("overlap get=%d", n)
	}
	out, e := r.newJavaByteArray(make([]byte, 4))
	check(t, e)
	call262(t, r, "getData", "([BII)I", c, out, 0, 2)
	got, e := r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(got[:2], []byte{1, 2}) {
		t.Fatalf("aliased remaining bytes=%v", got)
	}
	call262(t, r, "clearData", "()V", c)
	if n := call262(t, r, "putData", "([BII)I", c, a, 1, 3); n != 3 {
		t.Fatalf("overlap put=%d", n)
	}
	call262(t, r, "getData", "([BII)I", c, out, 0, 3)
	got, e = r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(got[:3], []byte{2, 1, 2}) {
		t.Fatalf("snapshot source put=%v", got)
	}
}
