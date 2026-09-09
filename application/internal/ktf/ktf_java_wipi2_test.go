package ktf

import (
	"context"
	"image"
	"image/color"
	"testing"
)

func TestKTFWIPI2HostSpecsDeclareAddedAPIs(t *testing.T) {
	want := map[string][]string{
		"org/kwis/msp/lcdui/AnimateImage": {
			"createAnimateImage(III)Lorg/kwis/msp/lcdui/AnimateImage;",
			"setFrameImage(Lorg/kwis/msp/lcdui/Image;I)V",
			"paintFrame(Lorg/kwis/msp/lcdui/Graphics;III)Z",
		},
		"org/kwis/msp/media/BaseClip": {
			"free()V",
			"mediaControl(I[I[I)I",
			"mediaModeControl(Ljava/lang/String;II[I)I",
		},
		"org/kwis/msp/media/StillClip": {
			"snapshot(Lorg/kwis/msp/media/PlayListener;)Z",
			"getData()[B",
		},
		"org/kwis/msp/media/VideoClip": {
			"record(Lorg/kwis/msp/media/PlayListener;)Z",
			"pause()Z",
		},
		"org/kwis/msp/handset/AddressBook": {
			"getAddressBook()Lorg/kwis/msp/handset/AddressBook;",
			"createRecord([Ljava/lang/Object;)I",
			"setShortCut([I[I[I)Z",
		},
		"org/kwis/msp/handset/GPSProvider": {
			"requestLocationInfo(I)I",
			"setLocationInfoListener(Lorg/kwis/msp/handset/GPSListener;)V",
		},
		"org/kwis/msp/io/IODevice": {
			"<init>(Ljava/lang/String;I[B)V",
			"read([BII)I",
			"control(Ljava/lang/String;[B[B)V",
		},
		"org/kwis/msp/io/SMS": {
			"send(Ljava/lang/String;Lorg/kwis/msp/io/SMSMessage;)I",
			"send(Ljava/lang/String;Ljava/lang/String;Lorg/kwis/msp/io/SMSMessage;)I",
			"getMaxMsgLength(Ljava/lang/String;)I",
		},
		"org/kwis/msp/io/ResourceGroup": {
			"writeData(Ljava/lang/String;Ljava/lang/String;[B)Ljava/lang/String;",
			"writeData(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;[BZ)Ljava/lang/String;",
			"search(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Z)[Ljava/lang/String;",
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

func TestKTFWIPI2MutableAnimateImageStoresFramesAndRates(t *testing.T) {
	runtime := newTestRuntime(t)
	parameters := allocWords(t, runtime, 7)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{2, 4, 3, 0, 0, 0, 0}))
	animated, err := runtime.handleAnimateImageMethod(
		"createAnimateImage", "(III)Lorg/kwis/msp/lcdui/AnimateImage;",
	)
	check(t, err)
	state := runtime.animateImages[animated]
	if state == nil || !state.mutable || len(state.frames) != 2 ||
		state.width != 4 || state.height != 3 {
		t.Fatalf("AnimateImage state = %+v", state)
	}
	check(t, runtime.writeWords(parameters, []uint32{animated, 75, 1, 0, 0, 0, 0}))
	_, err = runtime.handleAnimateImageMethod("setAnimationRate", "(II)V")
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{animated, 1, 0, 0, 0, 0, 0}))
	rate, err := runtime.handleAnimateImageMethod("getAnimationRate", "(I)I")
	check(t, err)
	if rate != 75 {
		t.Fatalf("AnimateImage rate = %d", rate)
	}
	frame, err := runtime.handleAnimateImageMethod(
		"getFrameImage", "(I)Lorg/kwis/msp/lcdui/Image;",
	)
	check(t, err)
	if frame != state.frames[1] {
		t.Fatalf("AnimateImage frame = 0x%08x, want 0x%08x", frame, state.frames[1])
	}
}

