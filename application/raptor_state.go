package application

import (
	"fmt"
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

type raptorSavedState struct {
	moduleInitialized bool
	started           bool
	sections          []raptorSectionState
	resolvedImports   map[raptorImportKey]uint64
	importSlots       []raptorImportKey
	importTrace       []raptorImportCall
	callbackTasks     []*raptorCallbackTask
}

func (m *Machine) writeRaptorState(writer *stateWriter) error {
	if m.raptor == nil {
		writer.u8(0)
		writer.write([]byte{0, 0, 0})
		return nil
	}
	sections := m.raptor.pkg.Image.AllocatedSections()
	if len(sections) > maxRaptorStateSections ||
		len(m.raptor.resolvedImports) > maxSavedWIPIEntries ||
		len(m.raptor.importSlots) != len(m.raptor.resolvedImports) ||
		len(m.raptor.importTrace) > maxSavedWIPIEntries ||
		len(m.raptor.callbackTasks) > maxRaptorStateCallbackTasks {
		return fmt.Errorf("save Raptor state: metadata exceeds format limits")
	}
	for _, task := range m.raptor.callbackTasks {
		if task == nil || task.callback.procedure == 0 ||
			len(task.context) > maxStateContext {
			return fmt.Errorf("save Raptor state: invalid callback task")
		}
	}
	writer.u8(1)
	writer.write([]byte{0, 0, 0})
	writer.u32(raptorStateSchema)
	if m.raptor.moduleInitialized {
		writer.u8(1)
	} else {
		writer.u8(0)
	}
	if m.raptor.started {
		writer.u8(1)
	} else {
		writer.u8(0)
	}
	writer.write([]byte{0, 0})
	writer.u32(uint32(len(sections)))
	for _, section := range sections {
		writer.u32(section.Address)
		writer.u32(section.Size)
		if err := m.writeMemoryState(writer, section.Address, section.Size); err != nil {
			return fmt.Errorf("save Raptor section %q: %w", section.Name, err)
		}
	}
	writer.u32(uint32(len(m.raptor.importSlots)))
	for _, key := range m.raptor.importSlots {
		writer.u32(key.Module)
		writer.u32(key.Ordinal)
		writer.u64(m.raptor.resolvedImports[key])
	}
	writer.u32(uint32(len(m.raptor.importTrace)))
	for _, call := range m.raptor.importTrace {
		writer.u32(call.Module)
		writer.u32(call.Ordinal)
		for _, argument := range call.Args {
			writer.u32(argument)
		}
		writer.u32(call.LR)
	}
	writer.u32(uint32(len(m.raptor.callbackTasks)))
	for _, task := range m.raptor.callbackTasks {
		writer.u32(task.callback.procedure)
		for _, argument := range task.callback.args {
			writer.u32(argument)
		}
		writer.u32(uint32(len(task.context)))
		writer.write(task.context)
	}
	return nil
}

func (m *Machine) parseRaptorState(
	decoder *stateDecoder,
) (*raptorSavedState, error) {
	present := decoder.u8()
	decoder.reserved(3)
	if present > 1 {
		return nil, decoder.fail("invalid Raptor state presence")
	}
	if present == 0 {
		if m.raptor != nil {
			return nil, decoder.fail("Raptor state component is missing")
		}
		return nil, nil
	}
	if m.raptor == nil {
		return nil, decoder.fail("unexpected Raptor state component")
	}
	schema := decoder.u32()
	if schema != raptorStateSchemaLegacy && schema != raptorStateSchema {
		return nil, decoder.fail(fmt.Sprintf("unsupported Raptor state schema %d", schema))
	}
	state := &raptorSavedState{
		moduleInitialized: decoder.u8() != 0,
		started:           decoder.u8() != 0,
		resolvedImports:   make(map[raptorImportKey]uint64),
	}
	decoder.reserved(2)
	sections := m.raptor.pkg.Image.AllocatedSections()
	count := decoder.u32()
	if count != uint32(len(sections)) || count > maxRaptorStateSections {
		return nil, decoder.fail("Raptor section table mismatch")
	}
	state.sections = make([]raptorSectionState, count)
	for index, section := range sections {
		address, size := decoder.u32(), decoder.u32()
		if address != section.Address || size != section.Size {
			return nil, decoder.fail(fmt.Sprintf(
				"Raptor section %d geometry mismatch",
				index,
			))
		}
		state.sections[index] = raptorSectionState{
			address: address,
			data:    append([]byte(nil), decoder.bytes(int(size))...),
		}
	}
	importCount := decoder.u32()
	if importCount > maxSavedWIPIEntries {
		return nil, decoder.fail("Raptor resolved-import table exceeds limit")
	}
	for index := uint32(0); index < importCount; index++ {
		key := raptorImportKey{Module: decoder.u32(), Ordinal: decoder.u32()}
		calls := decoder.u64()
		if key.Module == 0 || calls == 0 {
			return nil, decoder.fail("invalid Raptor resolved-import entry")
		}
		if _, exists := state.resolvedImports[key]; exists {
			return nil, decoder.fail("duplicate Raptor resolved-import entry")
		}
		state.resolvedImports[key] = calls
		state.importSlots = append(state.importSlots, key)
	}
	traceCount := decoder.u32()
	if traceCount > maxSavedWIPIEntries {
		return nil, decoder.fail("Raptor import trace exceeds limit")
	}
	state.importTrace = make([]raptorImportCall, traceCount)
	for index := range state.importTrace {
		call := &state.importTrace[index]
		call.Module = decoder.u32()
		call.Ordinal = decoder.u32()
		for argument := range call.Args {
			call.Args[argument] = decoder.u32()
		}
		call.LR = decoder.u32()
		if call.Module == 0 {
			return nil, decoder.fail("invalid Raptor import trace ordinal")
		}
	}
	if schema >= raptorStateSchema {
		callbackCount := decoder.u32()
		if callbackCount > maxRaptorStateCallbackTasks {
			return nil, decoder.fail("Raptor callback task table exceeds limit")
		}
		state.callbackTasks = make([]*raptorCallbackTask, callbackCount)
		for index := range state.callbackTasks {
			task := &raptorCallbackTask{}
			task.callback.procedure = decoder.u32()
			for argument := range task.callback.args {
				task.callback.args[argument] = decoder.u32()
			}
			contextSize := decoder.u32()
			if task.callback.procedure == 0 || contextSize > maxStateContext {
				return nil, decoder.fail("invalid Raptor callback task")
			}
			task.context = append([]byte(nil), decoder.bytes(int(contextSize))...)
			state.callbackTasks[index] = task
		}
	}
	if decoder.err != nil {
		return nil, decoder.err
	}
	return state, nil
}

func (m *Machine) restoreRaptorState(state *raptorSavedState) error {
	if m.raptor == nil {
		if state != nil {
			return fmt.Errorf("restore Raptor state: adapter is absent")
		}
		return nil
	}
	if state == nil {
		return fmt.Errorf("restore Raptor state: component is missing")
	}
	for index, section := range state.sections {
		if err := m.cpu.WriteMemory(section.address, section.data); err != nil {
			return fmt.Errorf("restore Raptor section %d: %w", index, err)
		}
	}
	m.raptor.moduleInitialized = state.moduleInitialized
	m.raptor.started = state.started
	m.raptor.resolvedImports = make(map[raptorImportKey]uint64, len(state.resolvedImports))
	m.raptor.importSlots = append([]raptorImportKey(nil), state.importSlots...)
	m.raptor.importSlotByKey = make(map[raptorImportKey]uint32, len(state.importSlots))
	for slot, key := range state.importSlots {
		m.raptor.resolvedImports[key] = state.resolvedImports[key]
		m.raptor.importSlotByKey[key] = uint32(slot)
	}
	m.raptor.importTrace = append([]raptorImportCall(nil), state.importTrace...)
	m.raptor.callbackTasks = make([]*raptorCallbackTask, len(state.callbackTasks))
	for index, saved := range state.callbackTasks {
		m.raptor.callbackTasks[index] = &raptorCallbackTask{
			callback: saved.callback,
			context:  append([]byte(nil), saved.context...),
		}
	}
	return nil
}
