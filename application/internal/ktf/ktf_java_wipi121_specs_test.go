package ktf

import (
	"context"
	"testing"
)

func TestKTFWIPI121HostSpecsDeclareFrameworkMethods(t *testing.T) {
	want := map[string][]string{
		"org/kwis/msp/lwc/Component": {
			"configure(IIIII)V",
			"getPreferredHeight(I)I",
			"getXOnScreen()I",
		},
		"org/kwis/msp/lwc/ContainerComponent": {
			"addComponent(ILorg/kwis/msp/lwc/Component;)V",
			"getNumberOfComponent()I",
			"processEvent(IIII)Z",
		},
		"org/kwis/msp/lwc/ShellComponent": {
			"<init>(IIIIZ)V",
			"setTitle(Ljava/lang/String;)V",
			"setGrabKeyListener(Lorg/kwis/msp/lwc/GrabKeyListener;Ljava/lang/Object;)V",
		},
		"org/kwis/msp/lwc/FormComponent": {
			"<init>(Z)V",
			"setFocus(Lorg/kwis/msp/lwc/Component;)V",
			"scrollTo(II)Z",
		},
		"org/kwis/msp/lwc/TextComponent": {
			"insert([CIII)V",
			"getConstraint()I",
			"showNotify(Z)V",
		},
		"org/kwis/msp/lwc/CheckboxComponent": {
			"<init>(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;Lorg/kwis/msp/lwc/CheckboxGroup;)V",
			"setChangeListener(Lorg/kwis/msp/lwc/ChangeListener;Ljava/lang/Object;)V",
		},
		"org/kwis/msp/lwc/CommandBarComponent": {
			"addCommand(Lorg/kwis/msp/lwc/Command;)I",
			"pointerNotify(III)Z",
		},
		"org/kwis/msp/lwc/ListComponent": {
			"<init>(I)V",
			"append(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;)I",
			"controlNumber(Z)V",
		},
		"org/kwis/msp/lwc/ProxyCard": {
			"<init>(Lorg/kwis/msp/lwc/ContainerComponent;IIIIZ)V",
			"paint(Lorg/kwis/msp/lcdui/Graphics;)V",
		},
		"org/kwis/msp/lwc/ScrollbarComponent": {
			"<init>(IIIIII)V",
			"getChangeAmount()I",
			"setViewAmount(I)V",
		},
		"org/kwis/msp/lwc/TextBoxComponent": {
			"<init>(Ljava/lang/String;II)V",
			"configure(IIIII)V",
		},
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
		"org/kwis/msf/io/Socket": {
			"isStream()Z",
			"send(Lorg/kwis/msf/io/Message;)V",
			"accept()Lorg/kwis/msf/io/Socket;",
		},
		"org/kwis/msf/io/HttpSocket": {
			"getHost()Ljava/lang/String;",
			"getRequestProperty(Ljava/lang/String;)Ljava/lang/String;",
			"setRequestProperty(Ljava/lang/String;Ljava/lang/String;)V",
		},
		"org/kwis/msp/io/File": {
			"<init>(Ljava/lang/String;II)V",
			"openDataInputStream()Ljava/io/DataInputStream;",
			"write([BII)I",
		},
		"org/kwis/msp/io/FileSystem": {
			"exists(Ljava/lang/String;I)Z",
			"list(Ljava/lang/String;I)Ljava/util/Vector;",
			"rename(Ljava/lang/String;Ljava/lang/String;I)V",
		},
		"org/kwis/msp/db/DataBase": {
			"deleteDataBase(Ljava/lang/String;I)V",
			"selectRecord(I[BI)V",
			"sortRecord(Lorg/kwis/msp/db/DataFilter;Lorg/kwis/msp/db/DataComparator;)[I",
		},
		"org/kwis/msp/lcdui/Card": {
			"<init>(Lorg/kwis/msp/lcdui/Display;IIIIZ)V",
			"getDisplay()Lorg/kwis/msp/lcdui/Display;",
			"pointerNotify(III)Z",
		},
		"org/kwis/msp/lcdui/Display": {
			"callSerially(Ljava/lang/Runnable;I)V",
			"setDockedCard(Lorg/kwis/msp/lcdui/Card;I)V",
			"where()V",
		},
		"org/kwis/msp/lcdui/Jlet": {
			"destroyApp(Z)V",
			"getEventQueue()Lorg/kwis/msp/lcdui/EventQueue;",
			"startApp([Ljava/lang/String;)V",
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

func TestKTFWIPI121DataBaseSelectRecordCopiesIntoBuffer(t *testing.T) {
	runtime := newTestRuntime(t)
	database := newHostObject(t, runtime, "org/kwis/msp/db/DataBase")
	runtime.databases[database] = &Database{
		Name:    "scores",
		Records: [][]byte{{1, 2, 3}},
	}
	buffer, err := runtime.newJavaByteArray([]byte{9, 9, 9, 9, 9})
	check(t, err)
	parameters := allocWords(t, runtime, 4)
	check(t, runtime.writeWords(parameters, []uint32{database, 0, buffer, 1}))
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })

	_, err = runtime.handleDataBaseMethod(
		context.Background(),
		"selectRecord",
		"(I[BI)V",
	)
	check(t, err)
	got, err := runtime.readJavaByteArray(buffer)
	check(t, err)
	want := []byte{9, 1, 2, 3, 9}
	if string(got) != string(want) {
		t.Fatalf("DataBase.selectRecord buffer = %v, want %v", got, want)
	}
}

func TestKTFWIPI121CardCoordinateConstructorState(t *testing.T) {
	runtime := newTestRuntime(t)
	card := newHostObject(t, runtime, "org/kwis/msp/lcdui/Card")
	check(t, runtime.configureCard(card, 0, 3, 4, 120, 160))
	for offset, want := range map[uint32]uint32{
		8: 3, 12: 4, 16: 120, 20: 160,
	} {
		got, err := runtime.readJavaFieldWord(card, offset)
		check(t, err)
		if got != want {
			t.Fatalf("Card field +%d = %d, want %d", offset, got, want)
		}
	}
}

func TestKTFWIPI121HttpSocketPreservesURLAndRequestState(t *testing.T) {
	runtime := newTestRuntime(t)
	rawURL := newJavaString(t, runtime, "https://example.com:8443/game?a=1#top")
	socket, err := runtime.newOfflineMSFSocket(rawURL)
	check(t, err)
	classAddress := readU32(t, runtime, socket+4)
	if class := inspectClass(t, runtime, classAddress); class.Name != "org/kwis/msf/io/HttpSocket" {
		t.Fatalf("URL.find socket class = %q", class.Name)
	}

	parameters := allocWords(t, runtime, 3)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{socket, 0, 0}))
	host, err := runtime.handleMSFSocketMethod("getHost", "()Ljava/lang/String;")
	check(t, err)
	if got := runtime.javaStringValue(host); got != "example.com" {
		t.Fatalf("HttpSocket.getHost = %q", got)
	}
	port, err := runtime.handleMSFSocketMethod("getPort", "()I")
	check(t, err)
	if port != 8443 {
		t.Fatalf("HttpSocket.getPort = %d", port)
	}

	key := newJavaString(t, runtime, "Accept")
	value := newJavaString(t, runtime, "application/octet-stream")
	check(t, runtime.writeWords(parameters, []uint32{socket, key, value}))
	_, err = runtime.handleMSFSocketMethod(
		"setRequestProperty",
		"(Ljava/lang/String;Ljava/lang/String;)V",
	)
	check(t, err)
	lowerKey := newJavaString(t, runtime, "accept")
	check(t, runtime.writeWords(parameters, []uint32{socket, lowerKey, 0}))
	got, err := runtime.handleMSFSocketMethod(
		"getRequestProperty",
		"(Ljava/lang/String;)Ljava/lang/String;",
	)
	check(t, err)
	if got != value {
		t.Fatalf("HttpSocket request property = 0x%08x, want 0x%08x", got, value)
	}
}

