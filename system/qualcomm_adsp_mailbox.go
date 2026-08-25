package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrQualcommADSPMailboxMMIO = errors.New("unsupported Qualcomm ADSP mailbox access")

const (
	qualcommADSPWriteMutexMask      = uint32(0x80000000)
	qualcommADSPWriteCommandMask    = uint32(0x70000000)
	qualcommADSPWriteRequest        = uint32(0x00000000)
	qualcommADSPWriteDone           = uint32(0x10000000)
	qualcommADSPWriteReady          = uint32(0x70000000)
	qualcommADSPMailboxStateVersion = uint32(2)
	qualcommADSPMailboxLegacyState  = uint32(1)
	qualcommADSPMaxPendingResponses = 4096
)

// QualcommADSPMailboxProfile locates the control aperture used by the
// Qualcomm ADSP RTOS host-to-DSP mailbox. The data banks and DSP address space
// remain separate profile regions because their size and bus width vary by
// chipset.
type QualcommADSPMailboxProfile struct {
	ID                 string
	Address            uint32
	Size               uint32
	WriteControlOffset uint32
	ControlRules       []QualcommADSPControlRuleProfile
	HostCommand        *QualcommADSPHostCommandProfile
}

// QualcommADSPControlRuleProfile describes a DSP-visible side effect of an
// exact mailbox register write. Board profiles use these rules for small
// hardware handshakes, such as the shared-memory alive flag asserted when a
// DSP image starts, without baking a firmware address into the device model.
type QualcommADSPControlRuleProfile struct {
	Offset                    uint32
	Value                     uint32
	ResponseDelayInstructions uint64
	Writes                    []QualcommADSPMemoryWriteProfile
	Interrupt                 *QualcommADSPInterruptProfile
}

// QualcommADSPInterruptProfile routes one DSP response event to the host
// interrupt controller. The shared-memory writes in the containing control
// rule are applied before the interrupt is pulsed, matching the ordering the
// host ISR observes.
type QualcommADSPInterruptProfile struct {
	Source                uint8
	UseVectoredController bool
}

type QualcommADSPMemoryWriteProfile struct {
	WindowID string
	Offset   uint32
	Width    Width
	Value    uint32
}

// QualcommADSPHostCommandProfile describes a firmware-visible command flag
// in an ADSP shared bank. A mailbox request applies the matching memory-copy
// rules and clears the flag, mirroring the small control ABI implemented by
// the DSP image without tying the emulator to guest code addresses.
type QualcommADSPHostCommandProfile struct {
	SelectorWindowID string
	SelectorOffset   uint32
	SelectorWidth    Width
	Rules            []QualcommADSPHostCommandRuleProfile
}

type QualcommADSPHostCommandRuleProfile struct {
	Command uint32
	Copies  []QualcommADSPMemoryCopyProfile
}

type QualcommADSPMemoryCopyProfile struct {
	SourceWindowID      string
	SourceOffset        uint32
	DestinationWindowID string
	DestinationOffset   uint32
	Width               Width
}

func (p QualcommADSPMailboxProfile) validate() error {
	if !validProfileID(p.ID) || p.Size == 0 || p.Size%uint32(Width32) != 0 ||
		p.Address%uint32(Width32) != 0 ||
		p.WriteControlOffset%uint32(Width32) != 0 ||
		uint64(p.WriteControlOffset)+uint64(Width32) > uint64(p.Size) ||
		uint64(p.Address)+uint64(p.Size) > 1<<32 {
		return fmt.Errorf("invalid Qualcomm ADSP mailbox profile %q", p.ID)
	}
	return nil
}

// QualcommADSPMailbox models the generic control-word handshake used by the
// Qualcomm ADSP RTOS. It intentionally does not emulate DSP algorithms. A
// write request receives a deterministic zero-status buffer acknowledgement,
// while write-done returns the mailbox to the ready state. The low 24 address
// bits are retained as the buffer address, which lets firmware copy the
// command through its separately mapped DSP address space.
type QualcommADSPMailbox struct {
	writeControlOffset uint32
	data               []byte
	controlRules       map[qualcommADSPControlKey]qualcommADSPControlRule
	pendingResponses   []qualcommADSPPendingResponse
	hostCommand        *qualcommADSPHostCommand
}

