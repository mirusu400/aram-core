package gnex32

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"reflect"
	"sort"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	gnex32RuntimeStateMagic   = "GNX32RT\x00"
	gnex32RuntimeStateVersion = uint32(3)
	// Shared state may itself contain the configured 768 MiB maximum. GNEX
	// symbol/media memory and the three profile-sized LCD buffers need separate room.
	MaxRuntimeStateBytes = uint64(850 << 20)
	maxSavedFaultBytes   = uint32(16 << 10)
)

type vmRefState struct {
	Slot, Symbol, Element int
}

type vmState struct {
	PC      int
	Calls   []int
	Marks   []int
	Stack   []uint32
	Refs    []vmRefState
	Halted  bool
	Fault   string
	Symbols gnex.GNEX32SymbolMemoryState
	Media   gnex.GNEX32MediaMemoryState
}

type displayState struct {
	Width, Height int
	Clip          image.Rectangle
	Gamma         uint8
	Working       []byte
	Latest, Saved []byte
	HasLatest     bool
	HasSaved      bool
}

type runtimeState struct {
	VM             vmState
	Display        displayState
	Services       []byte
	SoundClip      shared.ServiceID
	KeyToneClip    shared.ServiceID
	KeyToneOn      bool
	Backlight      BacklightMode
	Tick           uint64
	Timers         [3]runtimeTimer
	Presents       uint64
	NV             [UserNVWords]uint32
	RandomState    uint32
	Paint          color.RGBA
	PaintSource    color.RGBA
	PaintIndex     uint8
	PaintMapped    bool
	FontSize       uint32
	FontColor      color.RGBA
	FontIndex      uint8
	FontAlign      uint32
	FontBack       color.RGBA
	FontBackIdx    uint8
	CloseRequested bool
}

func (vm *VM) snapshotState() vmState {
	state := vmState{
		PC: vm.pc, Calls: append([]int(nil), vm.calls...),
		Marks: append([]int(nil), vm.marks...), Stack: append([]uint32(nil), vm.stack...),
		Halted: vm.halted, Symbols: vm.memory.Snapshot(), Media: vm.media.Snapshot(),
	}
	if vm.fault != nil {
		state.Fault = vm.fault.Error()
	}
	state.Refs = make([]vmRefState, 0, len(vm.refs))
	for slot, ref := range vm.refs {
		state.Refs = append(state.Refs, vmRefState{Slot: slot, Symbol: ref.symbol, Element: ref.element})
	}
	sort.Slice(state.Refs, func(i, j int) bool { return state.Refs[i].Slot < state.Refs[j].Slot })
	return state
}

func (vm *VM) restoreState(state vmState) error {
	if vm == nil || vm.memory == nil || vm.media == nil {
		return fmt.Errorf("gnex32: VM state target is incomplete")
	}
	if state.PC < 0 || state.PC > len(vm.image.Code) || state.PC&1 != 0 ||
		(!state.Halted && state.Fault == "" && state.PC == len(vm.image.Code)) {
		return fmt.Errorf("gnex32: invalid saved PC %d", state.PC)
	}
	if len(state.Calls) > 1024 || len(state.Marks) > 1024 || len(state.Stack) > 4096 ||
		len(state.Refs) > len(state.Stack) || len(state.Fault) > int(maxSavedFaultBytes) {
		return fmt.Errorf("gnex32: saved VM stack or fault exceeds limit")
	}
	for _, target := range state.Calls {
		if target < 0 || target > len(vm.image.Code) || target&1 != 0 {
			return fmt.Errorf("gnex32: invalid saved return address %d", target)
		}
	}
	for _, mark := range state.Marks {
		if mark < 0 || mark > len(state.Stack) {
			return fmt.Errorf("gnex32: invalid saved stack mark %d", mark)
		}
	}
	refs := make(map[int]gnex32SymbolRef, len(state.Refs))
	for _, saved := range state.Refs {
		if saved.Slot < 0 || saved.Slot >= len(state.Stack) {
			return fmt.Errorf("gnex32: invalid saved lvalue slot %d", saved.Slot)
		}
		if _, duplicate := refs[saved.Slot]; duplicate {
			return fmt.Errorf("gnex32: duplicate saved lvalue slot %d", saved.Slot)
		}
		if _, err := vm.memory.ReadWord(saved.Symbol, saved.Element); err != nil {
			return fmt.Errorf("gnex32: invalid saved lvalue: %w", err)
		}
		refs[saved.Slot] = gnex32SymbolRef{symbol: saved.Symbol, element: saved.Element}
	}
	if err := vm.memory.Restore(state.Symbols); err != nil {
		return err
	}
	if err := vm.media.Restore(state.Media); err != nil {
		return err
	}
	vm.pc = state.PC
	vm.calls = append(vm.calls[:0], state.Calls...)
	vm.marks = append(vm.marks[:0], state.Marks...)
	vm.stack = append(vm.stack[:0], state.Stack...)
	vm.refs = refs
	vm.halted = state.Halted
	vm.fault = nil
	if state.Fault != "" {
		vm.fault = errors.New(state.Fault)
	}
	return nil
}

