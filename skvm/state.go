package skvm

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"github.com/mirusu400/aram-core/internal/ime"
	"io"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	vmStateMagic       = "ARAMSKV\x00"
	vmStateVersion     = uint32(4)
	maxVMStateBytes    = uint64(1024 << 20)
	maxVMMetadataBytes = uint64(512 << 20)
	maxVMHeapObjects   = uint32(4_000_000)
	maxVMFrames        = uint32(65_536)
)

type valueState struct {
	Kind ValueKind
	Bits uint64
}

type namedValueState struct {
	Name  string
	Value valueState
}

type classRuntimeState struct {
	Name      string
	InitState classInitState
	Static    []namedValueState
}

type arrayState struct {
	Descriptor string
	Elements   []valueState
}

type nativeMapEntry struct {
	Key       string
	Reference uint32
}

type nativeState struct {
	Kind       string
	Text       string
	Data       []byte
	Offset     int64
	Flag       bool
	Reference  uint32
	References []uint32
	Service    shared.ServiceID
	Service2   shared.ServiceID
	Services   []shared.ServiceID
	Width      int32
	Height     int32
	Color      uint32
	Integer    int32
	Long       int64
	Map        []nativeMapEntry
	Map2       []nativeMapEntry
}

type objectState struct {
	Reference uint32
	Class     string
	Fields    []namedValueState
	Array     *arrayState
	Native    nativeState
}

type frameState struct {
	Class      string
	Method     string
	Descriptor string
	Locals     []valueState
	Stack      []valueState
	PC         int32
	InvokePC   int32
}

type threadContinuationState struct {
	Reference uint32
	Frames    []frameState
}

type propertyState struct {
	Name  string
	Value string
}

type vmMetadataState struct {
	Schema              uint32
	ClassDigest         [sha256.Size]byte
	Classes             []classRuntimeState
	Heap                []objectState
	NextReference       uint32
	HostStatic          []namedValueState
	Frames              []frameState
	ThreadContinuations []threadContinuationState
	InstructionLimit    uint64
	Instructions        uint64
	ScreenWidth         int32
	ScreenHeight        int32
	DisplayReference    uint32
	CurrentDisplay      uint32
	Properties          []propertyState
	ScreenGraphics      uint32
	ServiceOwner        shared.OwnerID
	ScreenSurface       shared.ServiceID
	DefaultFont         shared.ServiceID
}

type nativeLink struct {
	alias uint32
	file  uint32
}

// MarshalBinary saves the execution engine, adapter mappings, and every
// shared semantic service. The trace hook is intentionally not saved.
func (vm *VM) MarshalBinary() ([]byte, error) {
	if vm == nil || vm.services == nil {
		return nil, fmt.Errorf("save SKVM state: VM is not initialized")
	}
	if vm.executionFrameCount() > int(maxVMFrames) ||
		len(vm.heap) > int(maxVMHeapObjects) {
		return nil, fmt.Errorf("save SKVM state: object or frame limit exceeded")
	}
	if err := vm.validateReferences(); err != nil {
		return nil, fmt.Errorf("save SKVM state: %w", err)
	}
	metadata, err := vm.snapshotMetadata()
	if err != nil {
		return nil, err
	}
	metadataPayload, err := shared.MarshalStateComponent(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode SKVM metadata: %w", err)
	}
	if uint64(len(metadataPayload)) > maxVMMetadataBytes {
		return nil, fmt.Errorf("save SKVM state: metadata exceeds byte limit")
	}
	servicePayload, err := vm.services.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("encode SKVM services: %w", err)
	}
	if uint64(len(metadataPayload))+uint64(len(servicePayload)) >
		maxVMStateBytes-128 {
		return nil, fmt.Errorf("save SKVM state: state exceeds byte limit")
	}

	var output bytes.Buffer
	output.WriteString(vmStateMagic)
	writeVMU32(&output, vmStateVersion)
	writeVMU64(&output, uint64(len(metadataPayload)))
	output.Write(metadataPayload)
	metadataDigest := sha256.Sum256(metadataPayload)
	output.Write(metadataDigest[:])
	writeVMU64(&output, uint64(len(servicePayload)))
	output.Write(servicePayload)
	serviceDigest := sha256.Sum256(servicePayload)
	output.Write(serviceDigest[:])
	digest := sha256.Sum256(output.Bytes())
	output.Write(digest[:])
	return output.Bytes(), nil
}

// UnmarshalBinary validates a complete candidate VM and service graph before
// committing either one to the live interpreter.
func (vm *VM) UnmarshalBinary(data []byte) error {
	if vm == nil || vm.services == nil {
		return fmt.Errorf("load SKVM state: VM is not initialized")
	}
	metadata, serviceState, err := decodeVMState(data)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(serviceState.Config, vm.services.Config) {
		return fmt.Errorf("load SKVM state: shared service configuration mismatch")
	}
	candidateServices, err := shared.NewServices(serviceState.Config)
	if err != nil {
		return fmt.Errorf("load SKVM state: create candidate services: %w", err)
	}
	if err := candidateServices.Restore(serviceState); err != nil {
		return fmt.Errorf("load SKVM state: restore candidate services: %w", err)
	}
	candidate, err := vm.buildCandidate(metadata, candidateServices)
	if err != nil {
		return err
	}

	// Restore is itself validation-before-mutation. It cannot fail after the
	// identical candidate graph above succeeded, but retain the error boundary.
	if err := vm.services.Restore(serviceState); err != nil {
		return fmt.Errorf("load SKVM state: commit services: %w", err)
	}
	candidate.services = vm.services
	*vm = *candidate
	return nil
}

