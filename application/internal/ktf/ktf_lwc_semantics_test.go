package ktf

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestKTFLWCLayoutFrameAndTraversal(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	container := newHostObject(t, runtime, "org/kwis/msp/lwc/FormComponent")
	first := newHostObject(t, runtime, "org/kwis/msp/lwc/ButtonComponent")
	second := newHostObject(t, runtime, "org/kwis/msp/lwc/TextFieldComponent")

	runtime.configureLWC(runtime.lwcComponent(container), 0, 0, 80, 60)
	runtime.lwcComponent(first).preferredWidth = 20
	runtime.lwcComponent(first).preferredHeight = 10
	runtime.lwcComponent(second).preferredWidth = 30
	runtime.lwcComponent(second).preferredHeight = 12
	runtime.addLWCChild(container, 0, first)
	runtime.addLWCChild(container, 1, second)
	runtime.setLWCFocus(first, true)

	_, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/FormComponent",
		"useFrame",
		"(Z)V",
		[]uint32{0, container, 1},
	)
	check(t, err)
	_, err = runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/FormComponent",
		"setPacked",
		"(Z)V",
		[]uint32{0, container, 1},
	)
	check(t, err)
	_, err = runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/FormComponent",
		"validate",
		"()V",
		[]uint32{0, container},
	)
	check(t, err)
	if got := runtime.lwcComponent(first); got.x != 1 || got.y != 1 || got.width != 78 {
		t.Fatalf("framed first child = x:%d y:%d w:%d", got.x, got.y, got.width)
	}
	if got := runtime.lwcComponent(second); got.y != 11 || got.width != 78 {
		t.Fatalf("framed second child = y:%d w:%d", got.y, got.width)
	}

	next, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/FormComponent",
		"getNextTraversalComponent",
		"()Lorg/kwis/msp/lwc/Component;",
		[]uint32{0, container},
	)
	check(t, err)
	if next != second {
		t.Fatalf("next traversal = 0x%08x, want second 0x%08x", next, second)
	}
	previous, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/FormComponent",
		"getPrevTraversalComponent",
		"()Lorg/kwis/msp/lwc/Component;",
		[]uint32{0, container},
	)
	check(t, err)
	if previous != second {
		t.Fatalf("previous traversal = 0x%08x, want wrapped second 0x%08x", previous, second)
	}

	_, err = runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/ImageComponent",
		"setLayout",
		"(I)V",
		[]uint32{0, first, 1 | 2},
	)
	if err == nil || runtime.LastJavaThrowName != "java/lang/IllegalArgumentException" {
		t.Fatalf("conflicting layout error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
}

func TestKTFLWCPaintContentAndFrameClipToComponent(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	component := newHostObject(t, runtime, "org/kwis/msp/lwc/LabelComponent")
	graphics := newHostObject(t, runtime, "org/kwis/msp/lcdui/Graphics")
	target := image.NewRGBA(image.Rect(0, 0, 20, 20))
	draw.Draw(target, target.Bounds(), image.NewUniform(color.RGBA{R: 0xff, A: 0xff}), image.Point{}, draw.Src)
	runtime.Graphics[graphics] = &ktfGraphics{
		Target: target,
		clip:   target.Bounds(),
		color:  color.RGBA{A: 0xff},
	}
	state := runtime.lwcComponent(component)
	runtime.configureLWC(state, 3, 4, 8, 6)
	state.background = 0x0000ff
	state.backgroundSet = true
	state.foreground = 0x00ff00
	state.framed = true

	_, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/LabelComponent",
		"paint",
		"(Lorg/kwis/msp/lcdui/Graphics;)V",
		[]uint32{0, component, graphics},
	)
	check(t, err)
	if got := color.RGBAModel.Convert(target.At(4, 5)).(color.RGBA); got.B != 0xff {
		t.Fatalf("component interior = %#v, want blue background", got)
	}
	if got := color.RGBAModel.Convert(target.At(3, 4)).(color.RGBA); got.G != 0xff {
		t.Fatalf("component frame = %#v, want green", got)
	}
	if got := color.RGBAModel.Convert(target.At(2, 4)).(color.RGBA); got.R != 0xff || got.G != 0 || got.B != 0 {
		t.Fatalf("pixel outside component changed: %#v", got)
	}
	if !runtime.Graphics[graphics].PixelsDirty {
		t.Fatal("LWC paint did not mark Graphics pixels dirty")
	}
}

func TestKTFLWCGrabListenerRunsBeforeFocusedChild(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	shell := newHostObject(t, runtime, "org/kwis/msp/lwc/ShellComponent")
	field := newHostObject(t, runtime, "org/kwis/msp/lwc/TextFieldComponent")
	listener := newHostObject(t, runtime, "org/kwis/msp/lwc/GrabKeyListener")
	data := newHostObject(t, runtime, "java/lang/Object")
	runtime.addLWCChild(shell, 0, field)
	runtime.setLWCFocus(field, true)

	_, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/ShellComponent",
		"setGrabKeyListener",
		"(Lorg/kwis/msp/lwc/GrabKeyListener;Ljava/lang/Object;)V",
		[]uint32{0, shell, listener, data},
	)
	check(t, err)
	_, err = runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/ShellComponent",
		"grabKey",
		"(I)V",
		[]uint32{0, shell, uint32('2')},
	)
	check(t, err)
	handled, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/ShellComponent",
		"keyNotify",
		"(II)Z",
		[]uint32{0, shell, KeyPressed, uint32('2')},
	)
	check(t, err)
	if handled != 1 {
		t.Fatal("focused text field did not consume the grabbed key after listener fallback")
	}
	if runtime.LastUnimplementedJava !=
		"org/kwis/msp/lwc/GrabKeyListener.grabKeyNotify(IILjava/lang/Object;)Z" {
		t.Fatalf("grab listener was not called first: %q", runtime.LastUnimplementedJava)
	}

	_, err = runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/ShellComponent",
		"ungrabKey",
		"(I)V",
		[]uint32{0, shell, uint32('2')},
	)
	check(t, err)
	if runtime.lwcComponent(shell).grabbedKeys['2'] {
		t.Fatal("ungrabKey left key registered")
	}
}