type qualcommADSPControlKey struct {
	offset uint32
	value  uint32
}

type qualcommADSPMemoryWrite struct {
	window *LatchedRegisterWindow
	offset uint32
	width  Width
	value  uint32
}

type qualcommADSPControlRule struct {
	delayInstructions uint64
	writes            []qualcommADSPMemoryWrite
	interrupt         *qualcommADSPInterrupt
}

type qualcommADSPPendingResponse struct {
	key                   qualcommADSPControlKey
	remainingInstructions uint64
}

type qualcommADSPInterrupt struct {
	source uint8
	pulser qualcommInterruptSourcePulser
}

type qualcommInterruptSourcePulser interface {
	PulseSource(uint8) error
}

type qualcommADSPHostCommand struct {
	selector *LatchedRegisterWindow
	offset   uint32
	width    Width
	rules    map[uint32][]qualcommADSPMemoryCopy
}

type qualcommADSPMemoryCopy struct {
	source            *LatchedRegisterWindow
	sourceOffset      uint32
	destination       *LatchedRegisterWindow
	destinationOffset uint32
	width             Width
}

func NewQualcommADSPMailbox(size, writeControlOffset uint32) (*QualcommADSPMailbox, error) {
	profile := QualcommADSPMailboxProfile{
		ID:                 "mailbox",
		Size:               size,
		WriteControlOffset: writeControlOffset,
	}
	if err := profile.validate(); err != nil || uint64(size) > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("invalid Qualcomm ADSP mailbox size/control offset")
	}
	return &QualcommADSPMailbox{
		writeControlOffset: writeControlOffset,
		data:               make([]byte, int(size)),
	}, nil
}

func (d *QualcommADSPMailbox) Reset() error {
	clear(d.data)
	d.pendingResponses = nil
	return nil
}

func (d *QualcommADSPMailbox) validAccess(offset uint32, width Width) bool {
	return width == Width32 && offset%uint32(Width32) == 0 &&
		uint64(offset)+uint64(Width32) <= uint64(len(d.data))
}

func (d *QualcommADSPMailbox) Read(offset uint32, width Width) (uint32, error) {
	if !d.validAccess(offset, width) {
		return 0, fmt.Errorf(
			"%w: read%d at 0x%x",
			ErrQualcommADSPMailboxMMIO,
			width*8,
			offset,
		)
	}
	return binary.LittleEndian.Uint32(d.data[int(offset) : int(offset)+4]), nil
}

func (d *QualcommADSPMailbox) Write(offset uint32, width Width, value uint32) error {
	if !d.validAccess(offset, width) {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrQualcommADSPMailboxMMIO,
			width*8,
			value,
			offset,
		)
	}
	if offset == d.writeControlOffset {
		value = acknowledgeQualcommADSPWriteControl(value)
	}
	binary.LittleEndian.PutUint32(d.data[int(offset):int(offset)+4], value)
	if err := d.processControlRule(offset, value); err != nil {
		return err
	}
	if offset == d.writeControlOffset &&
		value&qualcommADSPWriteCommandMask == qualcommADSPWriteRequest &&
		value&qualcommADSPWriteMutexMask == 0 {
		if err := d.processHostCommand(); err != nil {
			return err
		}
	}
	return nil
}

func (d *QualcommADSPMailbox) configureControlRules(
	profiles []QualcommADSPControlRuleProfile,
	windows map[string]*LatchedRegisterWindow,
) error {
	return d.configureControlRulesWithInterrupts(profiles, windows, nil, nil)
}