func TestKTFWIPI2GraphicsExtensionsRenderPixelsAndReset(t *testing.T) {
	runtime := newTestRuntime(t)
	graphics := newHostObject(t, runtime, "org/kwis/msp/lcdui/Graphics")
	target := image.NewRGBA(image.Rect(0, 0, 8, 8))
	runtime.Graphics[graphics] = &ktfGraphics{
		Target: target,
		clip:   target.Bounds(),
		color:  color.RGBA{R: 0xff, A: 0xff},
	}
	xs, err := runtime.newJavaIntArray([]uint32{1, 6, 1})
	check(t, err)
	ys, err := runtime.newJavaIntArray([]uint32{1, 1, 6})
	check(t, err)
	parameters := allocWords(t, runtime, 9)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{graphics, xs, ys, 0, 0, 0, 0, 0, 0}))
	_, err = runtime.handleGraphicsMethod("fillPolygon", "([I[I)V")
	check(t, err)
	red := color.RGBAModel.Convert(target.At(2, 2)).(color.RGBA)
	if red.R != 0xff || red.A != 0xff {
		t.Fatalf("filled polygon pixel = %#v", red)
	}

	pixels, err := runtime.newJavaByteArray([]byte{0x20, 0x40, 0x60, 0x80})
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{graphics, 3, 3, 2, 2, pixels, 0, 2, 0}))
	_, err = runtime.handleGraphicsMethod("setPixels", "(IIII[BII)V")
	check(t, err)
	gray := color.RGBAModel.Convert(target.At(4, 4)).(color.RGBA)
	if gray.R != 0x80 || gray.G != 0x80 || gray.B != 0x80 {
		t.Fatalf("setPixels pixel = %#v", gray)
	}

	state := runtime.Graphics[graphics]
	state.translate = image.Pt(3, 4)
	state.clip = image.Rect(2, 2, 4, 4)
	state.color = color.RGBA{R: 1, G: 2, B: 3, A: 4}
	state.xorMode = true
	check(t, runtime.writeWords(parameters, []uint32{graphics, 0, 0, 0, 0, 0, 0, 0, 0}))
	_, err = runtime.handleGraphicsMethod("reset", "()V")
	check(t, err)
	if state.translate != (image.Point{}) || state.clip != target.Bounds() ||
		state.color != (color.RGBA{A: 0xff}) || state.xorMode {
		t.Fatalf("reset graphics state = %+v", state)
	}
}

func TestKTFWIPI2ListSelectionAndImageOverloadsAreStateful(t *testing.T) {
	runtime := newTestRuntime(t)
	list := newHostObject(t, runtime, "org/kwis/msp/lwc/ListComponent")
	first := newJavaString(t, runtime, "first")
	second := newJavaString(t, runtime, "second")
	imageObject := newHostObject(t, runtime, "org/kwis/msp/lcdui/Image")
	_, err := runtime.handleLWCMethod(
		context.Background(), "org/kwis/msp/lwc/ListComponent", "<init>",
		"(I)V", []uint32{0, list, 2},
	)
	check(t, err)
	_, err = runtime.handleLWCMethod(
		context.Background(), "org/kwis/msp/lwc/ListComponent", "append",
		"(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;)I",
		[]uint32{0, list, first, 0},
	)
	check(t, err)
	_, err = runtime.handleLWCMethod(
		context.Background(), "org/kwis/msp/lwc/ListComponent", "insert",
		"(ILjava/lang/String;Lorg/kwis/msp/lcdui/Image;)I",
		[]uint32{0, list, 0, second, imageObject},
	)
	check(t, err)
	_, err = runtime.handleLWCMethod(
		context.Background(), "org/kwis/msp/lwc/ListComponent", "select",
		"(I)V", []uint32{0, list, 0},
	)
	check(t, err)
	selected, err := runtime.handleLWCMethod(
		context.Background(), "org/kwis/msp/lwc/ListComponent", "isSelected",
		"(I)Z", []uint32{0, list, 0},
	)
	check(t, err)
	if selected != 1 || runtime.Vectors[list][0] != second ||
		runtime.lwcComponent(list).itemImages[0] != imageObject {
		t.Fatalf("list state = %+v", runtime.lwcComponent(list))
	}
	snapshot := snapshotKTFLWC(runtime.lwcComponent(list))
	restored := restoreKTFLWC(snapshot)
	if !restored.selectedItems[0] {
		t.Fatalf("restored selection = %+v", restored.selectedItems)
	}
}

