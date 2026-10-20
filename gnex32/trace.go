package gnex32

import "sort"

const (
	traceStackTailWords  = 16
	traceCallsPerService = 32
)

// TraceStackValue is one captured value-stack word at a service boundary.
// Reference is true when the word is an lvalue placeholder; Symbol and Element
// identify the guest storage that the service can update through that slot.
type TraceStackValue struct {
	Value     uint32
	Reference bool
	Symbol    int
	Element   int
}

// ServiceCallTrace records the bounded tail of the value stack immediately
// before a service is invoked. StackBase is the original index of Stack[0].
type ServiceCallTrace struct {
	Offset     int
	StackDepth int
	StackBase  int
	Stack      []TraceStackValue
}

// OpcodeTrace summarizes one dynamically reached opcode.
type OpcodeTrace struct {
	Opcode  uint16
	Count   uint64
	Offsets []int
}

// ServiceTrace summarizes one dynamically reached service and retains the
// first bounded set of argument-stack samples.
type ServiceTrace struct {
	ID             uint16
	Name           string
	Count          uint64
	Calls          []ServiceCallTrace
	DroppedSamples uint64
}

// ExecutionTrace is a deterministic snapshot of opt-in VM coverage. Opcodes
// and services are sorted numerically and their code offsets are sorted.
type ExecutionTrace struct {
	InstructionCount uint64
	Opcodes          []OpcodeTrace
	Services         []ServiceTrace
}

type opcodeTraceState struct {
	count   uint64
	offsets map[int]struct{}
}

type serviceTraceState struct {
	count   uint64
	calls   []ServiceCallTrace
	dropped uint64
}

type executionTraceState struct {
	instructions uint64
	opcodes      map[uint16]*opcodeTraceState
	services     map[uint16]*serviceTraceState
}

func newExecutionTraceState() *executionTraceState {
	return &executionTraceState{
		opcodes:  make(map[uint16]*opcodeTraceState),
		services: make(map[uint16]*serviceTraceState),
	}
}

func (state *executionTraceState) recordInstruction(offset int, opcode uint16) {
	state.instructions++
	entry := state.opcodes[opcode]
	if entry == nil {
		entry = &opcodeTraceState{offsets: make(map[int]struct{})}
		state.opcodes[opcode] = entry
	}
	entry.count++
	entry.offsets[offset] = struct{}{}
}

func (state *executionTraceState) recordService(offset int, id uint16, stack []uint32, refs map[int]gnex32SymbolRef) {
	entry := state.services[id]
	if entry == nil {
		entry = &serviceTraceState{}
		state.services[id] = entry
	}
	entry.count++
	if len(entry.calls) >= traceCallsPerService {
		entry.dropped++
		return
	}
	base := len(stack) - traceStackTailWords
	if base < 0 {
		base = 0
	}
	call := ServiceCallTrace{Offset: offset, StackDepth: len(stack), StackBase: base,
		Stack: make([]TraceStackValue, len(stack)-base)}
	for slot := base; slot < len(stack); slot++ {
		value := TraceStackValue{Value: stack[slot]}
		if ref, ok := refs[slot]; ok {
			value.Reference = true
			value.Symbol = ref.symbol
			value.Element = ref.element
		}
		call.Stack[slot-base] = value
	}
	entry.calls = append(entry.calls, call)
}

func (state *executionTraceState) snapshot() ExecutionTrace {
	result := ExecutionTrace{InstructionCount: state.instructions,
		Opcodes:  make([]OpcodeTrace, 0, len(state.opcodes)),
		Services: make([]ServiceTrace, 0, len(state.services))}
	for opcode, entry := range state.opcodes {
		item := OpcodeTrace{Opcode: opcode, Count: entry.count,
			Offsets: make([]int, 0, len(entry.offsets))}
		for offset := range entry.offsets {
			item.Offsets = append(item.Offsets, offset)
		}
		sort.Ints(item.Offsets)
		result.Opcodes = append(result.Opcodes, item)
	}
	for id, entry := range state.services {
		name, _ := ServiceName(id)
		item := ServiceTrace{ID: id, Name: name, Count: entry.count,
			Calls: make([]ServiceCallTrace, len(entry.calls)), DroppedSamples: entry.dropped}
		for index, call := range entry.calls {
			item.Calls[index] = call
			item.Calls[index].Stack = append([]TraceStackValue(nil), call.Stack...)
		}
		result.Services = append(result.Services, item)
	}
	sort.Slice(result.Opcodes, func(i, j int) bool { return result.Opcodes[i].Opcode < result.Opcodes[j].Opcode })
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].ID < result.Services[j].ID })
	return result
}

// EnableExecutionTrace starts a fresh bounded coverage trace. Trace state is
// diagnostic only: it is deliberately excluded from save states and execution
// semantics.
func (vm *VM) EnableExecutionTrace() {
	vm.trace = newExecutionTraceState()
}

// DisableExecutionTrace stops and discards execution tracing.
func (vm *VM) DisableExecutionTrace() {
	vm.trace = nil
}

// ResetExecutionTrace clears an enabled trace without disabling collection.
func (vm *VM) ResetExecutionTrace() {
	if vm.trace != nil {
		vm.trace = newExecutionTraceState()
	}
}

// ExecutionTrace returns an independent snapshot of the current trace.
func (vm *VM) ExecutionTrace() ExecutionTrace {
	if vm.trace == nil {
		return ExecutionTrace{}
	}
	return vm.trace.snapshot()
}
