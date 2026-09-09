package ktf

import (
	"context"
	"testing"
)

func TestKTFWIPI2HostSpecsDeclareAddedAPIs(t *testing.T) {
	want := map[string][]string{
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
