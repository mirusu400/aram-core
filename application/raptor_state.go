package application

import (
	"fmt"
	"sort"
)

const (
	raptorStateSchema      = uint32(1)
	maxRaptorStateSections = 1024
)

type raptorSectionState struct {
	address uint32
	data    []byte
}

type raptorSavedState struct {
	moduleInitialized bool
	started           bool
	sections          []raptorSectionState
	resolvedImports   map[uint32]uint64
	importTrace       []raptorImportCall
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
		len(m.raptor.importTrace) > maxSavedWIPIEntries {
		return fmt.Errorf("save Raptor state: metadata exceeds format limits")
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
	ordinals := make([]uint32, 0, len(m.raptor.resolvedImports))
	for ordinal := range m.raptor.resolvedImports {
		ordinals = append(ordinals, ordinal)
	}
	sort.Slice(ordinals, func(i, j int) bool { return ordinals[i] < ordinals[j] })
	writer.u32(uint32(len(ordinals)))
	for _, ordinal := range ordinals {
		writer.u32(ordinal)
		writer.u64(m.raptor.resolvedImports[ordinal])
	}
	writer.u32(uint32(len(m.raptor.importTrace)))
	for _, call := range m.raptor.importTrace {
		writer.u32(call.Ordinal)
		for _, argument := range call.Args {
			writer.u32(argument)
		}
		writer.u32(call.LR)
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
	if schema := decoder.u32(); schema != raptorStateSchema {
		return nil, decoder.fail(fmt.Sprintf("unsupported Raptor state schema %d", schema))
	}
	state := &raptorSavedState{
		moduleInitialized: decoder.u8() != 0,
		started:           decoder.u8() != 0,
		resolvedImports:   make(map[uint32]uint64),
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
	var previous uint32
	for index := uint32(0); index < importCount; index++ {
		ordinal, calls := decoder.u32(), decoder.u64()
		if (index != 0 && ordinal <= previous) || calls == 0 ||
			ordinal >= raptorImportStubSize/4 {
			return nil, decoder.fail("invalid Raptor resolved-import entry")
		}
		state.resolvedImports[ordinal] = calls
		previous = ordinal
	}
	traceCount := decoder.u32()
	if traceCount > maxSavedWIPIEntries {
		return nil, decoder.fail("Raptor import trace exceeds limit")
	}
	state.importTrace = make([]raptorImportCall, traceCount)
	for index := range state.importTrace {
		call := &state.importTrace[index]
		call.Ordinal = decoder.u32()
		for argument := range call.Args {
			call.Args[argument] = decoder.u32()
		}
		call.LR = decoder.u32()
		if call.Ordinal >= raptorImportStubSize/4 {
			return nil, decoder.fail("invalid Raptor import trace ordinal")
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
	m.raptor.resolvedImports = make(map[uint32]uint64, len(state.resolvedImports))
	for ordinal, calls := range state.resolvedImports {
		m.raptor.resolvedImports[ordinal] = calls
	}
	m.raptor.importTrace = append([]raptorImportCall(nil), state.importTrace...)
	return nil
}