func (d *QualcommADSPMailbox) configureControlRulesWithInterrupts(
	profiles []QualcommADSPControlRuleProfile,
	windows map[string]*LatchedRegisterWindow,
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
) error {
	rules := make(map[qualcommADSPControlKey]qualcommADSPControlRule, len(profiles))
	for _, profile := range profiles {
		if !d.validAccess(profile.Offset, Width32) {
			return fmt.Errorf("invalid Qualcomm ADSP control-rule offset")
		}
		key := qualcommADSPControlKey{offset: profile.Offset, value: profile.Value}
		if _, duplicate := rules[key]; duplicate {
			return fmt.Errorf("duplicate Qualcomm ADSP control rule at 0x%x value 0x%x", profile.Offset, profile.Value)
		}
		writes := make([]qualcommADSPMemoryWrite, 0, len(profile.Writes))
		for _, writeProfile := range profile.Writes {
			window := windows[writeProfile.WindowID]
			if window == nil || !window.validAccess(writeProfile.Offset, writeProfile.Width) ||
				writeProfile.Width < Width32 && writeProfile.Value >= uint32(1)<<(uint32(writeProfile.Width)*8) {
				return fmt.Errorf("invalid Qualcomm ADSP control-rule memory write")
			}
			writes = append(writes, qualcommADSPMemoryWrite{
				window: window,
				offset: writeProfile.Offset,
				width:  writeProfile.Width,
				value:  writeProfile.Value,
			})
		}
		rule := qualcommADSPControlRule{
			delayInstructions: profile.ResponseDelayInstructions,
			writes:            writes,
		}
		if profile.Interrupt != nil {
			var pulser qualcommInterruptSourcePulser
			if profile.Interrupt.UseVectoredController {
				if vectoredInterruptController != nil {
					pulser = vectoredInterruptController
				}
			} else if interruptController != nil {
				pulser = interruptController
			}
			if pulser == nil {
				return fmt.Errorf(
					"Qualcomm ADSP interrupt source %d has no attached interrupt controller",
					profile.Interrupt.Source,
				)
			}
			rule.interrupt = &qualcommADSPInterrupt{
				source: profile.Interrupt.Source,
				pulser: pulser,
			}
		}
		rules[key] = rule
	}
	d.controlRules = rules
	return nil
}

func (d *QualcommADSPMailbox) processControlRule(offset, value uint32) error {
	key := qualcommADSPControlKey{offset: offset, value: value}
	rule, known := d.controlRules[key]
	if !known {
		return nil
	}
	if rule.delayInstructions != 0 {
		if len(d.pendingResponses) >= qualcommADSPMaxPendingResponses {
			return fmt.Errorf("Qualcomm ADSP pending response queue is full")
		}
		d.pendingResponses = append(d.pendingResponses, qualcommADSPPendingResponse{
			key:                   key,
			remainingInstructions: rule.delayInstructions,
		})
		return nil
	}
	return d.applyControlRule(rule)
}

func (d *QualcommADSPMailbox) applyControlRule(rule qualcommADSPControlRule) error {
	for _, operation := range rule.writes {
		if err := operation.window.Write(operation.offset, operation.width, operation.value); err != nil {
			return fmt.Errorf("write Qualcomm ADSP control response: %w", err)
		}
	}
	if rule.interrupt != nil {
		if err := rule.interrupt.pulser.PulseSource(rule.interrupt.source); err != nil {
			return fmt.Errorf("pulse Qualcomm ADSP interrupt source %d: %w", rule.interrupt.source, err)
		}
	}
	return nil
}

// Advance completes at most one delayed response per execution slice. Real
// host/DSP command channels serialize completions through a single event slot;
// limiting delivery to one response prevents multiple queued commands from
// collapsing into one sticky interrupt and gives the host ISR time to unwind.
func (d *QualcommADSPMailbox) Advance(retiredInstructions uint64) error {
	if retiredInstructions == 0 || len(d.pendingResponses) == 0 {
		return nil
	}
	pending := &d.pendingResponses[0]
	if retiredInstructions < pending.remainingInstructions {
		pending.remainingInstructions -= retiredInstructions
		return nil
	}
	key := pending.key
	d.pendingResponses = d.pendingResponses[1:]
	rule, known := d.controlRules[key]
	if !known || rule.delayInstructions == 0 {
		return fmt.Errorf("invalid Qualcomm ADSP pending response")
	}
	return d.applyControlRule(rule)
}