func (vm *VM) snapshotMetadata() (vmMetadataState, error) {
	state := vmMetadataState{
		Schema:           vmStateVersion,
		ClassDigest:      vm.classDigest,
		NextReference:    vm.nextReference,
		InstructionLimit: vm.InstructionLimit,
		Instructions:     vm.Instructions,
		ScreenWidth:      int32(vm.ScreenWidth),
		ScreenHeight:     int32(vm.ScreenHeight),
		DisplayReference: vm.displayReference,
		CurrentDisplay:   vm.currentDisplay,
		ScreenGraphics:   vm.screenGraphics,
		ServiceOwner:     vm.serviceOwner,
		ScreenSurface:    vm.screenSurface,
		DefaultFont:      vm.defaultFont,
	}
	classNames := make([]string, 0, len(vm.classes))
	for name := range vm.classes {
		classNames = append(classNames, name)
	}
	sort.Strings(classNames)
	for _, name := range classNames {
		class := vm.classes[name]
		state.Classes = append(state.Classes, classRuntimeState{
			Name: name, InitState: class.initState,
			Static: snapshotValueMap(class.static),
		})
	}
	state.HostStatic = snapshotValueMap(vm.hostStatic)
	propertyNames := make([]string, 0, len(vm.properties))
	for name := range vm.properties {
		propertyNames = append(propertyNames, name)
	}
	sort.Strings(propertyNames)
	for _, name := range propertyNames {
		state.Properties = append(state.Properties, propertyState{
			Name: name, Value: vm.properties[name],
		})
	}

	references := make([]uint32, 0, len(vm.heap))
	for reference := range vm.heap {
		references = append(references, reference)
	}
	sort.Slice(references, func(i, j int) bool { return references[i] < references[j] })
	firstPointer := make(map[any]uint32)
	for _, reference := range references {
		native := vm.heap[reference].Native
		value := reflect.ValueOf(native)
		if native != nil && value.Kind() == reflect.Pointer && !value.IsNil() {
			if _, exists := firstPointer[native]; !exists {
				firstPointer[native] = reference
			}
		}
	}
	for _, reference := range references {
		object := vm.heap[reference]
		saved := objectState{
			Reference: reference,
			Class:     object.Class,
			Fields:    snapshotValueMap(object.Fields),
		}
		if object.Array != nil {
			saved.Array = &arrayState{
				Descriptor: object.Array.Descriptor,
				Elements:   snapshotValues(object.Array.Elements),
			}
		}
		native, err := snapshotNative(reference, object.Native, firstPointer)
		if err != nil {
			return vmMetadataState{}, fmt.Errorf(
				"save SKVM object %d native state: %w",
				reference,
				err,
			)
		}
		saved.Native = native
		state.Heap = append(state.Heap, saved)
		thread, ok := object.Native.(*threadState)
		if ok && firstPointer[object.Native] == reference &&
			len(thread.continuation) != 0 {
			frames, err := snapshotFrames(
				thread.continuation,
				fmt.Sprintf("thread %d", reference),
			)
			if err != nil {
				return vmMetadataState{}, err
			}
			state.ThreadContinuations = append(
				state.ThreadContinuations,
				threadContinuationState{Reference: reference, Frames: frames},
			)
		}
	}
	frames, err := snapshotFrames(vm.frames, "frame")
	if err != nil {
		return vmMetadataState{}, err
	}
	state.Frames = frames
	return state, nil
}

func (vm *VM) executionFrameCount() int {
	count := len(vm.frames)
	limit := int(maxVMFrames)
	if count > limit {
		return limit + 1
	}
	for _, object := range vm.heap {
		if state, ok := object.Native.(*threadState); ok {
			if len(state.continuation) > limit-count {
				return limit + 1
			}
			count += len(state.continuation)
		}
	}
	return count
}

func snapshotFrames(frames []*frame, label string) ([]frameState, error) {
	saved := make([]frameState, 0, len(frames))
	for index, current := range frames {
		if current == nil || current.class == nil {
			return nil, fmt.Errorf("save SKVM %s %d: invalid frame", label, index)
		}
		if current.pc < math.MinInt32 || current.pc > math.MaxInt32 ||
			current.invokePC < math.MinInt32 || current.invokePC > math.MaxInt32 {
			return nil, fmt.Errorf("save SKVM %s %d: PC exceeds limit", label, index)
		}
		saved = append(saved, frameState{
			Class: current.class.Name, Method: current.method.Name,
			Descriptor: current.method.Descriptor,
			Locals:     snapshotValues(current.locals),
			Stack:      snapshotValues(current.stack),
			PC:         int32(current.pc),
			InvokePC:   int32(current.invokePC),
		})
	}
	return saved, nil
}