func (d *Display) snapshotState() displayState {
	state := displayState{
		Width: d.working.Bounds().Dx(), Height: d.working.Bounds().Dy(),
		Clip: d.clip, Gamma: d.gamma, Working: bytes.Clone(d.working.Pix),
		HasLatest: d.latest != nil, HasSaved: d.saved != nil,
	}
	if d.latest != nil {
		state.Latest = bytes.Clone(d.latest.Pix)
	}
	if d.saved != nil {
		state.Saved = bytes.Clone(d.saved.Pix)
	}
	return state
}

func (d *Display) restoreState(state displayState) error {
	if d == nil || d.working == nil || state.Width != d.working.Bounds().Dx() ||
		state.Height != d.working.Bounds().Dy() || state.Width <= 0 || state.Height <= 0 {
		return fmt.Errorf("gnex32: display state dimensions mismatch")
	}
	bounds := d.working.Bounds()
	if state.Gamma > 6 || state.Clip.Min.X < bounds.Min.X || state.Clip.Min.Y < bounds.Min.Y ||
		state.Clip.Max.X > bounds.Max.X || state.Clip.Max.Y > bounds.Max.Y ||
		state.Clip.Min.X > state.Clip.Max.X || state.Clip.Min.Y > state.Clip.Max.Y {
		return fmt.Errorf("gnex32: invalid display state")
	}
	want := len(d.working.Pix)
	if len(state.Working) != want || state.HasLatest != (len(state.Latest) != 0) ||
		state.HasSaved != (len(state.Saved) != 0) ||
		(state.HasLatest && len(state.Latest) != want) || (state.HasSaved && len(state.Saved) != want) {
		return fmt.Errorf("gnex32: invalid display pixel state")
	}
	working := image.NewRGBA(bounds)
	copy(working.Pix, state.Working)
	var latest, saved *image.RGBA
	if state.HasLatest {
		latest = image.NewRGBA(bounds)
		copy(latest.Pix, state.Latest)
	}
	if state.HasSaved {
		saved = image.NewRGBA(bounds)
		copy(saved.Pix, state.Saved)
	}
	d.working, d.latest, d.saved = working, latest, saved
	d.clip, d.gamma = state.Clip, state.Gamma
	return nil
}

func (r *Runtime) snapshotState() (runtimeState, error) {
	services, err := r.services.MarshalBinary()
	if err != nil {
		return runtimeState{}, err
	}
	return runtimeState{
		VM: r.vm.snapshotState(), Display: r.display.snapshotState(), Services: services,
		SoundClip: r.soundClip, KeyToneClip: r.keyToneClip,
		KeyToneOn: r.keyToneOn, Backlight: r.backlight,
		Tick: r.tick, Timers: r.timers, Presents: r.presents, NV: r.nv,
		RandomState: r.randomState,
		Paint:       r.paint, PaintSource: r.paintSource, PaintIndex: r.paintIndex, PaintMapped: r.paintMapped,
		FontSize: r.fontSize, FontColor: r.fontColor, FontIndex: r.fontIndex,
		FontAlign: r.fontAlign, FontBack: r.fontBack, FontBackIdx: r.fontBackIdx,
		CloseRequested: r.closeRequested,
	}, nil
}

