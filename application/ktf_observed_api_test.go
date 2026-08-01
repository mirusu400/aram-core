package application

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

func newKTFObservedAPIRuntime(t *testing.T) *ktfRuntime {
	t.Helper()
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.cpu.Close() })
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func prepareKTFMismatchedMethod(
	t *testing.T,
	runtime *ktfRuntime,
	className, name, descriptor string,
) {
	t.Helper()
	class, err := runtime.ensureJavaClass(className)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.resolveJavaMethod(class, name, descriptor); err != nil {
		t.Fatal(err)
	}
}

func writeKTFObservedRegister(
	t *testing.T,
	runtime *ktfRuntime,
	register, value uint32,
) {
	t.Helper()
	if err := runtime.cpu.WriteRegister(register, value); err != nil {
		t.Fatal(err)
	}
}

func requireKTFReceiverCorrection(
	t *testing.T,
	runtime *ktfRuntime,
	declared, actual, method string,
) {
	t.Helper()
	want := "java_host_receiver_correct:" + declared + "." + method + "->" + actual
	for _, entry := range runtime.hostTrace {
		if strings.HasPrefix(entry, want) {
			return
		}
	}
	t.Fatalf("receiver correction %q missing from trace %v", want, runtime.hostTrace)
}

func TestKTFHostJavaMethodCorrectsObservedAOTReceiverAliases(t *testing.T) {
	t.Run("DataOutputStream", func(t *testing.T) {
		runtime := newKTFObservedAPIRuntime(t)
		const (
			declared   = "[LImage;"
			actual     = "java/io/DataOutputStream"
			method     = "writeShort(I)V"
			methodName = "writeShort"
			descriptor = "(I)V"
		)
		prepareKTFMismatchedMethod(
			t,
			runtime,
			declared,
			methodName,
			descriptor,
		)
		stream, err := runtime.newHostJavaObject(actual)
		if err != nil {
			t.Fatal(err)
		}
		runtime.outputStreams[stream] = nil
		writeKTFObservedRegister(t, runtime, cpu.RegisterR1, stream)
		writeKTFObservedRegister(t, runtime, cpu.RegisterR2, 0x1234)
		if _, err := ktfHostJavaMethod(
			declared,
			methodName,
			descriptor,
		)(context.Background(), runtime); err != nil {
			t.Fatal(err)
		}
		if got := runtime.outputStreams[stream]; !bytes.Equal(got, []byte{0x12, 0x34}) {
			t.Fatalf("DataOutputStream bytes = %x", got)
		}
		requireKTFReceiverCorrection(t, runtime, declared, actual, method)
	})

	t.Run("DataInputStream", func(t *testing.T) {
		runtime := newKTFObservedAPIRuntime(t)
		const (
			declared   = "org/kwis/msp/io/File"
			actual     = "java/io/DataInputStream"
			method     = "readInt()I"
			methodName = "readInt"
			descriptor = "()I"
		)
		prepareKTFMismatchedMethod(
			t,
			runtime,
			declared,
			methodName,
			descriptor,
		)
		stream, err := runtime.newHostJavaObject(actual)
		if err != nil {
			t.Fatal(err)
		}
		runtime.inputStreams[stream] = &ktfInputStream{
			data: []byte{0x01, 0x02, 0x03, 0x04},
		}
		writeKTFObservedRegister(t, runtime, cpu.RegisterR1, stream)
		value, err := ktfHostJavaMethod(
			declared,
			methodName,
			descriptor,
		)(context.Background(), runtime)
		if err != nil {
			t.Fatal(err)
		}
		if value != 0x01020304 {
			t.Fatalf("DataInputStream.readInt = 0x%08x", value)
		}
		requireKTFReceiverCorrection(t, runtime, declared, actual, method)
	})

	t.Run("Enumeration", func(t *testing.T) {
		runtime := newKTFObservedAPIRuntime(t)
		const (
			declared   = "org/kwis/msp/io/File"
			actual     = "java/util/Enumeration"
			method     = "hasMoreElements()Z"
			methodName = "hasMoreElements"
			descriptor = "()Z"
		)
		prepareKTFMismatchedMethod(
			t,
			runtime,
			declared,
			methodName,
			descriptor,
		)
		enumeration, err := runtime.newJavaEnumeration([]uint32{0x1234})
		if err != nil {
			t.Fatal(err)
		}
		writeKTFObservedRegister(t, runtime, cpu.RegisterR1, enumeration)
		value, err := ktfHostJavaMethod(
			declared,
			methodName,
			descriptor,
		)(context.Background(), runtime)
		if err != nil {
			t.Fatal(err)
		}
		if value != 1 {
			t.Fatalf("Enumeration.hasMoreElements = %d", value)
		}
		requireKTFReceiverCorrection(t, runtime, declared, actual, method)
	})
}

