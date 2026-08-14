package raptor

import (
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
)

const (
	raptorStateSchemaLegacy     = uint32(2)
	raptorStateSchema           = uint32(3)
	maxRaptorStateSections      = 1024
	maxRaptorStateCallbackTasks = 1024
)

type raptorSectionState struct {
	address uint32
	data    []byte
}

type SavedState struct {
	ModuleInitialized bool
	Started           bool
	sections          []raptorSectionState
	resolvedImports   map[raptorImportKey]uint64
	importSlots       []raptorImportKey
	ImportTrace       []raptorImportCall
	CallbackTasks     []*CallbackTask
}

func WriteState(r *Runtime, backend cpu.Backend, writer *guest.StateWriter) error {
	if r == nil {
		writer.U8(0)
		writer.Write([]byte{0, 0, 0})
		return nil
	}
	sections := r.Pkg.Image.AllocatedSections()
	if len(sections) > maxRaptorStateSections ||
		len(r.resolvedImports) > wipirt.MaxSavedEntries ||
		len(r.importSlots) != len(r.resolvedImports) ||
		len(r.ImportTrace) > wipirt.MaxSavedEntries ||
		len(r.CallbackTasks) > maxRaptorStateCallbackTasks {
		return fmt.Errorf("save Raptor state: metadata exceeds format limits")
	}
	for _, task := range r.CallbackTasks {
		if task == nil || task.Callback.Procedure == 0 ||
			len(task.Context) > guest.MaxStateContext {
			return fmt.Errorf("save Raptor state: invalid callback task")
		}
	}
	writer.U8(1)
	writer.Write([]byte{0, 0, 0})
	writer.U32(raptorStateSchema)
	if r.ModuleInitialized {
		writer.U8(1)
	} else {
		writer.U8(0)
	}
	if r.Started {
		writer.U8(1)
	} else {
		writer.U8(0)
	}
	writer.Write([]byte{0, 0})
	writer.U32(uint32(len(sections)))
	for _, section := range sections {
		writer.U32(section.Address)
		writer.U32(section.Size)
		if err := guest.WriteMemoryState(writer, backend, section.Address, section.Size); err != nil {
			return fmt.Errorf("save Raptor section %q: %w", section.Name, err)
		}
	}
	writer.U32(uint32(len(r.importSlots)))
	for _, key := range r.importSlots {
		writer.U32(key.Module)
		writer.U32(key.Ordinal)
		writer.U64(r.resolvedImports[key])
	}
	writer.U32(uint32(len(r.ImportTrace)))
	for _, call := range r.ImportTrace {
		writer.U32(call.Module)
		writer.U32(call.Ordinal)
		for _, argument := range call.Args {
			writer.U32(argument)
		}
		writer.U32(call.LR)
	}
	writer.U32(uint32(len(r.CallbackTasks)))
	for _, task := range r.CallbackTasks {
		writer.U32(task.Callback.Procedure)
		for _, argument := range task.Callback.Args {
			writer.U32(argument)
		}
		writer.U32(uint32(len(task.Context)))
		writer.Write(task.Context)
	}
	return nil
}