// MarshalBinary emits a versioned, checksummed, source-independent runtime
// snapshot. The application container binds it to an exact source SHA-256.
func (r *Runtime) MarshalBinary() ([]byte, error) {
	if r == nil || r.closed {
		return nil, fmt.Errorf("gnex32: cannot save a closed runtime")
	}
	if err := r.validateRestoredState(); err != nil {
		return nil, fmt.Errorf("gnex32: validate runtime before save: %w", err)
	}
	state, err := r.snapshotState()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.WriteString(gnex32RuntimeStateMagic)
	writeStateU32(&output, gnex32RuntimeStateVersion)
	encodeVMState(&output, state.VM)
	encodeDisplayState(&output, state.Display)
	writeStateU64(&output, uint64(state.SoundClip))
	writeStateU64(&output, uint64(state.KeyToneClip))
	writeStateBool(&output, state.KeyToneOn)
	output.WriteByte(byte(state.Backlight))
	writeStateBool(&output, state.CloseRequested)
	output.WriteByte(0)
	writeStateU64(&output, state.Tick)
	for _, timer := range state.Timers {
		writeStateU64(&output, timer.due)
		writeStateU32(&output, timer.period)
		writeStateBool(&output, timer.repeat)
		writeStateBool(&output, timer.active)
		output.Write([]byte{0, 0})
	}
	writeStateU64(&output, state.Presents)
	for _, value := range state.NV {
		writeStateU32(&output, value)
	}
	writeStateU32(&output, state.RandomState)
	writeStateColor(&output, state.Paint)
	writeStateColor(&output, state.PaintSource)
	output.WriteByte(state.PaintIndex)
	writeStateBool(&output, state.PaintMapped)
	output.Write([]byte{0, 0})
	writeStateU32(&output, state.FontSize)
	writeStateColor(&output, state.FontColor)
	output.WriteByte(state.FontIndex)
	output.Write([]byte{0, 0, 0})
	writeStateU32(&output, state.FontAlign)
	writeStateColor(&output, state.FontBack)
	output.WriteByte(state.FontBackIdx)
	output.Write([]byte{0, 0, 0})
	writeStateU64(&output, uint64(len(state.Services)))
	output.Write(state.Services)
	digest := sha256.Sum256(output.Bytes())
	output.Write(digest[:])
	if uint64(output.Len()) > MaxRuntimeStateBytes {
		return nil, fmt.Errorf("gnex32: runtime state exceeds byte limit")
	}
	return output.Bytes(), nil
}

// UnmarshalBinary validates on an isolated runtime before replacing live state.
func (r *Runtime) UnmarshalBinary(data []byte) error {
	if r == nil || r.closed {
		return fmt.Errorf("gnex32: cannot load a closed runtime")
	}
	state, err := decodeRuntimeState(data, r)
	if err != nil {
		return err
	}
	candidate, err := NewRuntime(r.vm.image, r.profile)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = candidate.Close()
		}
	}()
	if err := candidate.vm.restoreState(state.VM); err != nil {
		return err
	}
	if err := candidate.display.restoreState(state.Display); err != nil {
		return err
	}
	serviceState, err := shared.DecodeServicesState(state.Services)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(serviceState.Config, candidate.services.Config) {
		return fmt.Errorf("gnex32: saved shared-service configuration mismatch")
	}
	if err := candidate.services.Restore(serviceState); err != nil {
		return err
	}
	candidate.text = candidate.services.Text
	candidate.soundClip, candidate.keyToneClip = state.SoundClip, state.KeyToneClip
	candidate.keyToneOn, candidate.backlight = state.KeyToneOn, state.Backlight
	candidate.tick, candidate.timers, candidate.presents = state.Tick, state.Timers, state.Presents
	candidate.nv, candidate.randomState = state.NV, state.RandomState
	candidate.paint, candidate.paintSource = state.Paint, state.PaintSource
	candidate.paintIndex, candidate.paintMapped = state.PaintIndex, state.PaintMapped
	candidate.fontSize, candidate.fontColor, candidate.fontIndex = state.FontSize, state.FontColor, state.FontIndex
	candidate.fontAlign, candidate.fontBack, candidate.fontBackIdx = state.FontAlign, state.FontBack, state.FontBackIdx
	candidate.closeRequested = state.CloseRequested
	if err := candidate.validateRestoredState(); err != nil {
		return err
	}
	*r = *candidate
	r.vm.service = r.service
	r.vm.copyImage = r.copyImage
	r.vm.copyImageDir = r.copyImageDir
	r.vm.copyImageEx = r.copyImageEx
	committed = true
	return nil
}