func boolToUint32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func snapshotNative(
	reference uint32,
	native any,
	firstPointer map[any]uint32,
) (nativeState, error) {
	if native == nil {
		return nativeState{}, nil
	}
	value := reflect.ValueOf(native)
	if value.Kind() == reflect.Pointer && !value.IsNil() &&
		firstPointer[native] != reference {
		return nativeState{Kind: "alias", Reference: firstPointer[native]}, nil
	}
	switch state := native.(type) {
	case string:
		return nativeState{Kind: "string", Text: state}, nil
	case *stringBufferState:
		return nativeState{Kind: "string-buffer", Text: state.value}, nil
	case *inputStreamState:
		return nativeState{
			Kind: "input-stream", Data: append([]byte(nil), state.data...),
			Offset: int64(state.offset), Flag: state.closed,
			Reference: state.connection,
		}, nil
	case *randomState:
		return nativeState{Kind: "random", Text: state.stream}, nil
	case *threadState:
		return nativeState{
			Kind:      "thread",
			Reference: state.target,
			Flag:      state.active,
			Long:      int64(state.wakeAt),
		}, nil
	case *recordStoreState:
		return nativeState{Kind: "record-store", Text: state.name, Service: state.id}, nil
	case *xFileState:
		return nativeState{
			Kind: "x-file", Text: state.name,
			Data:   append([]byte(nil), state.data...),
			Offset: int64(state.offset),
		}, nil
	case *xTextFieldState:
		return nativeState{Kind: "x-text-field", Text: state.text, Flag: state.focus}, nil
	case *outputStreamState:
		link := uint32(0)
		if state.file != nil {
			link = firstPointer[state.file]
			if link == 0 {
				return nativeState{}, fmt.Errorf("output stream file is not owned by a heap object")
			}
		}
		return nativeState{
			Kind: "output-stream", Data: append([]byte(nil), state.data...),
			Text: state.name, Reference: link,
			References: []uint32{state.connection},
		}, nil
	case *socketConnectionState:
		return nativeState{
			Kind: "socket-connection", Service: state.socket, Flag: state.closed,
		}, nil
	case *httpConnectionState:
		return nativeState{
			Kind: "http-connection", Service: state.request, Flag: state.closed,
		}, nil
	case *audioClipState:
		return nativeState{Kind: "audio-clip", Service: state.clip}, nil
	case *inputStreamReaderState:
		return nativeState{Kind: "input-stream-reader", Reference: state.stream}, nil
	case *imageState:
		return nativeState{
			Kind: "image", Width: int32(state.width), Height: int32(state.height),
			Service: state.surface, Service2: state.asset,
		}, nil
	case *fontState:
		return nativeState{Kind: "font", Service: state.font}, nil
	case *graphicsState:
		return nativeState{
			Kind: "graphics", Width: int32(state.width), Height: int32(state.height),
			Service: state.surface, Service2: state.font, Color: state.color,
			Integer: state.stroke,
		}, nil
	case *dataInputState:
		return nativeState{Kind: "data-input", Reference: state.stream}, nil
	case *dataOutputState:
		return nativeState{Kind: "data-output", Reference: state.stream}, nil
	case *vectorState:
		return nativeState{
			Kind: "vector", References: append([]uint32(nil), state.values...),
		}, nil
	case *integerState:
		return nativeState{Kind: "integer", Integer: state.value}, nil
	case *textComponentHandlerState:
		automata := state.automata.Snapshot()
		return nativeState{
			Kind:      "text-component-handler",
			Reference: state.component,
			Integer:   automata.Mode,
			Width:     automata.ComposingKey,
			Height:    automata.ComposingIndex,
			References: []uint32{
				uint32(automata.Choseong),
				uint32(automata.Jungseong),
				uint32(automata.Jongseong),
				boolToUint32(automata.Composing),
				uint32(automata.LastKey),
				uint32(automata.LastIndex),
			},
		}, nil
	case *dateState:
		return nativeState{Kind: "date", Long: state.millis}, nil
	case *hashtableState:
		return nativeState{
			Kind: "hashtable",
			Map:  snapshotReferenceMap(state.values),
			Map2: snapshotReferenceMap(state.keys),
		}, nil
	case *timerObjectState:
		return nativeState{
			Kind: "timer", Services: append([]shared.ServiceID(nil), state.timers...),
		}, nil
	case *timerTaskState:
		return nativeState{
			Kind: "timer-task", Service: state.timer, Flag: state.cancelled,
		}, nil
	default:
		return nativeState{}, fmt.Errorf("unsupported native state %T", native)
	}
}

func decodeVMState(data []byte) (vmMetadataState, shared.ServicesState, error) {
	if uint64(len(data)) > maxVMStateBytes {
		return vmMetadataState{}, shared.ServicesState{},
			fmt.Errorf("load SKVM state: size exceeds limit")
	}
	minimum := len(vmStateMagic) + 4 + 8 + sha256.Size + 8 +
		sha256.Size + sha256.Size
	if len(data) < minimum {
		return vmMetadataState{}, shared.ServicesState{},
			fmt.Errorf("load SKVM state: truncated header")
	}
	payload := data[:len(data)-sha256.Size]
	expected := data[len(payload):]
	actual := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return vmMetadataState{}, shared.ServicesState{},
			fmt.Errorf("load SKVM state: checksum mismatch")
	}
	decoder := vmStateDecoder{reader: bytes.NewReader(payload)}
	if magic := decoder.bytes(len(vmStateMagic)); string(magic) != vmStateMagic {
		return vmMetadataState{}, shared.ServicesState{}, decoder.fail("magic mismatch")
	}
	if version := decoder.u32(); version != vmStateVersion {
		return vmMetadataState{}, shared.ServicesState{},
			decoder.fail(fmt.Sprintf("unsupported version %d", version))
	}
	metadataSize := decoder.u64()
	if metadataSize > maxVMMetadataBytes ||
		metadataSize > uint64(decoder.reader.Len()) ||
		metadataSize > uint64(MaxHostInt()) {
		return vmMetadataState{}, shared.ServicesState{},
			decoder.fail("invalid metadata size")
	}
	metadataPayload := decoder.bytes(int(metadataSize))
	metadataChecksum := decoder.bytes(sha256.Size)
	digest := sha256.Sum256(metadataPayload)
	if subtle.ConstantTimeCompare(metadataChecksum, digest[:]) != 1 {
		return vmMetadataState{}, shared.ServicesState{},
			decoder.fail("metadata checksum mismatch")
	}
	serviceSize := decoder.u64()
	if serviceSize > shared.MaxServicesStateBytes ||
		serviceSize > uint64(decoder.reader.Len()) ||
		serviceSize > uint64(MaxHostInt()) {
		return vmMetadataState{}, shared.ServicesState{},
			decoder.fail("invalid service state size")
	}
	servicePayload := decoder.bytes(int(serviceSize))
	serviceChecksum := decoder.bytes(sha256.Size)
	digest = sha256.Sum256(servicePayload)
	if subtle.ConstantTimeCompare(serviceChecksum, digest[:]) != 1 {
		return vmMetadataState{}, shared.ServicesState{},
			decoder.fail("service checksum mismatch")
	}
	if decoder.err != nil {
		return vmMetadataState{}, shared.ServicesState{}, decoder.err
	}
	if decoder.reader.Len() != 0 {
		return vmMetadataState{}, shared.ServicesState{},
			decoder.fail(fmt.Sprintf("%d trailing bytes", decoder.reader.Len()))
	}
	var metadata vmMetadataState
	if err := shared.UnmarshalStateComponent(metadataPayload, &metadata); err != nil {
		return vmMetadataState{}, shared.ServicesState{},
			fmt.Errorf("load SKVM state: decode metadata: %w", err)
	}
	serviceState, err := shared.DecodeServicesState(servicePayload)
	if err != nil {
		return vmMetadataState{}, shared.ServicesState{},
			fmt.Errorf("load SKVM state: decode services: %w", err)
	}
	return metadata, serviceState, nil
}

