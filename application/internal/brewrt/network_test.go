package brewrt

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestBREWOfflineSocketsAreAllocatedBeforeConnection(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	call := func(base, slot, receiver, arg uint32) uint32 {
		t.Helper()
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0: receiver,
			cpu.RegisterR1: arg,
			cpu.RegisterLR: returnTrap | 1,
		} {
			if err := runtime.cpu.WriteRegister(register, value); err != nil {
				t.Fatal(err)
			}
		}
		handled, _, _, err := runtime.handleAppletMethodTrap(base + slot*2 + 2)
		if err != nil || !handled {
			t.Fatalf("trap base=0x%x slot=%d handled=%v err=%v", base, slot, handled, err)
		}
		result, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := call(netTrapBase, 5, netObject, 0)  // AEE_SOCK_STREAM
	second := call(netTrapBase, 5, netObject, 1) // AEE_SOCK_DGRAM
	if first == 0 || second == 0 || first == second {
		t.Fatalf("OpenSocket returned non-distinct handles 0x%x and 0x%x", first, second)
	}
	for _, object := range []uint32{first, second} {
		var encoded [4]byte
		if err := runtime.cpu.ReadMemory(object, encoded[:]); err != nil {
			t.Fatal(err)
		}
		if got := binary.LittleEndian.Uint32(encoded[:]); got != socketVTable {
			t.Fatalf("socket 0x%x vtable=0x%x, want 0x%x", object, got, socketVTable)
		}
	}
	if got := call(socketTrapBase, 7, first, 0); got != brewNetError {
		t.Fatalf("offline Connect=%d, want AEE_NET_ERROR", got)
	}
	if got := call(socketTrapBase, 6, first, 0); got != brewNetDown {
		t.Fatalf("socket GetLastError=0x%x, want AEE_NET_ENETDOWN", got)
	}
	if got := call(socketTrapBase, 4, first, 0); got != 0 {
		t.Fatalf("socket Cancel=%d, want success", got)
	}
	if got := call(netTrapBase, 5, netObject, 99); got != 0 {
		t.Fatalf("unsupported socket type returned 0x%x, want null", got)
	}
	if got := call(netTrapBase, 4, netObject, 0); got != brewNetSockNotSupported {
		t.Fatalf("INetMgr GetLastError=0x%x, want AEE_NET_ESOCKNOSUPPORT", got)
	}
	runtime.releaseInterfaceObject(first)
	if runtime.socketHandles[first] != nil || runtime.heapAllocated[first] != 0 {
		t.Fatal("released socket remains allocated")
	}
	if runtime.socketHandles[second] == nil {
		t.Fatal("releasing one socket also removed the other")
	}
}