func ParseState(r *Runtime,
	decoder *guest.StateDecoder,
) (*SavedState, error) {
	present := decoder.U8()
	decoder.Reserved(3)
	if present > 1 {
		return nil, decoder.Fail("invalid Raptor state presence")
	}
	if present == 0 {
		if r != nil {
			return nil, decoder.Fail("Raptor state component is missing")
		}
		return nil, nil
	}
	if r == nil {
		return nil, decoder.Fail("unexpected Raptor state component")
	}
	schema := decoder.U32()
	if schema != raptorStateSchemaLegacy && schema != raptorStateSchema {
		return nil, decoder.Fail(fmt.Sprintf("unsupported Raptor state schema %d", schema))
	}
	state := &SavedState{
		ModuleInitialized: decoder.U8() != 0,
		Started:           decoder.U8() != 0,
		resolvedImports:   make(map[raptorImportKey]uint64),
	}
	decoder.Reserved(2)
	sections := r.Pkg.Image.AllocatedSections()
	count := decoder.U32()
	if count != uint32(len(sections)) || count > maxRaptorStateSections {
		return nil, decoder.Fail("Raptor section table mismatch")
	}
	state.sections = make([]raptorSectionState, count)
	for index, section := range sections {
		address, size := decoder.U32(), decoder.U32()
		if address != section.Address || size != section.Size {
			return nil, decoder.Fail(fmt.Sprintf(
				"Raptor section %d geometry mismatch",
				index,
			))
		}
		state.sections[index] = raptorSectionState{
			address: address,
			data:    append([]byte(nil), decoder.Bytes(int(size))...),
		}
	}
	importCount := decoder.U32()
	if importCount > wipirt.MaxSavedEntries {
		return nil, decoder.Fail("Raptor resolved-import table exceeds limit")
	}
	for index := uint32(0); index < importCount; index++ {
		key := raptorImportKey{Module: decoder.U32(), Ordinal: decoder.U32()}
		calls := decoder.U64()
		if key.Module == 0 || calls == 0 {
			return nil, decoder.Fail("invalid Raptor resolved-import entry")
		}
		if _, exists := state.resolvedImports[key]; exists {
			return nil, decoder.Fail("duplicate Raptor resolved-import entry")
		}
		state.resolvedImports[key] = calls
		state.importSlots = append(state.importSlots, key)
	}
	traceCount := decoder.U32()
	if traceCount > wipirt.MaxSavedEntries {
		return nil, decoder.Fail("Raptor import trace exceeds limit")
	}
	state.ImportTrace = make([]raptorImportCall, traceCount)
	for index := range state.ImportTrace {
		call := &state.ImportTrace[index]
		call.Module = decoder.U32()
		call.Ordinal = decoder.U32()
		for argument := range call.Args {
			call.Args[argument] = decoder.U32()
		}
		call.LR = decoder.U32()
		if call.Module == 0 {
			return nil, decoder.Fail("invalid Raptor import trace ordinal")
		}
	}
	if schema >= raptorStateSchema {
		callbackCount := decoder.U32()
		if callbackCount > maxRaptorStateCallbackTasks {
			return nil, decoder.Fail("Raptor callback task table exceeds limit")
		}
		state.CallbackTasks = make([]*CallbackTask, callbackCount)
		for index := range state.CallbackTasks {
			task := &CallbackTask{}
			task.Callback.Procedure = decoder.U32()
			for argument := range task.Callback.Args {
				task.Callback.Args[argument] = decoder.U32()
			}
			contextSize := decoder.U32()
			if task.Callback.Procedure == 0 || contextSize > guest.MaxStateContext {
				return nil, decoder.Fail("invalid Raptor callback task")
			}
			task.Context = append([]byte(nil), decoder.Bytes(int(contextSize))...)
			state.CallbackTasks[index] = task
		}
	}
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	return state, nil
}

func RestoreState(r *Runtime, backend cpu.Backend, state *SavedState) error {
	if r == nil {
		if state != nil {
			return fmt.Errorf("restore Raptor state: adapter is absent")
		}
		return nil
	}
	if state == nil {
		return fmt.Errorf("restore Raptor state: component is missing")
	}
	for index, section := range state.sections {
		if err := backend.WriteMemory(section.address, section.data); err != nil {
			return fmt.Errorf("restore Raptor section %d: %w", index, err)
		}
	}
	r.ModuleInitialized = state.ModuleInitialized
	r.Started = state.Started
	r.resolvedImports = make(map[raptorImportKey]uint64, len(state.resolvedImports))
	r.importSlots = append([]raptorImportKey(nil), state.importSlots...)
	r.importSlotByKey = make(map[raptorImportKey]uint32, len(state.importSlots))
	for slot, key := range state.importSlots {
		r.resolvedImports[key] = state.resolvedImports[key]
		r.importSlotByKey[key] = uint32(slot)
	}
	r.ImportTrace = append([]raptorImportCall(nil), state.ImportTrace...)
	r.CallbackTasks = make([]*CallbackTask, len(state.CallbackTasks))
	for index, saved := range state.CallbackTasks {
		r.CallbackTasks[index] = &CallbackTask{
			Callback: saved.Callback,
			Context:  append([]byte(nil), saved.Context...),
		}
	}
	return nil
}
