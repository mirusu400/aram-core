package ktf

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

type receiverReadSpy struct {
	cpu.Backend
	receiver, class, descriptor uint32
	headerReads, metadataReads  int
}

func (b *receiverReadSpy) ReadMemory(address uint32, destination []byte) error {
	if address == b.receiver {
		b.headerReads++
	}
	if address == b.class || address == b.descriptor {
		b.metadataReads++
	}
	return b.Backend.ReadMemory(address, destination)
}

func TestKTFExactHostReceiverAvoidsClassParsing(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	graphics := newHostObject(t, runtime, "org/kwis/msp/lcdui/Graphics")
	runtime.Graphics[graphics] = &ktfGraphics{Target: image.NewRGBA(image.Rect(0, 0, 8, 8)), color: color.RGBA{A: 0xff}}
	class := runtime.JavaClasses["org/kwis/msp/lcdui/Graphics"]
	descriptor := readU32(t, runtime, class+8)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, graphics))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0x123456))
	spy := &receiverReadSpy{Backend: runtime.CPU, receiver: graphics, class: class, descriptor: descriptor}
	runtime.CPU = spy
	handler := HostJavaMethod("org/kwis/msp/lcdui/Graphics", "setColor", "(I)V")
	_, err := handler(context.Background(), runtime)
	check(t, err)
	if spy.headerReads != 1 || spy.metadataReads != 0 {
		t.Fatalf("exact receiver: header reads=%d metadata reads=%d", spy.headerReads, spy.metadataReads)
	}
	if got := runtime.Graphics[graphics].color; got != (color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Fatalf("setColor = %+v", got)
	}
	if runtime.inspectMemo.open {
		t.Fatal("resolution window remained open after the handler")
	}
	// Changing the receiver's class between calls must take the full path,
	// rather than reusing the previous exact-class decision/header.
	otherClass, err := runtime.EnsureJavaClass("java/lang/Object")
	check(t, err)
	check(t, runtime.WriteU32(graphics+4, otherClass))
	spy.headerReads, spy.metadataReads = 0, 0
	_, err = handler(context.Background(), runtime)
	check(t, err)
	if spy.headerReads != 1 || spy.metadataReads == 0 {
		t.Fatalf("changed receiver: header reads=%d metadata reads=%d", spy.headerReads, spy.metadataReads)
	}
}

func TestKTFReceiverHeaderMemoLivesOnlyInsideResolution(t *testing.T) {
	runtime := newTestRuntime(t)
	object := allocWords(t, runtime, 2)
	check(t, runtime.WriteU32(object+4, 0x1234))
	read := func(want uint32) {
		t.Helper()
		got, err := runtime.readHostJavaReceiverClass(object)
		check(t, err)
		if got != want {
			t.Fatalf("receiver class = %08x, want %08x", got, want)
		}
	}
	read(0x1234)
	check(t, runtime.WriteU32(object+4, 0x5678))
	read(0x5678)
	runtime.inspectMemo.begin()
	read(0x5678)
	check(t, runtime.WriteU32(object+4, 0x1234))
	read(0x5678)
	runtime.inspectMemo.reset()
	read(0x1234)
}