func TestKTFWIPI2VolumeSettingsRoundTrip(t *testing.T) {
	runtime := newTestRuntime(t)
	parameters := allocWords(t, runtime, 3)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{7, 1, 0}))
	_, err := runtime.handleWIPI2VolumeMethod("setMuteState", "(IZ)V")
	check(t, err)
	muted, err := runtime.handleWIPI2VolumeMethod("getMute", "(I)Z")
	check(t, err)
	if muted != 1 {
		t.Fatalf("getMute = %d", muted)
	}
	check(t, runtime.writeWords(parameters, []uint32{7, 35, 0}))
	_, err = runtime.handleWIPI2VolumeMethod("setDefaultVolume", "(II)V")
	check(t, err)
	volume, err := runtime.handleWIPI2VolumeMethod("getDefaultVolume", "(I)I")
	check(t, err)
	if volume != 35 {
		t.Fatalf("getDefaultVolume = %d", volume)
	}
	check(t, runtime.writeWords(parameters, []uint32{64, 0, 0}))
	_, err = runtime.handleWIPI2VolumeMethod("set", "(I)V")
	check(t, err)
	volume, err = runtime.handleWIPI2VolumeMethod("get", "()I")
	check(t, err)
	if volume != 64 {
		t.Fatalf("Volume.get = %d", volume)
	}
}

func TestKTFWIPI2MediaHierarchyAndCameraCapture(t *testing.T) {
	if parent := HostJavaClassSpecs["org/kwis/msp/media/Clip"].Parent; parent != "org/kwis/msp/media/BaseClip" {
		t.Fatalf("Clip parent = %q", parent)
	}
	runtime := newTestRuntime(t)
	still := newHostObject(t, runtime, "org/kwis/msp/media/StillClip")
	mediaType := newJavaString(t, runtime, "image/jpeg")
	parameters := allocWords(t, runtime, 6)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{still, mediaType, 0, 0, 0, 0}))
	_, err := runtime.handleMediaMethod("<init>", "(Ljava/lang/String;)V")
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{still, 0, 0, 0, 0, 0}))
	captured, err := runtime.handleMediaMethod(
		"snapshot", "(Lorg/kwis/msp/media/PlayListener;)Z",
	)
	check(t, err)
	if captured != 1 || len(runtime.clips[still].data) < 2 ||
		runtime.clips[still].data[0] != 0xff || runtime.clips[still].data[1] != 0xd8 {
		t.Fatalf("StillClip snapshot result=%d size=%d", captured, len(runtime.clips[still].data))
	}
	data, err := runtime.handleMediaMethod("getData", "()[B")
	check(t, err)
	encoded, err := runtime.readJavaByteArray(data)
	check(t, err)
	if len(encoded) < 2 || len(runtime.clips[still].data) != 0 {
		t.Fatalf("StillClip.getData size=%d remaining=%d", len(encoded), len(runtime.clips[still].data))
	}
}

func TestKTFWIPI2MediaModeControlRoundTripsProperty(t *testing.T) {
	runtime := newTestRuntime(t)
	clip := newHostObject(t, runtime, "org/kwis/msp/media/Clip")
	mode := newJavaString(t, runtime, "DEFAULT_MODE")
	buffer, err := runtime.newJavaIntArray([]uint32{42})
	check(t, err)
	parameters := allocWords(t, runtime, 6)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{clip, mode, 1, 7, buffer, 0}))
	result, err := runtime.handleMediaMethod(
		"mediaModeControl", "(Ljava/lang/String;II[I)I",
	)
	check(t, err)
	if result != 0 {
		t.Fatalf("mediaModeControl set = %d", int32(result))
	}
	check(t, runtime.WriteU32(readU32(t, runtime, buffer)+8, 0))
	check(t, runtime.writeWords(parameters, []uint32{clip, mode, 0, 7, buffer, 0}))
	result, err = runtime.handleMediaMethod(
		"mediaModeControl", "(Ljava/lang/String;II[I)I",
	)
	check(t, err)
	value, err := runtime.readWIPI2IntArrayFirst(buffer)
	check(t, err)
	if result != 0 || value != 42 {
		t.Fatalf("mediaModeControl get = %d, value=%d", int32(result), value)
	}
}

