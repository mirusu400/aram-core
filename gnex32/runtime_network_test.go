package gnex32

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

func newNetworkRuntime(t *testing.T) *Runtime {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 8)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	symbols[7] = gnex.GNEX32Symbol{Flags: 1, Words: 4, Data: make([]byte, 16)}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: []byte{
			0x86, 0x00, // main return
			0x04, 0x00, 0x00, 0x00, // push swData
			0x0b, 0x00, 0x06, 0x00, // save callback result in symbol 6
			0x86, 0x00,
		},
		Symbols: symbols,
		Media: []gnex.GNEX32Media{
			{Flags: 0, Data: []byte("211.233.42.196\x00")},
		},
		Callbacks: gnex.GNEX32Callbacks{Result: 162},
	}, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func networkResult(t *testing.T, runtime *Runtime) uint32 {
	t.Helper()
	result, err := runtime.vm.memory.ReadWord(6, 0)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNetConnectReportsDeterministicOfflineFailure(t *testing.T) {
	runtime := newNetworkRuntime(t)
	result, err := runtime.service(0x101, []uint32{0, 17002})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 0 || runtime.networkSocket == 0 {
		t.Fatalf("NetConnect result = %+v, socket = %d", result, runtime.networkSocket)
	}
	info, err := runtime.services.Network.SocketInfo(runtime.owner, runtime.networkSocket)
	if err != nil || info.State != shared.ConnectionClosed ||
		info.Host != "211.233.42.196" || info.Port != 17002 {
		t.Fatalf("offline socket = %+v, %v", info, err)
	}
	if err := runtime.dispatchReadyEvents(8); err != nil {
		t.Fatal(err)
	}
	if got := networkResult(t, runtime); got != 0 || runtime.networkSocket != 0 {
		t.Fatalf("offline result = %d, socket = %d", got, runtime.networkSocket)
	}
}

func TestNetConnectSendAndDisconnect(t *testing.T) {
	runtime := newNetworkRuntime(t)
	runtime.services.Device.SetNetworkAvailable(true)
	if _, err := runtime.service(0x101, []uint32{0, 17002}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.dispatchReadyEvents(8); err != nil {
		t.Fatal(err)
	}
	if got := networkResult(t, runtime); got != 1 {
		t.Fatalf("connect result = %d", got)
	}

	want := []byte{0x12, 0x34, 0x56, 0x78}
	if err := runtime.vm.memory.WriteBytes(7, 0, want); err != nil {
		t.Fatal(err)
	}
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 7}
	result, err := runtime.service(0x104, []uint32{0, uint32(len(want))})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 0 {
		t.Fatalf("NetSend result = %+v", result)
	}
	written, err := runtime.services.Network.SocketWritten(runtime.owner, runtime.networkSocket)
	if err != nil || !bytes.Equal(written, want) {
		t.Fatalf("written packet = %x, %v", written, err)
	}
	if status, _ := runtime.vm.memory.ReadWord(0, 0); status != 1 {
		t.Fatalf("NetSend swData = %d", status)
	}

	if result, err = runtime.service(0x102, nil); err != nil || result.Pop != 0 || len(result.Push) != 0 {
		t.Fatalf("NetDisconnect result = %+v, %v", result, err)
	}
	if runtime.networkSocket != 0 {
		t.Fatalf("socket remains after disconnect: %d", runtime.networkSocket)
	}
	if err := runtime.dispatchReadyEvents(8); err != nil {
		t.Fatal(err)
	}
	if got := networkResult(t, runtime); got != 9 {
		t.Fatalf("disconnect result = %d", got)
	}
	if _, err := runtime.service(0x102, nil); err != nil {
		t.Fatal(err)
	}
	if err := runtime.dispatchReadyEvents(8); err != nil {
		t.Fatal(err)
	}
	if got := networkResult(t, runtime); got != 10 {
		t.Fatalf("disconnected result = %d", got)
	}
}

func TestNetworkServicesValidateArguments(t *testing.T) {
	runtime := newNetworkRuntime(t)
	for name, stack := range map[string][]uint32{
		"missing port": {0},
		"zero port":    {0, 0},
		"large port":   {0, 65536},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runtime.service(0x101, stack); err == nil {
				t.Fatal("invalid NetConnect succeeded")
			}
		})
	}
	if _, err := runtime.service(0x104, []uint32{0, 1}); err != ErrInvalidLValue {
		t.Fatalf("NetSend without lvalue error = %v", err)
	}
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 7}
	for _, size := range []uint32{^uint32(0), 1025} {
		if _, err := runtime.service(0x104, []uint32{0, size}); err == nil {
			t.Fatalf("NetSend accepted size %d", int32(size))
		}
	}
	if _, err := runtime.service(0x104, []uint32{0, 0}); err != nil {
		t.Fatal(err)
	}
	if status, _ := runtime.vm.memory.ReadWord(0, 0); status != 0 {
		t.Fatalf("disconnected NetSend swData = %d", status)
	}
}

func TestConnectedNetworkRoundTripsRuntimeState(t *testing.T) {
	source := newNetworkRuntime(t)
	source.services.Device.SetNetworkAvailable(true)
	if _, err := source.service(0x101, []uint32{0, 17002}); err != nil {
		t.Fatal(err)
	}
	if err := source.dispatchReadyEvents(8); err != nil {
		t.Fatal(err)
	}
	source.vm.refs[0] = gnex32SymbolRef{symbol: 7}
	if err := source.vm.memory.WriteBytes(7, 0, []byte("packet")); err != nil {
		t.Fatal(err)
	}
	if _, err := source.service(0x104, []uint32{0, 6}); err != nil {
		t.Fatal(err)
	}
	clear(source.vm.refs)
	state, err := source.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	restored := newNetworkRuntime(t)
	if err := restored.UnmarshalBinary(state); err != nil {
		t.Fatal(err)
	}
	if restored.networkSocket == 0 {
		t.Fatal("restored network socket is missing")
	}
	info, err := restored.services.Network.SocketInfo(restored.owner, restored.networkSocket)
	if err != nil || info.State != shared.ConnectionConnected {
		t.Fatalf("restored socket = %+v, %v", info, err)
	}
	written, err := restored.services.Network.SocketWritten(restored.owner, restored.networkSocket)
	if err != nil || string(written) != "packet" {
		t.Fatalf("restored packet = %q, %v", written, err)
	}
}
