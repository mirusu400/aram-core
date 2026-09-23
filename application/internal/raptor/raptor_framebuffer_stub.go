package raptor

import (
	"encoding/binary"
	"fmt"
)

var raptorFramebufferPixelsImport = raptorImportKey{Module: 507, Ordinal: 50}

// resolvedImportStub returns the public address placed in a Raptor import
// veneer. Most imports remain host breakpoints. grpGetFrameBufferPixels is
// different: software renderers call it in their innermost pixel loop, so a
// host round trip for this descriptor field can consume millions of traps for
// a few hundred displayed frames.
func (r *Runtime) resolvedImportStub(key raptorImportKey) (uint32, error) {
	hostStub, err := r.importStub(key)
	if err != nil || key != raptorFramebufferPixelsImport {
		return hostStub, err
	}
	if err := r.installFramebufferPixelsStub(hostStub); err != nil {
		return 0, err
	}
	return raptorFramebufferPixelsStub, nil
}

func (r *Runtime) installFramebufferPixelsStub(hostStub uint32) error {
	originRows := byte(0)
	if r.primaryFramebufferHeight == 0 {
		originRows = raptorScreenOriginY
	}
	code := raptorFramebufferPixelsCode(hostStub, originRows)
	if err := r.CPU.WriteMemory(raptorFramebufferPixelsStub, code); err != nil {
		return fmt.Errorf("install Raptor framebuffer-pixels helper: %w", err)
	}
	return nil
}

// raptorFramebufferPixelsCode builds a Thumb-1 helper for the six-word WIPI
// framebuffer descriptor:
//
//	pixels, width, height, bytes-per-line, bits-per-pixel, owns-pixels
//
// Valid descriptors return without leaving the guest. A zero handle, a raw
// pixel pointer, or a malformed descriptor branches to the ordinary host stub,
// preserving the compatibility fallbacks in DispatchPrivateImport. The screen
// descriptor is the only one that does not own its pixels; its raw pointer is
// shifted below LGT Raptor's reserved handset strip when that default geometry
// is active.
func raptorFramebufferPixelsCode(hostStub uint32, originRows byte) []byte {
	code := []byte{
		0x00, 0x28, // cmp r0, #0
		0x1a, 0xd0, // beq fallback
		0x02, 0x68, // ldr r2, [r0, #0]       ; pixels
		0x13, 0x0e, // lsrs r3, r2, #24
		0x10, 0x2b, // cmp r3, #0x10          ; heap is 0x10000000..0x11ffffff
		0x01, 0xd0, // beq pixels_in_heap
		0x11, 0x2b, // cmp r3, #0x11
		0x14, 0xd1, // bne fallback
		0x01, 0x69, // pixels_in_heap: ldr r1, [r0, #16] ; bits-per-pixel
		0x10, 0x29, // cmp r1, #16
		0x01, 0xd0, // beq bpp_valid
		0x20, 0x29, // cmp r1, #32
		0x0f, 0xd1, // bne fallback
		0x41, 0x69, // bpp_valid: ldr r1, [r0, #20] ; owns-pixels
		0x01, 0x29, // cmp r1, #1
		0x0c, 0xd8, // bhi fallback
		0xc3, 0x68, // ldr r3, [r0, #12]      ; bytes-per-line
		0x00, 0x2b, // cmp r3, #0
		0x09, 0xd0, // beq fallback
		0x00, 0x29, // cmp r1, #0
		0x05, 0xd1, // bne return_pixels
		0x81, 0x68, // ldr r1, [r0, #8]       ; screen height
		originRows, 0x29, // cmp r1, #originRows
		0x02, 0xd9, // bls return_pixels
		originRows, 0x21, // movs r1, #originRows
		0x4b, 0x43, // muls r3, r1
		0xd2, 0x18, // adds r2, r2, r3
		0x10, 0x46, // return_pixels: mov r0, r2
		0x70, 0x47, // bx lr
		0x01, 0x4b, // fallback: ldr r3, [pc, #4]
		0x18, 0x47, // bx r3
		0x00, 0xbf, // nop (align literal)
		0, 0, 0, 0, // host breakpoint address | Thumb bit
	}
	binary.LittleEndian.PutUint32(code[len(code)-4:], hostStub|1)
	return code
}