func (vm *VM) buildCandidate(
	state vmMetadataState,
	services *shared.Services,
) (*VM, error) {
	if state.Schema != vmStateVersion || state.NextReference == 0 ||
		state.ClassDigest != vm.classDigest ||
		state.ServiceOwner != vm.serviceOwner ||
		state.ScreenWidth != int32(vm.ScreenWidth) ||
		state.ScreenHeight != int32(vm.ScreenHeight) ||
		len(state.Heap) > int(maxVMHeapObjects) ||
		len(state.Frames) > int(maxVMFrames) ||
		state.ScreenWidth <= 0 || state.ScreenHeight <= 0 ||
		state.ScreenWidth > math.MaxInt32 || state.ScreenHeight > math.MaxInt32 {
		return nil, fmt.Errorf("load SKVM state: invalid metadata limits")
	}
	frameCount := len(state.Frames)
	for _, continuation := range state.ThreadContinuations {
		if len(continuation.Frames) > int(maxVMFrames)-frameCount {
			return nil, fmt.Errorf("load SKVM state: thread frame limit exceeded")
		}
		frameCount += len(continuation.Frames)
	}
	if len(state.Classes) != len(vm.classes) {
		return nil, fmt.Errorf("load SKVM state: class table size mismatch")
	}
	candidate := &VM{
		classes:          make(map[string]*runtimeClass, len(vm.classes)),
		heap:             make(map[uint32]*Object, len(state.Heap)),
		nextReference:    state.NextReference,
		natives:          vm.natives,
		hostSupers:       vm.hostSupers,
		hostStatic:       make(map[string]Value, len(state.HostStatic)),
		hook:             vm.hook,
		InstructionLimit: state.InstructionLimit,
		Instructions:     state.Instructions,
		ScreenWidth:      int(state.ScreenWidth),
		ScreenHeight:     int(state.ScreenHeight),
		displayReference: state.DisplayReference,
		currentDisplay:   state.CurrentDisplay,
		properties:       make(map[string]string, len(state.Properties)),
		screenGraphics:   state.ScreenGraphics,
		services:         services,
		serviceOwner:     state.ServiceOwner,
		screenSurface:    state.ScreenSurface,
		defaultFont:      state.DefaultFont,
		classDigest:      state.ClassDigest,
	}
	previousName := ""
	for index, saved := range state.Classes {
		current := vm.classes[saved.Name]
		if current == nil || (index != 0 && saved.Name <= previousName) ||
			saved.InitState > classFailed {
			return nil, fmt.Errorf("load SKVM state: invalid class %d", index)
		}
		static, err := restoreValueMap(saved.Static)
		if err != nil {
			return nil, fmt.Errorf("load SKVM state: class %q: %w", saved.Name, err)
		}
		if !sameValueMapShape(static, current.static) {
			return nil, fmt.Errorf("load SKVM state: class %q static field mismatch", saved.Name)
		}
		candidate.classes[saved.Name] = &runtimeClass{
			class: current.class, static: static, initState: saved.InitState,
		}
		previousName = saved.Name
	}
	hostStatic, err := restoreValueMap(state.HostStatic)
	if err != nil {
		return nil, fmt.Errorf("load SKVM state: host statics: %w", err)
	}
	if !sameValueMapShape(hostStatic, vm.hostStatic) {
		return nil, fmt.Errorf("load SKVM state: host static field mismatch")
	}
	candidate.hostStatic = hostStatic
	previousName = ""
	for index, property := range state.Properties {
		if strings.TrimSpace(property.Name) == "" || len(property.Name) > 255 ||
			len(property.Value) > 4096 ||
			(index != 0 && property.Name <= previousName) {
			return nil, fmt.Errorf("load SKVM state: invalid property %d", index)
		}
		candidate.properties[property.Name] = property.Value
		previousName = property.Name
	}

	links := make(map[uint32]nativeLink)
	var previousReference uint32
	for index, saved := range state.Heap {
		if saved.Reference == 0 ||
			(index != 0 && saved.Reference <= previousReference) ||
			saved.Reference >= state.NextReference ||
			!candidate.knownClass(saved.Class) {
			return nil, fmt.Errorf("load SKVM state: invalid heap object %d", index)
		}
		fields, err := restoreValueMap(saved.Fields)
		if err != nil {
			return nil, fmt.Errorf("load SKVM state: object %d fields: %w", saved.Reference, err)
		}
		object := &Object{Class: saved.Class, Fields: fields}
		if saved.Array != nil {
			if saved.Array.Descriptor != saved.Class ||
				!strings.HasPrefix(saved.Array.Descriptor, "[") {
				return nil, fmt.Errorf("load SKVM state: object %d invalid array", saved.Reference)
			}
			elements, err := restoreValues(saved.Array.Elements)
			if err != nil {
				return nil, fmt.Errorf("load SKVM state: object %d array: %w", saved.Reference, err)
			}
			object.Array = &Array{
				Descriptor: saved.Array.Descriptor, Elements: elements,
			}
		} else if strings.HasPrefix(saved.Class, "[") {
			return nil, fmt.Errorf("load SKVM state: array object %d has no array", saved.Reference)
		}
		native, link, err := restoreNative(saved.Native)
		if err != nil {
			return nil, fmt.Errorf("load SKVM state: object %d native: %w", saved.Reference, err)
		}
		object.Native = native
		if link != (nativeLink{}) {
			links[saved.Reference] = link
		}
		candidate.heap[saved.Reference] = object
		previousReference = saved.Reference
	}
	for reference, link := range links {
		object := candidate.heap[reference]
		if link.alias != 0 {
			target := candidate.heap[link.alias]
			if target == nil || target.Native == nil {
				return nil, fmt.Errorf(
					"load SKVM state: object %d has invalid native alias %d",
					reference,
					link.alias,
				)
			}
			object.Native = target.Native
		}
		if link.file != 0 {
			target := candidate.heap[link.file]
			file, ok := nativeAsXFile(target)
			if !ok {
				return nil, fmt.Errorf(
					"load SKVM state: object %d has invalid file link %d",
					reference,
					link.file,
				)
			}
			stream, ok := object.Native.(*outputStreamState)
			if !ok {
				return nil, fmt.Errorf("load SKVM state: invalid output stream link")
			}
			stream.file = file
		}
	}
	var previousThread uint32
	for index, saved := range state.ThreadContinuations {
		if saved.Reference == 0 ||
			(index != 0 && saved.Reference <= previousThread) ||
			len(saved.Frames) == 0 {
			return nil, fmt.Errorf(
				"load SKVM state: invalid thread continuation %d",
				index,
			)
		}
		object := candidate.heap[saved.Reference]
		thread, ok := object.Native.(*threadState)
		if !ok || !thread.active || len(thread.continuation) != 0 {
			return nil, fmt.Errorf(
				"load SKVM state: invalid thread continuation object %d",
				saved.Reference,
			)
		}
		for frameIndex, savedFrame := range saved.Frames {
			current, err := restoreFrame(
				candidate,
				savedFrame,
				fmt.Sprintf(
					"thread %d frame %d",
					saved.Reference,
					frameIndex,
				),
			)
			if err != nil {
				return nil, err
			}
			if current.invokePC < 0 {
				return nil, fmt.Errorf(
					"load SKVM state: thread %d frame %d has no pending invocation",
					saved.Reference,
					frameIndex,
				)
			}
			thread.continuation = append(thread.continuation, current)
		}
		if err := validateContinuationFrames(thread.continuation); err != nil {
			return nil, fmt.Errorf(
				"load SKVM state: thread %d continuation: %w",
				saved.Reference,
				err,
			)
		}
		previousThread = saved.Reference
	}
	for index, saved := range state.Frames {
		current, err := restoreFrame(candidate, saved, fmt.Sprintf("frame %d", index))
		if err != nil {
			return nil, err
		}
		candidate.frames = append(candidate.frames, current)
	}
	if err := candidate.validateReferences(); err != nil {
		return nil, err
	}
	return candidate, nil
}

