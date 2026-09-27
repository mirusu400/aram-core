package wipi

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSocketResponseReachesGuestWithAndWithoutReadEvent(t *testing.T) {
	for _, deliverEvent := range []bool{false, true} {
		t.Run(map[bool]string{false: "already-buffered", true: "waiting-callback"}[deliverEvent], func(t *testing.T) {
			r := newPublicRuntime(t)
			descriptor := dispatchPublicAPI(t, r, "MC_netSocket", 2, 1).Low
			if got := dispatchPublicAPI(t, r, "MC_netSocketConnect", descriptor, 0x7f000001, 8501, 0, 0).Low; got != 0 {
				t.Fatalf("connect = %x", got)
			}
			r.PendingCallbacks = nil
			if deliverEvent {
				dispatchPublicAPI(t, r, "MC_netSetReadCB", descriptor, 0x900, 0x55)
			}
			id := r.socketServices[int32(descriptor)]
			check(t, r.Services.InjectSocketResponse(r.ServiceOwner, id, []byte{1, 2, 3, 4}, r.Services.Clock.Monotonic()))
			if deliverEvent {
				if !r.DeliverSocketRead(id) {
					t.Fatal("response did not wake callback")
				}
			} else {
				dispatchPublicAPI(t, r, "MC_netSetReadCB", descriptor, 0x900, 0x55)
			}
			if len(r.PendingCallbacks) != 1 || r.PendingCallbacks[0].Args != [4]uint32{descriptor, 0, 0x55} {
				t.Fatalf("callbacks = %+v", r.PendingCallbacks)
			}
			buffer, err := r.Heap.Allocate(8, true)
			check(t, err)
			for _, want := range [][]byte{{1, 2}, {3, 4}, {}} {
				got := dispatchPublicAPI(t, r, "MC_netSocketRead", descriptor, buffer, 2).Low
				if got != uint32(len(want)) {
					t.Fatalf("read length = %d, want %d", got, len(want))
				}
				data := make([]byte, got)
				check(t, r.CPU.ReadMemory(buffer, data))
				if !bytes.Equal(data, want) {
					t.Fatalf("read = %x, want %x", data, want)
				}
			}
			info, err := r.Services.Network.SocketInfo(r.ServiceOwner, id)
			check(t, err)
			if info.ReadBytes != 0 || r.DeliverSocketRead(id) {
				t.Fatal("response delivered twice")
			}
			dispatchPublicAPI(t, r, "MC_netSetReadCB", descriptor, 0x900, 0x55)
			if r.DeliverSocketRead(id) {
				t.Fatal("stale response event woke a new callback")
			}
		})
	}
}

func TestWIPIFunterResponseDoesNotDuplicateCarrierGrant(t *testing.T) {
	r := newPublicRuntime(t)
	r.OfflineCarrierAuth = true
	descriptor := dispatchPublicAPI(t, r, "MC_netSocket", 2, 1).Low
	dispatchPublicAPI(t, r, "MC_netSocketConnect", descriptor, 0xd373cb11, 15102, 0, 0)
	buffer, err := r.Heap.Allocate(64, true)
	check(t, err)
	request := []byte{8, 0, 0, 0, 0xff, 0xff, 0x40, 6}
	check(t, r.CPU.WriteMemory(buffer, request))
	if n := dispatchPublicAPI(t, r, "MC_netSocketWrite", descriptor, buffer, 8).Low; n != 8 {
		t.Fatalf("write = %d", n)
	}
	n := dispatchPublicAPI(t, r, "MC_netSocketRead", descriptor, buffer, 64).Low
	got := make([]byte, n)
	check(t, r.CPU.ReadMemory(buffer, got))
	if n != 20 || binary.LittleEndian.Uint16(got[6:]) != 1601 || binary.LittleEndian.Uint32(got[16:]) != 0 {
		t.Fatalf("Funter empty catalogue = %x", got)
	}
	if n := dispatchPublicAPI(t, r, "MC_netSocketRead", descriptor, buffer, 64).Low; n != 0 {
		t.Fatalf("extra grant bytes = %d", n)
	}
}