func TestKTFWIPI2AddressBookStoresSearchesAndRemovesRecords(t *testing.T) {
	runtime := newTestRuntime(t)
	book := newHostObject(t, runtime, "org/kwis/msp/handset/AddressBook")
	name := newJavaString(t, runtime, "Alice")
	phone := newJavaString(t, runtime, "01012345678")
	fields, err := runtime.newJavaReferenceArray(
		"[Ljava/lang/Object;", []uint32{name, phone, 0, 0},
	)
	check(t, err)
	parameters := allocWords(t, runtime, 5)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{book, fields, 0, 0, 0}))
	recordID, err := runtime.handleWIPI2AddressMethod(
		"org/kwis/msp/handset/AddressBook", "createRecord",
		"([Ljava/lang/Object;)I",
	)
	check(t, err)
	if recordID != 1 {
		t.Fatalf("AddressBook.createRecord = %d, want 1", recordID)
	}

	check(t, runtime.writeWords(parameters, []uint32{book, 0, name, 1, 0}))
	matches, err := runtime.handleWIPI2AddressMethod(
		"org/kwis/msp/handset/AddressBook", "searchAddress",
		"(ILjava/lang/Object;Z)[I",
	)
	check(t, err)
	got, err := runtime.readWIPI2IntArray(matches)
	check(t, err)
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("AddressBook.searchAddress = %v", got)
	}

	check(t, runtime.writeWords(parameters, []uint32{book, recordID, 0, 0, 0}))
	address, err := runtime.handleWIPI2AddressMethod(
		"org/kwis/msp/handset/AddressBook", "getAddress",
		"(I)Lorg/kwis/msp/handset/Address;",
	)
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{address, 1, 0, 0, 0}))
	gotPhone, err := runtime.handleWIPI2AddressMethod(
		"org/kwis/msp/handset/Address", "getField", "(I)Ljava/lang/Object;",
	)
	check(t, err)
	if runtime.javaStringValue(gotPhone) != "01012345678" {
		t.Fatalf("Address.getField phone = %q", runtime.javaStringValue(gotPhone))
	}

	check(t, runtime.writeWords(parameters, []uint32{book, recordID, 0, 0, 0}))
	removed, err := runtime.handleWIPI2AddressMethod(
		"org/kwis/msp/handset/AddressBook", "removeAddress", "(I)Z",
	)
	check(t, err)
	if removed != 1 || len(runtime.wipi2Addresses) != 0 {
		t.Fatalf("AddressBook.removeAddress = %d, records=%d", removed, len(runtime.wipi2Addresses))
	}
}

func TestKTFWIPI2GPSConfigAndLocationRequestAreStateful(t *testing.T) {
	runtime := newTestRuntime(t)
	config := newHostObject(t, runtime, "org/kwis/msp/handset/GPSConfig")
	parameters := allocWords(t, runtime, 8)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{config, 1, 0, 3, 1, 0x7f000001, 7275, 0}))
	_, err := runtime.handleWIPI2LocationMethod(
		context.Background(), "org/kwis/msp/handset/GPSConfig",
		"<init>", "(IIIIII)V",
	)
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{config, 0, 0, 0, 0, 0, 0, 0}))
	port, err := runtime.handleWIPI2LocationMethod(
		context.Background(), "org/kwis/msp/handset/GPSConfig",
		"getPdePort", "()I",
	)
	check(t, err)
	if port != 7275 {
		t.Fatalf("GPSConfig.getPdePort = %d", port)
	}

	listener := newHostObject(t, runtime, "org/kwis/msp/handset/GPSListener")
	check(t, runtime.writeWords(parameters, []uint32{listener, 0, 0, 0, 0, 0, 0, 0}))
	_, err = runtime.handleWIPI2LocationMethod(
		context.Background(), "org/kwis/msp/handset/GPSProvider",
		"setLocationInfoListener", "(Lorg/kwis/msp/handset/GPSListener;)V",
	)
	check(t, err)
	provider := newHostObject(t, runtime, "org/kwis/msp/handset/GPSProvider")
	check(t, runtime.writeWords(parameters, []uint32{provider, 0, 0, 0, 0, 0, 0, 0}))
	_, err = runtime.handleWIPI2LocationMethod(
		context.Background(), "org/kwis/msp/handset/GPSProvider",
		"requestLocationInfo", "(I)I",
	)
	check(t, err)
	queued := len(runtime.PendingJavaCalls) + len(runtime.Tasks)
	if queued == 0 || len(runtime.wipi2GPSLocations) != 1 {
		t.Fatalf("GPS request queued work=%d locations=%d", queued, len(runtime.wipi2GPSLocations))
	}
}