func TestKTFWIPI121LWCConstructorsInitializeState(t *testing.T) {
	runtime := newTestRuntime(t)
	label := newJavaString(t, runtime, "selected")
	image := newHostObject(t, runtime, "org/kwis/msp/lcdui/Image")
	group := newHostObject(t, runtime, "org/kwis/msp/lwc/CheckboxGroup")
	checkbox := newHostObject(t, runtime, "org/kwis/msp/lwc/CheckboxComponent")

	_, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/CheckboxComponent",
		"<init>",
		"(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;Lorg/kwis/msp/lwc/CheckboxGroup;)V",
		[]uint32{0, checkbox, label, image, group},
	)
	check(t, err)
	checkboxState := runtime.lwcComponent(checkbox)
	if checkboxState.text != label || checkboxState.image != image ||
		checkboxState.group != group {
		t.Fatalf("CheckboxComponent constructor state = %+v", checkboxState)
	}

	scrollbar := newHostObject(t, runtime, "org/kwis/msp/lwc/ScrollbarComponent")
	_, err = runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/ScrollbarComponent",
		"<init>",
		"(IIIIII)V",
		[]uint32{0, scrollbar, 2, 30, 10, 0, 100, 5},
	)
	check(t, err)
	scrollbarState := runtime.lwcComponent(scrollbar)
	if scrollbarState.mode != 2 || scrollbarState.progressValue != 30 ||
		scrollbarState.viewAmount != 10 || scrollbarState.minimum != 0 ||
		scrollbarState.progressMax != 100 || scrollbarState.changeAmount != 5 {
		t.Fatalf("ScrollbarComponent constructor state = %+v", scrollbarState)
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