func restoreFrame(vm *VM, saved frameState, label string) (*frame, error) {
	runtime := vm.classes[saved.Class]
	if runtime == nil {
		return nil, fmt.Errorf("load SKVM state: %s class is missing", label)
	}
	method, ok := runtime.class.Method(saved.Method, saved.Descriptor)
	if !ok || saved.PC < 0 || int(saved.PC) > len(method.Code) ||
		len(saved.Locals) != int(method.MaxLocals) ||
		len(saved.Stack) > int(method.MaxStack) ||
		saved.InvokePC < -1 || int(saved.InvokePC) >= len(method.Code) {
		return nil, fmt.Errorf("load SKVM state: invalid %s", label)
	}
	if saved.InvokePC >= 0 {
		opcode := method.Code[saved.InvokePC]
		if opcode < 0xb6 || opcode > 0xb9 ||
			saved.InvokePC >= saved.PC {
			return nil, fmt.Errorf("load SKVM state: invalid %s invocation", label)
		}
	}
	locals, err := restoreValues(saved.Locals)
	if err != nil {
		return nil, fmt.Errorf("load SKVM state: %s locals: %w", label, err)
	}
	stack, err := restoreValues(saved.Stack)
	if err != nil {
		return nil, fmt.Errorf("load SKVM state: %s stack: %w", label, err)
	}
	return &frame{
		class: runtime.class, method: method, locals: locals,
		stack: stack, pc: int(saved.PC), invokePC: int(saved.InvokePC),
	}, nil
}

func validateContinuationFrames(frames []*frame) error {
	if len(frames) == 0 {
		return fmt.Errorf("empty continuation")
	}
	for index, current := range frames {
		reference, result, err := pendingInvocation(current)
		if err != nil {
			return fmt.Errorf("frame %d: %w", index, err)
		}
		if index+1 < len(frames) {
			if reference.Descriptor != frames[index+1].method.Descriptor {
				return fmt.Errorf(
					"frame %d invokes %s but child descriptor is %s",
					index,
					reference.Descriptor,
					frames[index+1].method.Descriptor,
				)
			}
			continue
		}
		if result.kind != 'V' {
			return fmt.Errorf("yielding invocation returns a value")
		}
	}
	return nil
}

func pendingInvocation(current *frame) (Reference, valueType, error) {
	if current == nil || current.class == nil ||
		current.invokePC < 0 || current.invokePC+2 >= len(current.method.Code) {
		return Reference{}, valueType{}, fmt.Errorf("invalid pending invocation")
	}
	opcode := current.method.Code[current.invokePC]
	instructionSize := 3
	if opcode == 0xb9 {
		instructionSize = 5
	} else if opcode < 0xb6 || opcode > 0xb8 {
		return Reference{}, valueType{}, fmt.Errorf(
			"pending opcode 0x%02x is not an invocation",
			opcode,
		)
	}
	if current.pc != current.invokePC+instructionSize ||
		current.invokePC+instructionSize > len(current.method.Code) {
		return Reference{}, valueType{}, fmt.Errorf("invalid invocation return PC")
	}
	index := binary.BigEndian.Uint16(
		current.method.Code[current.invokePC+1 : current.invokePC+3],
	)
	reference, err := current.class.Reference(index)
	if err != nil {
		return Reference{}, valueType{}, err
	}
	_, result, err := parseMethodDescriptor(reference.Descriptor)
	if err != nil {
		return Reference{}, valueType{}, err
	}
	return reference, result, nil
}

func sameValueMapShape(saved, current map[string]Value) bool {
	if len(saved) != len(current) {
		return false
	}
	for name, value := range saved {
		expected, ok := current[name]
		if !ok || value.Kind != expected.Kind {
			return false
		}
	}
	return true
}