func TestKTFWIPI2HandsetStateSnapshotIsIndependent(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.wipi2AddressGroups = []uint32{11}
	runtime.wipi2Addresses[1] = &ktfWIPI2Address{
		fields: []uint32{21, 22}, lockStatus: 1,
	}
	runtime.wipi2AddressObjects[31] = 1
	runtime.wipi2AddressShortcuts[7] = [2]int{1, 0}
	runtime.wipi2NextAddressID = 2
	runtime.wipi2GPSConfig = ktfWIPI2GPSConfig{pdePort: 7275}
	runtime.wipi2GPSLocations[41] = ktfWIPI2GPSLocation{
		latitude: 37566500, longitude: 126978000,
		timestamp: "1234", valid: true,
	}

	snapshot := snapshotWIPI2Handset(runtime)
	runtime.wipi2Addresses[1].fields[0] = 99
	runtime.wipi2AddressGroups[0] = 99
	restored := newTestRuntime(t)
	restoreWIPI2Handset(restored, snapshot)
	if restored.wipi2AddressGroups[0] != 11 ||
		restored.wipi2Addresses[1].fields[0] != 21 ||
		restored.wipi2AddressShortcuts[7] != [2]int{1, 0} ||
		restored.wipi2GPSConfig.pdePort != 7275 ||
		!restored.wipi2GPSLocations[41].valid {
		t.Fatalf("restored handset state is incomplete: %+v", snapshot)
	}
}

func TestKTFWIPI2IODeviceIsStatefulLoopback(t *testing.T) {
	runtime := newTestRuntime(t)
	device := newHostObject(t, runtime, "org/kwis/msp/io/IODevice")
	name := newJavaString(t, runtime, "loopback")
	parameters := allocWords(t, runtime, 5)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{device, name, 0, 0, 0}))
	_, err := runtime.handleWIPI2IODeviceMethod(
		"<init>", "(Ljava/lang/String;I[B)V",
	)
	check(t, err)

	source, err := runtime.newJavaByteArray([]byte{1, 2, 3, 4})
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{device, source, 1, 2, 0}))
	written, err := runtime.handleWIPI2IODeviceMethod("write", "([BII)I")
	check(t, err)
	if written != 2 {
		t.Fatalf("IODevice.write = %d, want 2", written)
	}

	target, err := runtime.newJavaByteArray([]byte{9, 9, 9, 9})
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{device, target, 1, 3, 0}))
	read, err := runtime.handleWIPI2IODeviceMethod("read", "([BII)I")
	check(t, err)
	if read != 2 {
		t.Fatalf("IODevice.read = %d, want 2", read)
	}
	got, err := runtime.readJavaByteArray(target)
	check(t, err)
	if string(got) != string([]byte{9, 2, 3, 9}) {
		t.Fatalf("IODevice target = %v", got)
	}
}

func TestKTFWIPI2SMSValidatesAndSendsPayload(t *testing.T) {
	runtime := newTestRuntime(t)
	message := newHostObject(t, runtime, "org/kwis/msp/io/SMSMessage")
	data, err := runtime.newJavaByteArray([]byte("hello"))
	check(t, err)
	parameters := allocWords(t, runtime, 4)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{message, data, 0, 0}))
	_, err = runtime.handleWIPI2SMSMethod(
		"org/kwis/msp/io/SMSMessage", "<init>", "([B)V",
	)
	check(t, err)
	if got := string(runtime.wipi2SMSMessages[message]); got != "hello" {
		t.Fatalf("SMSMessage payload = %q", got)
	}

	number := newJavaString(t, runtime, "01012345678")
	check(t, runtime.writeWords(parameters, []uint32{number, message, 0, 0}))
	result, err := runtime.handleWIPI2SMSMethod(
		"org/kwis/msp/io/SMS", "send",
		"(Ljava/lang/String;Lorg/kwis/msp/io/SMSMessage;)I",
	)
	check(t, err)
	if result != 0 {
		t.Fatalf("SMS.send = %d, want success", int32(result))
	}
}