func (r *Runtime) validateRestoredState() error {
	if r.backlight > BacklightMMI || r.fontSize >= uint32(len(gnex32FontHeights)) ||
		r.paintIndex > 181 || r.fontIndex > 181 || r.fontBackIdx > 181 {
		return fmt.Errorf("gnex32: invalid saved drawing or device state")
	}
	enabled, _ := r.services.Device.Backlight()
	if enabled != r.backlightEnabled(r.backlight) {
		return fmt.Errorf("gnex32: saved backlight policy and device state disagree")
	}
	if _, err := r.services.Coordinator.Adapter(r.owner); err != nil {
		return fmt.Errorf("gnex32: saved runtime owner is missing: %w", err)
	}
	if r.soundClip != 0 && r.soundClip == r.keyToneClip {
		return fmt.Errorf("gnex32: saved sound and key tone share one clip")
	}
	for name, clip := range map[string]shared.ServiceID{"sound": r.soundClip, "key tone": r.keyToneClip} {
		if clip == 0 {
			continue
		}
		info, err := r.services.Media.Info(r.owner, clip)
		if err != nil || info.Owner != r.owner {
			return fmt.Errorf("gnex32: saved %s clip is invalid", name)
		}
	}
	return nil
}

func encodeVMState(output *bytes.Buffer, state vmState) {
	writeStateU32(output, uint32(state.PC))
	writeStateBool(output, state.Halted)
	output.Write([]byte{0, 0, 0})
	writeStateString(output, state.Fault)
	writeStateInts(output, state.Calls)
	writeStateInts(output, state.Marks)
	writeStateU32(output, uint32(len(state.Stack)))
	for _, value := range state.Stack {
		writeStateU32(output, value)
	}
	writeStateU32(output, uint32(len(state.Refs)))
	for _, ref := range state.Refs {
		writeStateU32(output, uint32(ref.Slot))
		writeStateU32(output, uint32(ref.Symbol))
		writeStateU32(output, uint32(ref.Element))
	}
	writeStateU32(output, uint32(len(state.Symbols.Symbols)))
	for _, data := range state.Symbols.Symbols {
		writeStateU32(output, uint32(len(data)))
		output.Write(data)
	}
	writeStateU32(output, uint32(len(state.Media.Resources)))
	for _, resource := range state.Media.Resources {
		writeStateU32(output, resource.Flags)
		writeStateU64(output, uint64(len(resource.Data)))
		output.Write(resource.Data)
		writeStateU32(output, uint32(len(resource.PaletteOverrides)))
		for _, override := range resource.PaletteOverrides {
			output.WriteByte(override.Index)
			output.WriteByte(override.Red)
			output.WriteByte(override.Green)
			output.WriteByte(override.Blue)
		}
	}
}

func encodeDisplayState(output *bytes.Buffer, state displayState) {
	writeStateU32(output, uint32(state.Width))
	writeStateU32(output, uint32(state.Height))
	writeStateI32(output, int32(state.Clip.Min.X))
	writeStateI32(output, int32(state.Clip.Min.Y))
	writeStateI32(output, int32(state.Clip.Max.X))
	writeStateI32(output, int32(state.Clip.Max.Y))
	output.WriteByte(state.Gamma)
	writeStateBool(output, state.HasLatest)
	writeStateBool(output, state.HasSaved)
	output.WriteByte(0)
	output.Write(state.Working)
	if state.HasLatest {
		output.Write(state.Latest)
	}
	if state.HasSaved {
		output.Write(state.Saved)
	}
}