func restoreNative(saved nativeState) (any, nativeLink, error) {
	switch saved.Kind {
	case "":
		return nil, nativeLink{}, nil
	case "alias":
		if saved.Reference == 0 {
			return nil, nativeLink{}, fmt.Errorf("zero alias")
		}
		return nil, nativeLink{alias: saved.Reference}, nil
	case "string":
		return saved.Text, nativeLink{}, nil
	case "string-buffer":
		return &stringBufferState{value: saved.Text}, nativeLink{}, nil
	case "input-stream":
		if saved.Offset < 0 || saved.Offset > int64(len(saved.Data)) {
			return nil, nativeLink{}, fmt.Errorf("invalid input stream offset")
		}
		return &inputStreamState{
			data:   append([]byte(nil), saved.Data...),
			offset: int(saved.Offset), closed: saved.Flag,
			connection: saved.Reference,
		}, nativeLink{}, nil
	case "random":
		if strings.TrimSpace(saved.Text) == "" || len(saved.Text) > 64 {
			return nil, nativeLink{}, fmt.Errorf("invalid random stream")
		}
		return &randomState{stream: saved.Text}, nativeLink{}, nil
	case "thread":
		if saved.Long < 0 {
			return nil, nativeLink{}, fmt.Errorf("invalid thread wake time")
		}
		return &threadState{
			target: saved.Reference,
			active: saved.Flag,
			wakeAt: time.Duration(saved.Long),
		}, nativeLink{}, nil
	case "record-store":
		return &recordStoreState{name: saved.Text, id: saved.Service}, nativeLink{}, nil
	case "x-file":
		if saved.Offset < 0 || saved.Offset > int64(len(saved.Data)) {
			return nil, nativeLink{}, fmt.Errorf("invalid file offset")
		}
		return &xFileState{
			name: saved.Text,
			data: append([]byte(nil), saved.Data...), offset: int(saved.Offset),
		}, nativeLink{}, nil
	case "x-text-field":
		return &xTextFieldState{text: saved.Text, focus: saved.Flag}, nativeLink{}, nil
	case "output-stream":
		connection := uint32(0)
		if len(saved.References) > 1 {
			return nil, nativeLink{}, fmt.Errorf("invalid output stream references")
		}
		if len(saved.References) == 1 {
			connection = saved.References[0]
		}
		return &outputStreamState{
			data: append([]byte(nil), saved.Data...), name: saved.Text,
			connection: connection,
		}, nativeLink{file: saved.Reference}, nil
	case "socket-connection":
		if saved.Flag && saved.Service != 0 || !saved.Flag && saved.Service == 0 {
			return nil, nativeLink{}, fmt.Errorf("invalid socket connection state")
		}
		return &socketConnectionState{
			socket: saved.Service,
			closed: saved.Flag,
		}, nativeLink{}, nil
	case "http-connection":
		if saved.Flag && saved.Service != 0 || !saved.Flag && saved.Service == 0 {
			return nil, nativeLink{}, fmt.Errorf("invalid HTTP connection state")
		}
		return &httpConnectionState{
			request: saved.Service,
			closed:  saved.Flag,
		}, nativeLink{}, nil
	case "audio-clip":
		return &audioClipState{clip: saved.Service}, nativeLink{}, nil
	case "input-stream-reader":
		return &inputStreamReaderState{stream: saved.Reference}, nativeLink{}, nil
	case "image":
		if saved.Width <= 0 || saved.Height <= 0 {
			return nil, nativeLink{}, fmt.Errorf("invalid image geometry")
		}
		return &imageState{
			width: int(saved.Width), height: int(saved.Height),
			surface: saved.Service, asset: saved.Service2,
		}, nativeLink{}, nil
	case "font":
		return &fontState{font: saved.Service}, nativeLink{}, nil
	case "graphics":
		if saved.Width <= 0 || saved.Height <= 0 {
			return nil, nativeLink{}, fmt.Errorf("invalid graphics geometry")
		}
		return &graphicsState{
			width: int(saved.Width), height: int(saved.Height),
			surface: saved.Service, font: saved.Service2, color: saved.Color,
			stroke: saved.Integer,
		}, nativeLink{}, nil
	case "data-input":
		return &dataInputState{stream: saved.Reference}, nativeLink{}, nil
	case "data-output":
		return &dataOutputState{stream: saved.Reference}, nativeLink{}, nil
	case "vector":
		return &vectorState{
			values: append([]uint32(nil), saved.References...),
		}, nativeLink{}, nil
	case "integer":
		return &integerState{value: saved.Integer}, nativeLink{}, nil
	case "text-component-handler":
		handler := newTextComponentHandlerState()
		handler.component = saved.Reference
		automata := ime.State{
			Mode:           saved.Integer,
			ComposingKey:   saved.Width,
			ComposingIndex: saved.Height,
		}
		if len(saved.References) == 6 {
			automata.Choseong = int32(saved.References[0])
			automata.Jungseong = int32(saved.References[1])
			automata.Jongseong = int32(saved.References[2])
			automata.Composing = saved.References[3] != 0
			automata.LastKey = int32(saved.References[4])
			automata.LastIndex = int32(saved.References[5])
		}
		handler.automata.Restore(automata)
		return handler, nativeLink{}, nil
	case "date":
		return &dateState{millis: saved.Long}, nativeLink{}, nil
	case "hashtable":
		values, err := restoreReferenceMap(saved.Map)
		if err != nil {
			return nil, nativeLink{}, err
		}
		keys, err := restoreReferenceMap(saved.Map2)
		if err != nil {
			return nil, nativeLink{}, err
		}
		return &hashtableState{values: values, keys: keys}, nativeLink{}, nil
	case "timer":
		return &timerObjectState{
			timers: append([]shared.ServiceID(nil), saved.Services...),
		}, nativeLink{}, nil
	case "timer-task":
		return &timerTaskState{
			timer: saved.Service, cancelled: saved.Flag,
		}, nativeLink{}, nil
	default:
		return nil, nativeLink{}, fmt.Errorf("unknown native kind %q", saved.Kind)
	}
}

