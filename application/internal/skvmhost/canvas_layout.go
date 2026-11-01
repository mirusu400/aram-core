package skvmhost

import (
	"encoding/binary"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
	skengine "github.com/mirusu400/aram-core/skvm"
)

// skvmSystemStripHeight is the annunciator strip an SKT handset kept out of
// Canvas.getHeight() while drawing still covered the whole display.
const skvmSystemStripHeight = 16

// skvmTitleRestoresSystemStrip reports whether a title's own layout code adds
// the SKT system strip back to Canvas.getHeight(): the canvas height is read
// and immediately increased by 16 (`getHeight() + 16`).
//
// That idiom only makes sense on a handset whose Canvas reported its height
// without the strip, so a title that carries it was written for one. Reporting
// the full framebuffer height to it instead makes its layout 16 rows taller
// than the display, and whatever it anchors to the bottom edge (soft-key
// labels, status bars, credit lines) is drawn off-screen (aram-core #571,
// #573). The scan reads only class bytecode; it never consults file names.
func skvmTitleRestoresSystemStrip(classes map[string][]byte) bool {
	parsed := make([]*skengine.Class, 0, len(classes))
	superclasses := make(map[string]string, len(classes))
	for name, data := range classes {
		class, err := skengine.ParseClass(name, data)
		if err != nil {
			continue
		}
		parsed = append(parsed, class)
		superclasses[class.Name] = class.SuperName
	}
	for _, class := range parsed {
		for _, method := range class.Methods {
			if methodAddsStripToCanvasHeight(class, method.Code, superclasses) {
				return true
			}
		}
	}
	return false
}

func skvmPackageRestoresSystemStrip(pkg skloader.Package) bool {
	classes := make(map[string][]byte, len(pkg.Classes))
	for name, class := range pkg.Classes {
		classes[name] = class.Data
	}
	return skvmTitleRestoresSystemStrip(classes)
}

func methodAddsStripToCanvasHeight(
	class *skengine.Class,
	code []byte,
	superclasses map[string]string,
) bool {
	for pc := 0; pc < len(code); {
		length := bytecodeInstructionLength(code, pc)
		if length <= 0 || pc+length > len(code) {
			return false
		}
		if code[pc] == bytecodeInvokeVirtual &&
			addsStripAfter(code, pc+length) &&
			readsCanvasHeight(class, binary.BigEndian.Uint16(code[pc+1:]), superclasses) {
			return true
		}
		pc += length
	}
	return false
}

// addsStripAfter matches `bipush 16; iadd` or `sipush 16; iadd` at pc.
func addsStripAfter(code []byte, pc int) bool {
	switch {
	case pc+2 < len(code) && code[pc] == bytecodeBIPush:
		return int8(code[pc+1]) == skvmSystemStripHeight && code[pc+2] == bytecodeIAdd
	case pc+3 < len(code) && code[pc] == bytecodeSIPush:
		return int16(binary.BigEndian.Uint16(code[pc+1:])) == skvmSystemStripHeight &&
			code[pc+3] == bytecodeIAdd
	}
	return false
}

func readsCanvasHeight(
	class *skengine.Class,
	index uint16,
	superclasses map[string]string,
) bool {
	reference, err := class.Reference(index)
	if err != nil || reference.Kind != skengine.ReferenceMethod ||
		reference.Name != "getHeight" || reference.Descriptor != "()I" {
		return false
	}
	// Image.getHeight() has the same name and descriptor; only a Canvas, or
	// one of the title's own Canvas subclasses, reports the display height.
	name := reference.Class
	for range 32 {
		switch name {
		case "javax/microedition/lcdui/Canvas",
			"javax/microedition/lcdui/Displayable",
			"javax/microedition/lcdui/game/GameCanvas":
			return true
		}
		superclass, ok := superclasses[name]
		if !ok {
			return false
		}
		name = superclass
	}
	return false
}

const (
	bytecodeBIPush        = 0x10
	bytecodeSIPush        = 0x11
	bytecodeIAdd          = 0x60
	bytecodeTableSwitch   = 0xaa
	bytecodeLookupSwitch  = 0xab
	bytecodeInvokeVirtual = 0xb6
	bytecodeWide          = 0xc4
	bytecodeIInc          = 0x84
)

// bytecodeInstructionLength returns the encoded length of the JVM instruction
// at pc, or -1 for an opcode or switch table the method cannot hold.
func bytecodeInstructionLength(code []byte, pc int) int {
	switch opcode := code[pc]; {
	case opcode == bytecodeTableSwitch:
		base := (pc + 4) &^ 3
		if base+12 > len(code) {
			return -1
		}
		low := int32(binary.BigEndian.Uint32(code[base+4:]))
		high := int32(binary.BigEndian.Uint32(code[base+8:]))
		if high < low || int64(high)-int64(low) >= int64(len(code)) {
			return -1
		}
		return base + 12 + int(high-low+1)*4 - pc
	case opcode == bytecodeLookupSwitch:
		base := (pc + 4) &^ 3
		if base+8 > len(code) {
			return -1
		}
		pairs := int32(binary.BigEndian.Uint32(code[base+4:]))
		if pairs < 0 || int(pairs) > len(code) {
			return -1
		}
		return base + 8 + int(pairs)*8 - pc
	case opcode == bytecodeWide:
		if pc+1 < len(code) && code[pc+1] == bytecodeIInc {
			return 6
		}
		return 4
	default:
		return int(bytecodeLengths[opcode])
	}
}

// bytecodeLengths holds the fixed encoded length of each JVM opcode; zero
// marks an opcode that is undefined or variable-length.
var bytecodeLengths = func() (lengths [256]int8) {
	set := func(first, last int, length int8) {
		for opcode := first; opcode <= last; opcode++ {
			lengths[opcode] = length
		}
	}
	set(0x00, 0x0f, 1) // nop, constants
	set(0x10, 0x10, 2) // bipush
	set(0x11, 0x11, 3) // sipush
	set(0x12, 0x12, 2) // ldc
	set(0x13, 0x14, 3) // ldc_w, ldc2_w
	set(0x15, 0x19, 2) // typed loads
	set(0x1a, 0x35, 1) // short loads, array loads
	set(0x36, 0x3a, 2) // typed stores
	set(0x3b, 0x83, 1) // short stores, array stores, stack, arithmetic
	set(0x84, 0x84, 3) // iinc
	set(0x85, 0x98, 1) // conversions, comparisons
	set(0x99, 0xa8, 3) // branches, goto, jsr
	set(0xa9, 0xa9, 2) // ret
	set(0xac, 0xb1, 1) // returns
	set(0xb2, 0xb8, 3) // field access, invokes
	set(0xb9, 0xba, 5) // invokeinterface, invokedynamic
	set(0xbb, 0xbb, 3) // new
	set(0xbc, 0xbc, 2) // newarray
	set(0xbd, 0xbd, 3) // anewarray
	set(0xbe, 0xbf, 1) // arraylength, athrow
	set(0xc0, 0xc1, 3) // checkcast, instanceof
	set(0xc2, 0xc3, 1) // monitors
	set(0xc5, 0xc5, 4) // multianewarray
	set(0xc6, 0xc7, 3) // ifnull, ifnonnull
	set(0xc8, 0xc9, 5) // goto_w, jsr_w
	return lengths
}()