func decodeRuntimeState(data []byte, template *Runtime) (runtimeState, error) {
	if uint64(len(data)) > MaxRuntimeStateBytes {
		return runtimeState{}, fmt.Errorf("gnex32: runtime state exceeds byte limit")
	}
	minimum := len(gnex32RuntimeStateMagic) + 4 + sha256.Size
	if len(data) < minimum {
		return runtimeState{}, fmt.Errorf("gnex32: truncated runtime state")
	}
	payload := data[:len(data)-sha256.Size]
	expected := data[len(payload):]
	actual := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return runtimeState{}, fmt.Errorf("gnex32: runtime state checksum mismatch")
	}
	decoder := stateDecoder{reader: bytes.NewReader(payload)}
	if magic := decoder.bytes(len(gnex32RuntimeStateMagic)); string(magic) != gnex32RuntimeStateMagic {
		return runtimeState{}, decoder.fail("magic mismatch")
	}
	if version := decoder.u32(); version != gnex32RuntimeStateVersion {
		return runtimeState{}, decoder.fail(fmt.Sprintf("unsupported version %d", version))
	}
	state := runtimeState{}
	state.VM = decoder.vm(template.vm)
	state.Display = decoder.display(template.display)
	state.SoundClip = shared.ServiceID(decoder.u64())
	state.KeyToneClip = shared.ServiceID(decoder.u64())
	state.KeyToneOn = decoder.boolean()
	state.Backlight = BacklightMode(decoder.u8())
	state.CloseRequested = decoder.boolean()
	decoder.reserved(1)
	state.Tick = decoder.u64()
	for index := range state.Timers {
		state.Timers[index] = runtimeTimer{
			due: decoder.u64(), period: decoder.u32(),
			repeat: decoder.boolean(), active: decoder.boolean(),
		}
		decoder.reserved(2)
	}
	state.Presents = decoder.u64()
	for index := range state.NV {
		state.NV[index] = decoder.u32()
	}
	state.RandomState = decoder.u32()
	state.Paint = decoder.rgba()
	state.PaintSource = decoder.rgba()
	state.PaintIndex = decoder.u8()
	state.PaintMapped = decoder.boolean()
	decoder.reserved(2)
	state.FontSize = decoder.u32()
	state.FontColor = decoder.rgba()
	state.FontIndex = decoder.u8()
	decoder.reserved(3)
	state.FontAlign = decoder.u32()
	state.FontBack = decoder.rgba()
	state.FontBackIdx = decoder.u8()
	decoder.reserved(3)
	serviceSize := decoder.u64()
	if serviceSize > shared.MaxServicesStateBytes || serviceSize > uint64(decoder.reader.Len()) {
		decoder.err = decoder.fail("invalid shared-service state length")
	} else {
		state.Services = decoder.bytes(int(serviceSize))
	}
	if decoder.err != nil {
		return runtimeState{}, decoder.err
	}
	if decoder.reader.Len() != 0 {
		return runtimeState{}, decoder.fail(fmt.Sprintf("%d trailing bytes", decoder.reader.Len()))
	}
	return state, nil
}

type stateDecoder struct {
	reader *bytes.Reader
	offset uint64
	err    error
}

func (d *stateDecoder) bytes(size int) []byte {
	if d.err != nil || size < 0 || size > d.reader.Len() {
		if d.err == nil {
			d.err = d.fail("truncated data")
		}
		return nil
	}
	result := make([]byte, size)
	if _, err := io.ReadFull(d.reader, result); err != nil {
		d.err = d.fail(err.Error())
		return nil
	}
	d.offset += uint64(size)
	return result
}

func (d *stateDecoder) u8() uint8 {
	data := d.bytes(1)
	if len(data) == 1 {
		return data[0]
	}
	return 0
}

func (d *stateDecoder) boolean() bool {
	value := d.u8()
	if value > 1 && d.err == nil {
		d.err = d.fail("invalid boolean")
	}
	return value != 0
}

func (d *stateDecoder) u32() uint32 {
	data := d.bytes(4)
	if len(data) == 4 {
		return binary.LittleEndian.Uint32(data)
	}
	return 0
}

func (d *stateDecoder) i32() int32 { return int32(d.u32()) }

func (d *stateDecoder) u64() uint64 {
	data := d.bytes(8)
	if len(data) == 8 {
		return binary.LittleEndian.Uint64(data)
	}
	return 0
}

func (d *stateDecoder) reserved(size int) {
	for _, value := range d.bytes(size) {
		if value != 0 && d.err == nil {
			d.err = d.fail("nonzero reserved field")
		}
	}
}

func (d *stateDecoder) string(limit uint32) string {
	size := d.u32()
	if size > limit || uint64(size) > uint64(d.reader.Len()) {
		d.err = d.fail("invalid string length")
		return ""
	}
	return string(d.bytes(int(size)))
}

func (d *stateDecoder) ints(limit uint32) []int {
	count := d.u32()
	if count > limit || uint64(count)*4 > uint64(d.reader.Len()) {
		d.err = d.fail("invalid integer array length")
		return nil
	}
	result := make([]int, count)
	for index := range result {
		result[index] = int(d.u32())
	}
	return result
}