func TestKTFHostJavaMethodReadsSpilledArgumentsFromStack(t *testing.T) {
	runtime := newKTFObservedAPIRuntime(t)
	dialog, err := runtime.newHostJavaObject("org/kwis/msp/lwc/DialogComponent")
	if err != nil {
		t.Fatal(err)
	}
	work, err := runtime.newHostJavaObject("org/kwis/msp/lwc/Component")
	if err != nil {
		t.Fatal(err)
	}
	const stack = DefaultStackBase + 0x400
	writeKTFObservedRegister(t, runtime, cpu.RegisterR1, dialog)
	writeKTFObservedRegister(t, runtime, cpu.RegisterR2, work)
	writeKTFObservedRegister(t, runtime, cpu.RegisterR3, 0)
	writeKTFObservedRegister(t, runtime, cpu.RegisterR4, 0xfeedface)
	writeKTFObservedRegister(t, runtime, cpu.RegisterSP, stack)
	if err := runtime.writeU32(stack, uint32(ktfDialogTypeOKCancel)); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lwc/DialogComponent",
		"<init>",
		"(Lorg/kwis/msp/lwc/Component;Ljava/lang/String;I)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	state := runtime.lwcComponents[dialog]
	if state == nil || state.work != work || state.dialogType != ktfDialogTypeOKCancel {
		t.Fatalf("dialog state = %#v", state)
	}
}