func (d *QualcommADSPMailbox) configureHostCommand(
	profile *QualcommADSPHostCommandProfile,
	windows map[string]*LatchedRegisterWindow,
) error {
	if profile == nil {
		d.hostCommand = nil
		return nil
	}
	selector := windows[profile.SelectorWindowID]
	if selector == nil || !selector.validAccess(profile.SelectorOffset, profile.SelectorWidth) {
		return fmt.Errorf("invalid Qualcomm ADSP host-command selector")
	}
	channel := &qualcommADSPHostCommand{
		selector: selector,
		offset:   profile.SelectorOffset,
		width:    profile.SelectorWidth,
		rules:    make(map[uint32][]qualcommADSPMemoryCopy, len(profile.Rules)),
	}
	for _, rule := range profile.Rules {
		if rule.Command == 0 ||
			profile.SelectorWidth < Width32 && rule.Command >= uint32(1)<<(uint32(profile.SelectorWidth)*8) {
			return fmt.Errorf("invalid Qualcomm ADSP host command 0x%x", rule.Command)
		}
		if _, duplicate := channel.rules[rule.Command]; duplicate {
			return fmt.Errorf("duplicate Qualcomm ADSP host command 0x%x", rule.Command)
		}
		copies := make([]qualcommADSPMemoryCopy, 0, len(rule.Copies))
		for _, copyProfile := range rule.Copies {
			source := windows[copyProfile.SourceWindowID]
			destination := windows[copyProfile.DestinationWindowID]
			if source == nil || destination == nil ||
				!source.validAccess(copyProfile.SourceOffset, copyProfile.Width) ||
				!destination.validAccess(copyProfile.DestinationOffset, copyProfile.Width) {
				return fmt.Errorf("invalid Qualcomm ADSP host-command memory copy")
			}
			copies = append(copies, qualcommADSPMemoryCopy{
				source:            source,
				sourceOffset:      copyProfile.SourceOffset,
				destination:       destination,
				destinationOffset: copyProfile.DestinationOffset,
				width:             copyProfile.Width,
			})
		}
		channel.rules[rule.Command] = copies
	}
	d.hostCommand = channel
	return nil
}

func (d *QualcommADSPMailbox) processHostCommand() error {
	if d.hostCommand == nil {
		return nil
	}
	command, err := d.hostCommand.selector.Read(d.hostCommand.offset, d.hostCommand.width)
	if err != nil {
		return fmt.Errorf("read Qualcomm ADSP host command: %w", err)
	}
	copies, known := d.hostCommand.rules[command]
	if !known {
		return nil
	}
	for _, operation := range copies {
		value, readErr := operation.source.Read(operation.sourceOffset, operation.width)
		if readErr != nil {
			return fmt.Errorf("read Qualcomm ADSP command payload: %w", readErr)
		}
		if writeErr := operation.destination.Write(
			operation.destinationOffset,
			operation.width,
			value,
		); writeErr != nil {
			return fmt.Errorf("write Qualcomm ADSP command response: %w", writeErr)
		}
	}
	if err := d.hostCommand.selector.Write(d.hostCommand.offset, d.hostCommand.width, 0); err != nil {
		return fmt.Errorf("acknowledge Qualcomm ADSP host command: %w", err)
	}
	return nil
}

func acknowledgeQualcommADSPWriteControl(value uint32) uint32 {
	if value&qualcommADSPWriteMutexMask == 0 {
		return value
	}
	switch value & qualcommADSPWriteCommandMask {
	case qualcommADSPWriteRequest:
		return value &^ qualcommADSPWriteMutexMask
	case qualcommADSPWriteDone:
		return qualcommADSPWriteReady
	default:
		return value
	}
}

func (d *QualcommADSPMailbox) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("QAMB")
	_ = binary.Write(&output, binary.LittleEndian, qualcommADSPMailboxStateVersion)
	_ = binary.Write(&output, binary.LittleEndian, d.writeControlOffset)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.data)))
	output.Write(d.data)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.pendingResponses)))
	for _, pending := range d.pendingResponses {
		_ = binary.Write(&output, binary.LittleEndian, pending.key.offset)
		_ = binary.Write(&output, binary.LittleEndian, pending.key.value)
		_ = binary.Write(&output, binary.LittleEndian, pending.remainingInstructions)
	}
	return output.Bytes(), nil
}

