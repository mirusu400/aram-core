package ktf

import (
	"context"
	"testing"
)

func TestKTFWIPI121HostSpecsDeclareFrameworkMethods(t *testing.T) {
	want := map[string][]string{
		"org/kwis/msf/core/Kernel": {
			"execute(Ljava/lang/String;[Ljava/lang/String;)I",
			"getExecNames(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)[Ljava/lang/String;",
			"getPrgInfo()[I",
		},
		"org/kwis/msf/core/Shared": {
			"createBuf(Ljava/lang/String;I)[B",
			"resizeBuf([BI)[B",
			"destroyBuf([B)V",
		},
		"org/kwis/msf/io/Message": {
			"<init>(Ljava/lang/String;[BII)V",
			"getData()[B",
			"setDate(Ljava/util/Date;)V",
		},
		"org/kwis/msp/io/File": {
			"<init>(Ljava/lang/String;II)V",
			"openDataInputStream()Ljava/io/DataInputStream;",
			"write([BII)I",
		},
		"org/kwis/msp/lcdui/DisplayProxy": {
			"getBitsPerPixel()I",
			"hasRepeatEvents()Z",
			"flush(IIII)V",
		},
		"org/kwis/msp/lwc/CheckboxGroup": {
			"select(Lorg/kwis/msp/lwc/CheckboxComponent;)V",
			"setChangeListener(Lorg/kwis/msp/lwc/ChangeListener;Ljava/lang/Object;)V",
		},
		"org/kwis/msp/lwc/Command": {
			"<init>(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/Object;)V",
			"getActiveImage()Lorg/kwis/msp/lcdui/Image;",
		},
	}
	for className, signatures := range want {
		declared := make(map[string]bool)
		for _, method := range HostJavaClassSpecs[className].methods {
			declared[method.name+method.descriptor] = true
		}
		for _, signature := range signatures {
			if !declared[signature] {
				t.Errorf("%s.%s is absent from the host spec", className, signature)
			}
		}
	}
}

func TestKTFWIPI121ListenerSpecsAreAbstract(t *testing.T) {
	want := map[string]string{
		"org/kwis/msp/db/DataComparator":         "compare([B[B)I",
		"org/kwis/msp/db/DataFilter":             "filter([B)Z",
		"org/kwis/msp/lcdui/ImageObserver":       "notify(Lorg/kwis/msp/lcdui/Image;I)V",
		"org/kwis/msp/lcdui/SystemEventListener": "notifySystemEvent(IIII)V",
		"org/kwis/msp/lwc/ActionListener":        "action(Lorg/kwis/msp/lwc/Component;Ljava/lang/Object;)V",
		"org/kwis/msp/lwc/ChangeListener":        "changed(Lorg/kwis/msp/lwc/Component;Ljava/lang/Object;)V",
		"org/kwis/msp/lwc/CommandListener":       "commandAction(Lorg/kwis/msp/lwc/Command;ILjava/lang/Object;)V",
	}
	for className, signature := range want {
		found := false
		for _, method := range HostJavaClassSpecs[className].methods {
			if method.name+method.descriptor == signature &&
				method.access&0x0400 != 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s.%s is not declared abstract", className, signature)
		}
	}
}

func TestKTFMessageConstructorsInitializeDocumentedSlice(t *testing.T) {
	runtime := newTestRuntime(t)
	message := newHostObject(t, runtime, "org/kwis/msf/io/Message")
	address := newJavaString(t, runtime, "01012345678")
	data, err := runtime.newJavaByteArray([]byte{10, 20, 30, 40})
	check(t, err)
	parameters := allocWords(t, runtime, 5)
	check(t, runtime.writeWords(parameters, []uint32{
		message, address, data, 1, 2,
	}))
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })

	_, err = runtime.handleMSFMessageMethod(
		"<init>",
		"(Ljava/lang/String;[BII)V",
	)
	check(t, err)
	state := runtime.lwcComponent(message)
	if state.text != address || state.image != data ||
		state.changeAmount != 1 || state.viewAmount != 2 {
		t.Fatalf("Message constructor state = %+v", state)
	}
}

func TestKTFCheckboxGroupRejectsNullSelection(t *testing.T) {
	runtime := newTestRuntime(t)
	group := newHostObject(t, runtime, "org/kwis/msp/lwc/CheckboxGroup")
	_, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/CheckboxGroup",
		"select",
		"(Lorg/kwis/msp/lwc/CheckboxComponent;)V",
		[]uint32{0, group, 0},
	)
	if err == nil || runtime.LastJavaThrowName != "java/lang/NullPointerException" {
		t.Fatalf("CheckboxGroup.select(null) = %v, throw %q", err, runtime.LastJavaThrowName)
	}
}

func TestKTFCommandStringImageConstructorLoadsBothImages(t *testing.T) {
	runtime := newTestRuntime(t)
	command := newHostObject(t, runtime, "org/kwis/msp/lwc/Command")
	label := newJavaString(t, runtime, "Play")
	normal := newJavaString(t, runtime, "normal.png")
	active := newJavaString(t, runtime, "active.png")
	extension := newHostObject(t, runtime, "java/lang/Object")

	_, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/Command",
		"<init>",
		"(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/Object;)V",
		[]uint32{0, command, label, normal, active, extension},
	)
	check(t, err)
	state := runtime.lwcComponent(command)
	if state.text != label || state.image == 0 || state.imageActive == 0 ||
		runtime.lwcEventData[command] != extension {
		t.Fatalf("Command string-image constructor state = %+v", state)
	}
}