func TestKTFWIPI2ResourceGroupStoresMetadataAndData(t *testing.T) {
	runtime := newTestRuntime(t)
	group := newHostObject(t, runtime, "org/kwis/msp/io/ResourceGroup")
	groupName := newJavaString(t, runtime, "image")
	title := newJavaString(t, runtime, "logo")
	uiName := newJavaString(t, runtime, "Logo")
	format := newJavaString(t, runtime, "image/png")
	data, err := runtime.newJavaByteArray([]byte{1, 2, 3})
	check(t, err)
	parameters := allocWords(t, runtime, 7)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{group, groupName, 0, 0, 0, 0, 0}))
	_, err = runtime.handleWIPI2ResourceGroupMethod(
		"<init>", "(Ljava/lang/String;)V",
	)
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{
		group, title, uiName, format, data, 1, 0,
	}))
	resourceName, err := runtime.handleWIPI2ResourceGroupMethod(
		"writeData",
		"(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;[BZ)Ljava/lang/String;",
	)
	check(t, err)
	if got := runtime.javaStringValue(resourceName); got != "logo" {
		t.Fatalf("ResourceGroup.writeData name = %q", got)
	}

	check(t, runtime.writeWords(parameters, []uint32{group, resourceName, 0, 0, 0, 0, 0}))
	stored, err := runtime.handleWIPI2ResourceGroupMethod(
		"getData", "(Ljava/lang/String;)[B",
	)
	check(t, err)
	payload, err := runtime.readJavaByteArray(stored)
	check(t, err)
	if string(payload) != string([]byte{1, 2, 3}) {
		t.Fatalf("ResourceGroup.getData = %v", payload)
	}

	// Exercise the normal dispatcher as well, so a spec-only declaration
	// cannot accidentally pass this test.
	count, err := HostJavaMethod(
		"org/kwis/msp/io/ResourceGroup", "getCount", "()I",
	)(context.Background(), runtime)
	check(t, err)
	if count != 1 {
		t.Fatalf("ResourceGroup.getCount = %d, want 1", count)
	}
	infoType := newJavaString(t, runtime, "NAME")
	check(t, runtime.writeWords(parameters, []uint32{group, infoType, 0, 0, 0, 0, 0}))
	groupInfo, err := runtime.handleWIPI2ResourceGroupMethod(
		"getGroupInfo", "(Ljava/lang/String;)[B",
	)
	check(t, err)
	encodedGroup, err := runtime.readJavaByteArray(groupInfo)
	check(t, err)
	if string(encodedGroup) != "image" {
		t.Fatalf("ResourceGroup.getGroupInfo = %q", encodedGroup)
	}
	check(t, runtime.writeWords(parameters, []uint32{group, resourceName, 0, 0, 0, 0, 0}))
	_, err = runtime.handleWIPI2ResourceGroupMethod(
		"deleteData", "(Ljava/lang/String;)V",
	)
	check(t, err)
	if len(runtime.wipi2Resources["image"]) != 0 {
		t.Fatal("ResourceGroup.deleteData(void) kept the resource")
	}
}

func TestKTFWIPI2StateSnapshotsAreDeepCopies(t *testing.T) {
	devices := map[uint32]*ktfWIPI2IODevice{
		1: {name: "loopback", number: 2, data: []byte{1, 2}},
	}
	resources := map[string]map[string]*ktfWIPI2Resource{
		"image": {
			"logo": {id: "logo", format: "image/png", data: []byte{3, 4}},
		},
	}
	restoredDevices := restoreWIPI2IODevices(snapshotWIPI2IODevices(devices))
	restoredResources := restoreWIPI2Resources(snapshotWIPI2Resources(resources))
	devices[1].data[0] = 9
	resources["image"]["logo"].data[0] = 9
	if restoredDevices[1].data[0] != 1 {
		t.Fatal("IODevice state snapshot aliases live data")
	}
	if restoredResources["image"]["logo"].data[0] != 3 {
		t.Fatal("ResourceGroup state snapshot aliases live data")
	}
}
