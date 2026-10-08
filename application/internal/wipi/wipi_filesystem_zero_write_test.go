package wipi

import (
	"bytes"
	"reflect"
	"testing"
)

func TestWIPIFilesystemZeroLengthWritePreservesFileAndPosition(t *testing.T) {
	runtime := newPublicRuntime(t)
	name, err := runtime.Heap.Allocate(32, true)
	check(t, err)
	_, err = runtime.writeCString(name, []byte("zero.dat"), -1)
	check(t, err)
	fd := dispatchPublicAPI(t, runtime, "MC_fsOpen", name, 8, 0).Low
	if int32(fd) < 3 {
		t.Fatalf("open descriptor = %d", fd)
	}
	data, err := runtime.Heap.Allocate(3, true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(data, []byte{1, 2, 3}))
	if count := dispatchPublicAPI(t, runtime, "MC_fsWrite", fd, data, 3).Low; count != 3 {
		t.Fatalf("initial write = %d", count)
	}
	if offset := dispatchPublicAPI(t, runtime, "MC_fsSeek", fd, 10, 0).Low; offset != 10 {
		t.Fatalf("seek = %d", offset)
	}
	before := runtime.Services.Storage.Snapshot()
	if count := dispatchPublicAPI(t, runtime, "MC_fsWrite", fd, data, 0).Low; count != 0 {
		t.Fatalf("zero-byte write = %d", count)
	}
	if !bytes.Equal(runtime.Files["/private/zero.dat"], []byte{1, 2, 3}) ||
		!reflect.DeepEqual(runtime.Services.Storage.Snapshot(), before) {
		t.Fatal("zero-byte guest write changed file or shared storage")
	}
	if offset := dispatchPublicAPI(t, runtime, "MC_fsSeek", fd, 0, 1).Low; offset != 10 {
		t.Fatalf("position after zero-byte write = %d, want 10", offset)
	}
}
