package interpreter

import "github.com/mirusu400/aram-core/cpu"

// semihostingThumbImmediate is the SVC comment ARM semihosting uses in Thumb
// state; the ARM-state form uses SVC 0x123456.
const (
	semihostingThumbImmediate = 0xab
	semihostingARMImmediate   = 0x123456
)

// handleSemihosting services an ARM semihosting call. r0 selects the operation
// and r1 points to its parameter block; the result is returned in r0. Some LGT
// titles are built against a semihosting libc and route optional file/console
// I/O through this interface (리얼사커매니저2009 issues SYS_WRITE at startup). The
// host models no real semihosting files, so writes report full success and
// reads report end-of-file — the same observable behavior a device gives when
// that optional I/O has nothing to act on — which lets the title continue
// instead of trapping on an unhandled supervisor call. Returns true when the
// call was serviced.
func (b *Backend) handleSemihosting() bool {
	operation := b.regs[0]
	block := b.regs[1]
	switch operation {
	case 0x01: // SYS_OPEN -> non-zero file handle (or -1 on failure)
		b.regs[0] = 1
	case 0x02: // SYS_CLOSE -> 0 on success
		b.regs[0] = 0
	case 0x03, 0x04: // SYS_WRITEC / SYS_WRITE0 -> no status
		b.regs[0] = 0
	case 0x05: // SYS_WRITE(handle, buffer, count) -> bytes NOT written
		b.regs[0] = 0
	case 0x06: // SYS_READ(handle, buffer, count) -> bytes NOT read (count = EOF)
		buffer, bufErr := b.read32(block+4, cpu.PermissionRead)
		count, err := b.read32(block+8, cpu.PermissionRead)
		if err != nil {
			count = 0
		}
		// Model an empty file: zero the destination so the title reads defined
		// zero bytes instead of parsing whatever was left in the buffer (which
		// it otherwise treats as data and dereferences as a stale pointer).
		if bufErr == nil && buffer != 0 && count != 0 && count <= 1<<20 {
			zero := make([]byte, count)
			_ = b.copyIn(buffer, zero, cpu.PermissionWrite)
		}
		b.regs[0] = count
	case 0x09: // SYS_ISTTY -> 0 (not interactive)
		b.regs[0] = 0
	case 0x0a: // SYS_SEEK -> 0 on success
		b.regs[0] = 0
	case 0x0c: // SYS_FLEN -> file length, or -1 when unknown
		b.regs[0] = ^uint32(0)
	case 0x0e: // SYS_REMOVE -> 0 on success
		b.regs[0] = 0
	default:
		b.regs[0] = 0
	}
	return true
}