func (d *stateDecoder) vm(template *VM) vmState {
	state := vmState{PC: int(d.u32()), Halted: d.boolean()}
	d.reserved(3)
	state.Fault = d.string(maxSavedFaultBytes)
	state.Calls = d.ints(1024)
	state.Marks = d.ints(1024)
	stackCount := d.u32()
	if stackCount > 4096 || uint64(stackCount)*4 > uint64(d.reader.Len()) {
		d.err = d.fail("invalid value stack length")
		return state
	}
	state.Stack = make([]uint32, stackCount)
	for index := range state.Stack {
		state.Stack[index] = d.u32()
	}
	refCount := d.u32()
	if refCount > stackCount || uint64(refCount)*12 > uint64(d.reader.Len()) {
		d.err = d.fail("invalid lvalue reference count")
		return state
	}
	state.Refs = make([]vmRefState, refCount)
	for index := range state.Refs {
		state.Refs[index] = vmRefState{Slot: int(d.u32()), Symbol: int(d.u32()), Element: int(d.u32())}
	}
	expectedSymbols := template.memory.Snapshot().Symbols
	symbolCount := d.u32()
	if int(symbolCount) != len(expectedSymbols) {
		d.err = d.fail("symbol count mismatch")
		return state
	}
	state.Symbols.Symbols = make([][]byte, symbolCount)
	for index := range state.Symbols.Symbols {
		size := d.u32()
		if int(size) != len(expectedSymbols[index]) || uint64(size) > uint64(d.reader.Len()) {
			d.err = d.fail("symbol length mismatch")
			return state
		}
		state.Symbols.Symbols[index] = d.bytes(int(size))
	}
	expectedMedia := template.media.Snapshot().Resources
	mediaCount := d.u32()
	if int(mediaCount) != len(expectedMedia) {
		d.err = d.fail("media count mismatch")
		return state
	}
	state.Media.Resources = make([]gnex.GNEX32MediaResourceState, mediaCount)
	var total uint64
	for index := range state.Media.Resources {
		flags, size := d.u32(), d.u64()
		total += size
		if flags != expectedMedia[index].Flags || size > uint64(d.reader.Len()) || total > gnex.MaxMemberSize {
			d.err = d.fail("invalid media resource state")
			return state
		}
		resource := gnex.GNEX32MediaResourceState{Flags: flags, Data: d.bytes(int(size))}
		overrideCount := d.u32()
		if overrideCount > 16 || uint64(overrideCount)*4 > uint64(d.reader.Len()) {
			d.err = d.fail("invalid media palette override count")
			return state
		}
		resource.PaletteOverrides = make([]gnex.GNEX32PaletteRGB, overrideCount)
		for overrideIndex := range resource.PaletteOverrides {
			data := d.bytes(4)
			if len(data) == 4 {
				resource.PaletteOverrides[overrideIndex] = gnex.GNEX32PaletteRGB{
					Index: data[0], Red: data[1], Green: data[2], Blue: data[3],
				}
			}
		}
		state.Media.Resources[index] = resource
	}
	return state
}

func (d *stateDecoder) display(template *Display) displayState {
	width, height := int(d.u32()), int(d.u32())
	minX, minY, maxX, maxY := int(d.i32()), int(d.i32()), int(d.i32()), int(d.i32())
	state := displayState{
		Width: width, Height: height,
		Clip:  image.Rectangle{Min: image.Pt(minX, minY), Max: image.Pt(maxX, maxY)},
		Gamma: d.u8(), HasLatest: d.boolean(), HasSaved: d.boolean(),
	}
	d.reserved(1)
	want := len(template.working.Pix)
	if state.Width != template.working.Bounds().Dx() || state.Height != template.working.Bounds().Dy() {
		d.err = d.fail("display dimensions mismatch")
		return state
	}
	state.Working = d.bytes(want)
	if state.HasLatest {
		state.Latest = d.bytes(want)
	}
	if state.HasSaved {
		state.Saved = d.bytes(want)
	}
	return state
}

func (d *stateDecoder) rgba() color.RGBA {
	data := d.bytes(4)
	if len(data) != 4 {
		return color.RGBA{}
	}
	return color.RGBA{R: data[0], G: data[1], B: data[2], A: data[3]}
}

func (d *stateDecoder) fail(reason string) error {
	return fmt.Errorf("gnex32: load runtime state at offset 0x%x: %s", d.offset, reason)
}

func writeStateU32(output *bytes.Buffer, value uint32) {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	output.Write(data[:])
}

func writeStateI32(output *bytes.Buffer, value int32) { writeStateU32(output, uint32(value)) }

func writeStateU64(output *bytes.Buffer, value uint64) {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	output.Write(data[:])
}

func writeStateBool(output *bytes.Buffer, value bool) {
	if value {
		output.WriteByte(1)
	} else {
		output.WriteByte(0)
	}
}

func writeStateString(output *bytes.Buffer, value string) {
	writeStateU32(output, uint32(len(value)))
	output.WriteString(value)
}

func writeStateInts(output *bytes.Buffer, values []int) {
	writeStateU32(output, uint32(len(values)))
	for _, value := range values {
		writeStateU32(output, uint32(value))
	}
}

func writeStateColor(output *bytes.Buffer, value color.RGBA) {
	output.Write([]byte{value.R, value.G, value.B, value.A})
}
