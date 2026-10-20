package gnex32

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/mirusu400/aram-core/loader/gnex"
)

// StaticServiceCall describes every statically reachable use of one 0x92
// service ID. Offsets are byte offsets relative to GNEX32Image.Code.
type StaticServiceCall struct {
	ID        uint16
	Name      string
	Supported bool
	Offsets   []int
}

// StaticOpcodeSite describes a reachable opcode the current VM cannot decode.
type StaticOpcodeSite struct {
	Opcode uint16
	Offset int
}

// StaticServiceAnalysis is a deterministic control-flow walk from the main
// entry and all nonzero header callbacks.
type StaticServiceAnalysis struct {
	Roots               []int
	InstructionCount    int
	ServiceCalls        []StaticServiceCall
	UnsupportedServices []StaticServiceCall
	UnknownOpcodes      []StaticOpcodeSite
}

// AnalyzeServices follows all direct branches and calls understood by the
// current VM. It distinguishes 0x92 instructions from identical words embedded
// in operands and reports service IDs missing from the runtime registry.
func AnalyzeServices(image gnex.GNEX32Image) (StaticServiceAnalysis, error) {
	if len(image.Code) == 0 || len(image.Code)&1 != 0 {
		return StaticServiceAnalysis{}, fmt.Errorf("gnex32: static analysis requires nonempty aligned code")
	}
	roots := make(map[int]struct{})
	addRoot := func(address uint32) error {
		offset := int64(address) - int64(image.Header.BodyOffset)
		if offset < 0 || offset >= int64(len(image.Code)) || offset&1 != 0 {
			return fmt.Errorf("gnex32: static analysis root %#x is outside aligned code", address)
		}
		roots[int(offset)] = struct{}{}
		return nil
	}
	if err := addRoot(image.Entry); err != nil {
		return StaticServiceAnalysis{}, err
	}
	for _, pointer := range image.Callbacks.Pointers() {
		if pointer != 0 {
			if err := addRoot(pointer); err != nil {
				return StaticServiceAnalysis{}, err
			}
		}
	}

	result := StaticServiceAnalysis{Roots: make([]int, 0, len(roots))}
	for root := range roots {
		result.Roots = append(result.Roots, root)
	}
	sort.Ints(result.Roots)
	queue := append([]int(nil), result.Roots...)
	visited := make(map[int]bool)
	owners := make([]int, len(image.Code)/2)
	for index := range owners {
		owners[index] = -1
	}
	calls := make(map[uint16][]int)

	for len(queue) != 0 {
		offset := queue[0]
		queue = queue[1:]
		if offset < 0 || offset&1 != 0 || offset >= len(image.Code) {
			return StaticServiceAnalysis{}, fmt.Errorf("gnex32: static control flow reaches invalid offset %d", offset)
		}
		if owner := owners[offset/2]; owner >= 0 && owner != offset {
			return StaticServiceAnalysis{}, fmt.Errorf("gnex32: static control flow reaches operand at offset %d owned by instruction %d", offset, owner)
		}
		if visited[offset] {
			continue
		}
		opcode := binary.LittleEndian.Uint16(image.Code[offset:])
		size, known := gnex32InstructionSize(opcode)
		if !known {
			visited[offset] = true
			owners[offset/2] = offset
			result.UnknownOpcodes = append(result.UnknownOpcodes, StaticOpcodeSite{Opcode: opcode, Offset: offset})
			continue
		}
		if len(image.Code)-offset < size {
			return StaticServiceAnalysis{}, fmt.Errorf("gnex32: truncated opcode %#x at code offset %d", opcode, offset)
		}
		for word := offset; word < offset+size; word += 2 {
			if owner := owners[word/2]; owner >= 0 && owner != offset {
				return StaticServiceAnalysis{}, fmt.Errorf("gnex32: overlapping instructions at offsets %d and %d", owner, offset)
			}
			owners[word/2] = offset
		}
		visited[offset] = true

		next := offset + size
		enqueue := func(successor int) { queue = append(queue, successor) }
		switch opcode {
		case 0xff, 0x86:
		case 0x81:
			instruction, err := image.DecodeAddressInstruction(offset)
			if err != nil {
				return StaticServiceAnalysis{}, err
			}
			enqueue(instruction.Target)
		case 0x85:
			instruction, err := image.DecodeAddressInstruction(offset)
			if err != nil {
				return StaticServiceAnalysis{}, err
			}
			enqueue(instruction.Target)
			enqueue(instruction.Next)
		case 0x7b, 0x7c, 0x7d, 0x7e, 0x7f, 0x80, 0x82, 0x83:
			instruction, err := image.DecodeAddressInstruction(offset)
			if err != nil {
				return StaticServiceAnalysis{}, err
			}
			enqueue(instruction.Target)
			enqueue(instruction.Next)
		default:
			if opcode == 0x92 {
				id := binary.LittleEndian.Uint16(image.Code[offset+2:])
				calls[id] = append(calls[id], offset)
			}
			enqueue(next)
		}
	}

	result.InstructionCount = len(visited)
	for id, offsets := range calls {
		sort.Ints(offsets)
		name, supported := ServiceName(id)
		call := StaticServiceCall{ID: id, Name: name, Supported: supported, Offsets: offsets}
		result.ServiceCalls = append(result.ServiceCalls, call)
		if !supported {
			result.UnsupportedServices = append(result.UnsupportedServices, call)
		}
	}
	sort.Slice(result.ServiceCalls, func(i, j int) bool { return result.ServiceCalls[i].ID < result.ServiceCalls[j].ID })
	sort.Slice(result.UnsupportedServices, func(i, j int) bool { return result.UnsupportedServices[i].ID < result.UnsupportedServices[j].ID })
	sort.Slice(result.UnknownOpcodes, func(i, j int) bool { return result.UnknownOpcodes[i].Offset < result.UnknownOpcodes[j].Offset })
	return result, nil
}

func gnex32InstructionSize(opcode uint16) (int, bool) {
	switch opcode {
	case 0xff, 0x86, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11,
		0x12, 0x13, 0x14, 0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58,
		0x59, 0x5a, 0x5b, 0x5c, 0x5d, 0x5e, 0x8f, 0x90, 0x91, 0x94, 0x95, 0x98:
		return 2, true
	case 0x04, 0x05, 0x06, 0x0b, 0x8c, 0x8e, 0x92:
		return 4, true
	case 0x02, 0x03, 0x07, 0x09, 0x0a, 0x74, 0x75, 0x7a, 0x8b, 0x8d,
		0x81, 0x82, 0x83, 0x85:
		return 6, true
	case 0x01, 0x68, 0x69, 0x6e, 0x6f, 0x72, 0x73, 0x78, 0x79,
		0x7b, 0x7c, 0x7d, 0x7e, 0x7f, 0x80:
		return 8, true
	case 0x66, 0x6d:
		return 10, true
	default:
		return 0, false
	}
}