func (vm *VM) validateReferences() error {
	validateValue := func(value Value, context string) error {
		if !value.Kind.valid() {
			return fmt.Errorf("load SKVM state: %s has invalid value kind %d", context, value.Kind)
		}
		if value.Kind == ValueReference {
			reference := uint32(value.bits)
			if value.bits > math.MaxUint32 ||
				(reference != 0 && vm.heap[reference] == nil) {
				return fmt.Errorf("load SKVM state: %s has dangling reference %d", context, reference)
			}
		}
		return nil
	}
	for reference, object := range vm.heap {
		for name, value := range object.Fields {
			if err := validateValue(value, fmt.Sprintf("object %d field %q", reference, name)); err != nil {
				return err
			}
		}
		if object.Array != nil {
			for index, value := range object.Array.Elements {
				if err := validateValue(
					value,
					fmt.Sprintf("object %d element %d", reference, index),
				); err != nil {
					return err
				}
			}
		}
		if err := vm.validateNative(reference, object.Native); err != nil {
			return err
		}
	}
	for name, value := range vm.hostStatic {
		if err := validateValue(value, "host static "+name); err != nil {
			return err
		}
	}
	for name, class := range vm.classes {
		for field, value := range class.static {
			if err := validateValue(value, "class "+name+" static "+field); err != nil {
				return err
			}
		}
	}
	for frameIndex, current := range vm.frames {
		for index, value := range current.locals {
			if err := validateValue(
				value,
				fmt.Sprintf("frame %d local %d", frameIndex, index),
			); err != nil {
				return err
			}
		}
		for index, value := range current.stack {
			if err := validateValue(
				value,
				fmt.Sprintf("frame %d stack %d", frameIndex, index),
			); err != nil {
				return err
			}
		}
	}
	for reference, object := range vm.heap {
		state, ok := object.Native.(*threadState)
		if !ok {
			continue
		}
		if !state.active && len(state.continuation) != 0 {
			return fmt.Errorf(
				"load SKVM state: inactive thread %d has a continuation",
				reference,
			)
		}
		if state.active && len(state.continuation) == 0 &&
			vm.runningThread != reference {
			return fmt.Errorf(
				"load SKVM state: active thread %d has no continuation",
				reference,
			)
		}
		if len(state.continuation) == 0 {
			continue
		}
		if err := validateContinuationFrames(state.continuation); err != nil {
			return fmt.Errorf(
				"load SKVM state: thread %d continuation: %w",
				reference,
				err,
			)
		}
		for frameIndex, current := range state.continuation {
			for index, value := range current.locals {
				if err := validateValue(
					value,
					fmt.Sprintf(
						"thread %d frame %d local %d",
						reference,
						frameIndex,
						index,
					),
				); err != nil {
					return err
				}
			}
			for index, value := range current.stack {
				if err := validateValue(
					value,
					fmt.Sprintf(
						"thread %d frame %d stack %d",
						reference,
						frameIndex,
						index,
					),
				); err != nil {
					return err
				}
			}
		}
	}
	for name, reference := range map[string]uint32{
		"display":         vm.displayReference,
		"current-display": vm.currentDisplay,
		"screen-graphics": vm.screenGraphics,
	} {
		if reference != 0 && vm.heap[reference] == nil {
			return fmt.Errorf("load SKVM state: %s reference %d is missing", name, reference)
		}
	}
	descriptor, err := vm.services.Graphics.Descriptor(vm.serviceOwner, vm.screenSurface)
	if err != nil || descriptor.Width != int32(vm.ScreenWidth) ||
		descriptor.Height != int32(vm.ScreenHeight) ||
		vm.services.Graphics.Screen() != vm.screenSurface {
		return fmt.Errorf("load SKVM state: invalid screen service mapping")
	}
	if _, err := vm.services.Text.Metrics(vm.serviceOwner, vm.defaultFont); err != nil {
		return fmt.Errorf("load SKVM state: invalid default font mapping: %w", err)
	}
	return nil
}

func (vm *VM) validateNative(reference uint32, native any) error {
	validateRef := func(value uint32, label string) error {
		if value != 0 && vm.heap[value] == nil {
			return fmt.Errorf(
				"load SKVM state: object %d %s reference %d is missing",
				reference,
				label,
				value,
			)
		}
		return nil
	}
	switch state := native.(type) {
	case nil, string, *stringBufferState,
		*integerState, *dateState, *xTextFieldState:
		return nil
	case *textComponentHandlerState:
		return validateRef(state.component, "text component")
	case *inputStreamState:
		return validateRef(state.connection, "input stream connection")
	case *xFileState:
		if state.name == "" {
			return nil
		}
		normalized, err := vm.services.Storage.NormalizePath(state.name)
		data, readErr := vm.services.Storage.ReadFile(
			shared.NamespacePrivate,
			state.name,
		)
		if err != nil || normalized != state.name ||
			readErr != nil || !bytes.Equal(data, state.data) {
			return fmt.Errorf("load SKVM state: object %d invalid file state", reference)
		}
		return nil
	case *randomState:
		for _, stream := range vm.services.Random.Snapshot().Streams {
			if stream.Name == state.stream && stream.Algorithm == shared.RNGJava48 {
				return nil
			}
		}
		return fmt.Errorf("load SKVM state: object %d random stream is missing", reference)
	case *threadState:
		if state.wakeAt < 0 {
			return fmt.Errorf(
				"load SKVM state: object %d has a negative thread wake time",
				reference,
			)
		}
		return validateRef(state.target, "thread target")
	case *recordStoreState:
		id, err := vm.services.Storage.OpenRecordStore(vm.serviceOwner, state.name)
		if err != nil || id != state.id {
			return fmt.Errorf("load SKVM state: object %d invalid record store", reference)
		}
		return nil
	case *outputStreamState:
		if err := validateRef(state.connection, "output stream connection"); err != nil {
			return err
		}
		if state.name != "" {
			normalized, err := vm.services.Storage.NormalizePath(state.name)
			data, readErr := vm.services.Storage.ReadFile(
				shared.NamespacePrivate,
				state.name,
			)
			if err != nil || normalized != state.name ||
				readErr != nil || !bytes.Equal(data, state.data) {
				return fmt.Errorf(
					"load SKVM state: object %d invalid output file",
					reference,
				)
			}
		}
		return nil
	case *socketConnectionState:
		if state.closed {
			if state.socket != 0 {
				return fmt.Errorf(
					"load SKVM state: object %d closed socket has a service",
					reference,
				)
			}
			return nil
		}
		info, err := vm.services.Network.SocketInfo(vm.serviceOwner, state.socket)
		if err != nil || info.State != shared.ConnectionConnected {
			return fmt.Errorf(
				"load SKVM state: object %d invalid socket connection",
				reference,
			)
		}
		return nil
	case *httpConnectionState:
		if state.closed {
			if state.request != 0 {
				return fmt.Errorf(
					"load SKVM state: object %d closed HTTP connection has a service",
					reference,
				)
			}
			return nil
		}
		info, err := vm.services.Network.HTTPInfo(vm.serviceOwner, state.request)
		if err != nil || info.State == shared.ConnectionClosed {
			return fmt.Errorf(
				"load SKVM state: object %d invalid HTTP connection",
				reference,
			)
		}
		return nil
	case *audioClipState:
		if state.clip == 0 {
			return nil
		}
		if _, err := vm.services.Media.Info(
			vm.serviceOwner,
			state.clip,
		); err != nil {
			return fmt.Errorf("load SKVM state: object %d invalid audio clip", reference)
		}
		return nil
	case *inputStreamReaderState:
		return validateRef(state.stream, "reader stream")
	case *imageState:
		descriptor, err := vm.services.Graphics.Descriptor(vm.serviceOwner, state.surface)
		if err != nil || descriptor.Width != int32(state.width) ||
			descriptor.Height != int32(state.height) {
			return fmt.Errorf("load SKVM state: object %d invalid image surface", reference)
		}
		if state.asset != 0 {
			info, err := vm.services.Assets.Info(vm.serviceOwner, state.asset)
			if err != nil || len(info.Frames) == 0 ||
				info.Frames[0].Surface != state.surface {
				return fmt.Errorf("load SKVM state: object %d invalid image asset", reference)
			}
		}
		return nil
	case *fontState:
		if _, err := vm.services.Text.Metrics(vm.serviceOwner, state.font); err != nil {
			return fmt.Errorf("load SKVM state: object %d invalid font", reference)
		}
		return nil
	case *graphicsState:
		descriptor, err := vm.services.Graphics.Descriptor(vm.serviceOwner, state.surface)
		if err != nil || descriptor.Width != int32(state.width) ||
			descriptor.Height != int32(state.height) {
			return fmt.Errorf("load SKVM state: object %d invalid graphics surface", reference)
		}
		if state.font != 0 {
			if _, err := vm.services.Text.Metrics(vm.serviceOwner, state.font); err != nil {
				return fmt.Errorf("load SKVM state: object %d invalid graphics font", reference)
			}
		}
		return nil
	case *dataInputState:
		return validateRef(state.stream, "data input stream")
	case *dataOutputState:
		return validateRef(state.stream, "data output stream")
	case *vectorState:
		for _, item := range state.values {
			if err := validateRef(item, "vector item"); err != nil {
				return err
			}
		}
		return nil
	case *hashtableState:
		for _, values := range []map[string]uint32{state.values, state.keys} {
			for _, item := range values {
				if err := validateRef(item, "hashtable item"); err != nil {
					return err
				}
			}
		}
		return nil
	case *timerObjectState:
		for _, id := range state.timers {
			if _, err := vm.services.Timers.Get(id, vm.serviceOwner); err != nil {
				return fmt.Errorf("load SKVM state: object %d invalid timer", reference)
			}
		}
		return nil
	case *timerTaskState:
		if state.timer != 0 {
			if _, err := vm.services.Timers.Get(state.timer, vm.serviceOwner); err != nil {
				return fmt.Errorf("load SKVM state: object %d invalid timer task", reference)
			}
		}
		return nil
	default:
		return fmt.Errorf("load SKVM state: object %d unsupported native %T", reference, native)
	}
}