func TestKTFObservedFileSystemNamespacesAndFileCursor(t *testing.T) {
	runtime := newKTFObservedAPIRuntime(t)
	directory, err := runtime.newJavaString("save")
	if err != nil {
		t.Fatal(err)
	}
	writeKTFObservedRegister(t, runtime, cpu.RegisterR1, directory)
	writeKTFObservedRegister(t, runtime, cpu.RegisterR2, 2)
	if _, err := runtime.handleFileSystemMethod(
		"mkdir",
		"(Ljava/lang/String;I)V",
	); err != nil {
		t.Fatal(err)
	}
	sharedDirectory, err := runtime.handleFileSystemMethod(
		"isDirectory",
		"(Ljava/lang/String;I)Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	privateDirectory, err := runtime.handleFileSystemMethod(
		"isDirectory",
		"(Ljava/lang/String;)Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if sharedDirectory != 1 || privateDirectory != 0 {
		t.Fatalf(
			"directory visibility shared=%d private=%d",
			sharedDirectory,
			privateDirectory,
		)
	}
	root, err := runtime.newJavaString("/")
	if err != nil {
		t.Fatal(err)
	}
	writeKTFObservedRegister(t, runtime, cpu.RegisterR1, root)
	vector, err := runtime.handleFileSystemMethod(
		"list",
		"(Ljava/lang/String;I)Ljava/util/Vector;",
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, value := range runtime.vectors[vector] {
		if runtime.javaStringValue(value) == "save" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("shared root entries = %#v", runtime.vectors[vector])
	}

	if err := runtime.services.Storage.WriteFile(
		shared.NamespaceShared,
		"/save/data.bin",
		[]byte{0xab},
	); err != nil {
		t.Fatal(err)
	}
	file, err := runtime.newHostJavaObject("org/kwis/msp/io/File")
	if err != nil {
		t.Fatal(err)
	}
	filename, err := runtime.newJavaString("/save/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	const stack = DefaultStackBase + 0x500
	writeKTFObservedRegister(t, runtime, cpu.RegisterR1, file)
	writeKTFObservedRegister(t, runtime, cpu.RegisterR2, filename)
	writeKTFObservedRegister(t, runtime, cpu.RegisterR3, ktfFileReadOnly)
	writeKTFObservedRegister(t, runtime, cpu.RegisterSP, stack)
	if err := runtime.writeU32(stack, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleFileMethod(
		"<init>",
		"(Ljava/lang/String;II)V",
	); err != nil {
		t.Fatal(err)
	}
	if opened := runtime.files[file]; opened == nil ||
		opened.namespace != shared.NamespaceShared {
		t.Fatalf("shared file = %#v", opened)
	}
	value, err := runtime.handleFileMethod("read", "()I")
	if err != nil {
		t.Fatal(err)
	}
	position, err := runtime.handleFileMethod("tell", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if value != 0xab || position != 1 {
		t.Fatalf("File.read/tell = 0x%02x/%d", value, position)
	}
}

func TestKTFObservedLWCProgressDialogAndAnnunciator(t *testing.T) {
	runtime := newKTFObservedAPIRuntime(t)
	call := func(
		className, name, descriptor string,
		instance uint32,
		args ...uint32,
	) uint32 {
		t.Helper()
		registers := make([]uint32, cpu.RegisterR12+1)
		registers[1] = instance
		copy(registers[2:], args)
		value, err := runtime.handleLWCMethod(
			context.Background(),
			className,
			name,
			descriptor,
			registers,
		)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	const progress = uint32(0x10002000)
	call(
		"org/kwis/msp/lwc/ProgressComponent",
		"<init>",
		"(ZI)V",
		progress,
		1,
		10,
	)
	call(
		"org/kwis/msp/lwc/ProgressComponent",
		"setStep",
		"(I)V",
		progress,
		3,
	)
	value := call(
		"org/kwis/msp/lwc/ProgressComponent",
		"setValue",
		"(I)I",
		progress,
		8,
	)
	call(
		"org/kwis/msp/lwc/ProgressComponent",
		"setMargin",
		"(II)V",
		progress,
		^uint32(0),
		4,
	)
	progressState := runtime.lwcComponents[progress]
	if value != 6 || progressState == nil ||
		progressState.progressValue != 6 ||
		progressState.progressTop != 0 ||
		progressState.progressBottom != 4 {
		t.Fatalf("progress state = %#v, value=%d", progressState, value)
	}

	const dialog = uint32(0x10003000)
	call(
		"org/kwis/msp/lwc/DialogComponent",
		"<init>",
		"(I)V",
		dialog,
		uint32(ktfDialogTypeOK),
	)
	if action := call(
		"org/kwis/msp/lwc/DialogComponent",
		"doModal",
		"()I",
		dialog,
	); action != uint32(ktfDialogOK) {
		t.Fatalf("Dialog.doModal = %d", action)
	}

	const (
		annunciator = uint32(0x10004000)
		card        = uint32(0x10005000)
	)
	runtime.lwcComponent(annunciator).card = card
	runtime.dirtyCards[card] = false
	call(
		"org/kwis/msp/lwc/AnnunciatorComponent",
		"performed",
		"(LXTimer;)V",
		annunciator,
		0,
	)
	if runtime.dirtyCards[card] {
		t.Fatal("Annunciator.performed unexpectedly scheduled a repaint")
	}
}

func TestKTFObservedStateRoundTrip(t *testing.T) {
	lwc := &ktfLWCComponent{
		progressValue:  6,
		progressMax:    10,
		progressStep:   3,
		progressTop:    2,
		progressBottom: 4,
		dialogType:     ktfDialogTypeOKCancel,
		dialogTimeout:  123,
		dialogAction:   ktfDialogCancel,
		dialogOK:       0x1000,
		dialogCancel:   0x2000,
		annunciator:    true,
		transparent:    true,
		progressInput:  true,
	}
	if restored := restoreKTFLWC(snapshotKTFLWC(lwc)); !reflect.DeepEqual(restored, lwc) {
		t.Fatalf("LWC state round trip = %#v, want %#v", restored, lwc)
	}
	files := map[uint32]*ktfFile{
		1: {
			namespace: shared.NamespaceShared,
			name:      "/save.dat",
			position:  7,
			mode:      ktfFileReadWrite,
		},
	}
	if restored := restoreKTFFiles(snapshotKTFFiles(files)); !reflect.DeepEqual(restored, files) {
		t.Fatalf("file state round trip = %#v, want %#v", restored, files)
	}
	legacy := restoreKTFFiles(map[uint32]ktfFileSnapshot{
		2: {Name: "/legacy.dat"},
	})
	if legacy[2] == nil || legacy[2].namespace != shared.NamespacePrivate {
		t.Fatalf("legacy file namespace = %#v", legacy[2])
	}
}

func TestRaptorObservedImports(t *testing.T) {
	if name, ok := raptorWIPIImportName(126); !ok || name != "MC_knlGetSystemProperty" {
		t.Fatalf("Raptor import 126 = %q, %t", name, ok)
	}
	public := newPublicRuntime(t)
	runtime := &raptorRuntime{cpu: public.cpu, public: public}
	for ordinal, want := range map[uint32]string{
		1233: "RAPTOR.privateStartup1233",
		1400: "RAPTOR.privateRuntimeInit1400",
	} {
		result, name, handled, err := runtime.dispatchPrivateImport(ordinal)
		if err != nil || !handled || name != want || result != (wipiReturn{}) {
			t.Errorf(
				"Raptor import %d = %#v, %q, handled=%t, err=%v",
				ordinal,
				result,
				name,
				handled,
				err,
			)
		}
	}
}