func (d *QualcommADSPMailbox) LoadState(state []byte) error {
	if len(state) < 20 || string(state[:4]) != "QAMB" ||
		binary.LittleEndian.Uint32(state[4:8]) != qualcommADSPMailboxStateVersion ||
		binary.LittleEndian.Uint32(state[8:12]) != d.writeControlOffset ||
		binary.LittleEndian.Uint32(state[12:16]) != uint32(len(d.data)) {
		return ErrInvalidState
	}
	dataEnd := 16 + len(d.data)
	if dataEnd+4 > len(state) {
		return ErrInvalidState
	}
	count := binary.LittleEndian.Uint32(state[dataEnd : dataEnd+4])
	if count > qualcommADSPMaxPendingResponses ||
		uint64(dataEnd)+4+uint64(count)*16 != uint64(len(state)) {
		return ErrInvalidState
	}
	pendingResponses := make([]qualcommADSPPendingResponse, 0, count)
	offset := dataEnd + 4
	for index := uint32(0); index < count; index++ {
		key := qualcommADSPControlKey{
			offset: binary.LittleEndian.Uint32(state[offset : offset+4]),
			value:  binary.LittleEndian.Uint32(state[offset+4 : offset+8]),
		}
		remaining := binary.LittleEndian.Uint64(state[offset+8 : offset+16])
		rule, known := d.controlRules[key]
		if !known || rule.delayInstructions == 0 || remaining == 0 ||
			remaining > rule.delayInstructions {
			return ErrInvalidState
		}
		pendingResponses = append(pendingResponses, qualcommADSPPendingResponse{
			key: key, remainingInstructions: remaining,
		})
		offset += 16
	}
	copy(d.data, state[16:dataEnd])
	d.pendingResponses = pendingResponses
	return nil
}

// LoadStateSubset accepts the former generic 32-bit latch representation used
// by diagnostic checkpoints. A pending control word is acknowledged while it
// is migrated, as the real DSP would have done after the snapshot was taken.
func (d *QualcommADSPMailbox) LoadStateSubset(state []byte) error {
	if len(state) >= 4 && string(state[:4]) == "QAMB" {
		if len(state) == 16+len(d.data) &&
			binary.LittleEndian.Uint32(state[4:8]) == qualcommADSPMailboxLegacyState &&
			binary.LittleEndian.Uint32(state[8:12]) == d.writeControlOffset &&
			binary.LittleEndian.Uint32(state[12:16]) == uint32(len(d.data)) {
			copy(d.data, state[16:])
			d.pendingResponses = nil
			return nil
		}
		return d.LoadState(state)
	}
	if len(state) < 16 || string(state[:4]) != "LRWN" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || Width(state[8]) != Width32 ||
		state[9] != 0 || state[10] != 0 || state[11] != 0 ||
		binary.LittleEndian.Uint32(state[12:16]) != uint32(len(d.data)) ||
		len(state) != 16+len(d.data) {
		return ErrInvalidState
	}
	copy(d.data, state[16:])
	d.pendingResponses = nil
	control := binary.LittleEndian.Uint32(
		d.data[int(d.writeControlOffset) : int(d.writeControlOffset)+4],
	)
	binary.LittleEndian.PutUint32(
		d.data[int(d.writeControlOffset):int(d.writeControlOffset)+4],
		acknowledgeQualcommADSPWriteControl(control),
	)
	if control&qualcommADSPWriteMutexMask != 0 &&
		control&qualcommADSPWriteCommandMask == qualcommADSPWriteRequest {
		if err := d.processHostCommand(); err != nil {
			return err
		}
	}
	return nil
}

var (
	_ Device               = (*QualcommADSPMailbox)(nil)
	_ ClockedDevice        = (*QualcommADSPMailbox)(nil)
	_ StatefulDevice       = (*QualcommADSPMailbox)(nil)
	_ SubsetStatefulDevice = (*QualcommADSPMailbox)(nil)
)