func (vm *VM) knownClass(class string) bool {
	if class == "" || len(class) > 1024 || strings.IndexByte(class, 0) >= 0 ||
		strings.Contains(class, `\`) || strings.Contains(class, ".") {
		return false
	}
	if strings.HasPrefix(class, "[") {
		return len(class) > 1
	}
	if vm.classes[class] != nil {
		return true
	}
	if _, ok := vm.hostSupers[class]; ok {
		return true
	}
	for _, part := range strings.Split(class, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func nativeAsXFile(object *Object) (*xFileState, bool) {
	if object == nil {
		return nil, false
	}
	state, ok := object.Native.(*xFileState)
	return state, ok
}

func snapshotValues(values []Value) []valueState {
	result := make([]valueState, len(values))
	for index, value := range values {
		result[index] = valueState{Kind: value.Kind, Bits: value.bits}
	}
	return result
}

func restoreValues(saved []valueState) ([]Value, error) {
	result := make([]Value, len(saved))
	for index, value := range saved {
		if !value.Kind.valid() ||
			((value.Kind == ValueInt || value.Kind == ValueReference ||
				value.Kind == ValueReturnAddress) && value.Bits > math.MaxUint32) ||
			(value.Kind == ValueTop && value.Bits != 0) {
			return nil, fmt.Errorf("invalid value %d", index)
		}
		result[index] = Value{Kind: value.Kind, bits: value.Bits}
	}
	return result, nil
}

func snapshotValueMap(values map[string]Value) []namedValueState {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]namedValueState, 0, len(names))
	for _, name := range names {
		value := values[name]
		result = append(result, namedValueState{
			Name: name, Value: valueState{Kind: value.Kind, Bits: value.bits},
		})
	}
	return result
}

func restoreValueMap(saved []namedValueState) (map[string]Value, error) {
	result := make(map[string]Value, len(saved))
	previous := ""
	for index, item := range saved {
		if item.Name == "" || (index != 0 && item.Name <= previous) {
			return nil, fmt.Errorf("invalid named value %d", index)
		}
		value, err := restoreValues([]valueState{item.Value})
		if err != nil {
			return nil, fmt.Errorf("named value %d: %w", index, err)
		}
		result[item.Name] = value[0]
		previous = item.Name
	}
	return result, nil
}

func snapshotReferenceMap(values map[string]uint32) []nativeMapEntry {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]nativeMapEntry, 0, len(names))
	for _, name := range names {
		result = append(result, nativeMapEntry{Key: name, Reference: values[name]})
	}
	return result
}

func restoreReferenceMap(saved []nativeMapEntry) (map[string]uint32, error) {
	result := make(map[string]uint32, len(saved))
	previous := ""
	for index, item := range saved {
		if item.Key == "" || (index != 0 && item.Key <= previous) {
			return nil, fmt.Errorf("invalid native map item %d", index)
		}
		result[item.Key] = item.Reference
		previous = item.Key
	}
	return result, nil
}

func (kind ValueKind) valid() bool {
	return kind >= ValueTop && kind <= ValueReturnAddress
}

type vmStateDecoder struct {
	reader *bytes.Reader
	offset uint64
	err    error
}

func (d *vmStateDecoder) bytes(size int) []byte {
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

func (d *vmStateDecoder) u32() uint32 {
	data := d.bytes(4)
	if len(data) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(data)
}

func (d *vmStateDecoder) u64() uint64 {
	data := d.bytes(8)
	if len(data) != 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(data)
}

func (d *vmStateDecoder) fail(reason string) error {
	return fmt.Errorf("load SKVM state at offset 0x%x: %s", d.offset, reason)
}

func writeVMU32(output *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	output.Write(encoded[:])
}

func writeVMU64(output *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	output.Write(encoded[:])
}

func MaxHostInt() int {
	return int(^uint(0) >> 1)
}
