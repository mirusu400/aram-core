package ktf

import (
	"context"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func ktfKernelSprintk(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	destination, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	formatAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if destination == 0 || formatAddress == 0 {
		return 0, nil
	}
	format, err := runtime.readCString(formatAddress, 4096)
	if err != nil {
		return 0, err
	}
	formatted, err := runtime.formatWIPICString(format, 2)
	if err != nil {
		return 0, err
	}
	if len(formatted) > 64<<10 {
		return 0, errors.New("KTF sprintk output exceeds 64 KiB")
	}
	if err := runtime.CPU.WriteMemory(
		destination,
		append([]byte(formatted), 0),
	); err != nil {
		return 0, err
	}
	runtime.tracef("wipic_sprintk:format=%q:result=%q", format, formatted)
	return uint32(len(formatted)), nil
}

func (r *Runtime) formatWIPICString(
	format string,
	argumentIndex uint32,
) (string, error) {
	var output strings.Builder
	for offset := 0; offset < len(format); {
		if format[offset] != '%' {
			output.WriteByte(format[offset])
			offset++
			continue
		}
		offset++
		if offset < len(format) && format[offset] == '%' {
			output.WriteByte('%')
			offset++
			continue
		}

		flagsStart := offset
		for offset < len(format) &&
			strings.ContainsRune("-+ #0", rune(format[offset])) {
			offset++
		}
		flags := format[flagsStart:offset]
		width := ""
		if offset < len(format) && format[offset] == '*' {
			value, err := r.parameter(argumentIndex)
			if err != nil {
				return "", err
			}
			argumentIndex++
			signed := int32(value)
			if signed < 0 {
				if !strings.Contains(flags, "-") {
					flags += "-"
				}
				signed = -signed
			}
			width = strconv.FormatInt(int64(signed), 10)
			offset++
		} else {
			widthStart := offset
			for offset < len(format) &&
				format[offset] >= '0' && format[offset] <= '9' {
				offset++
			}
			width = format[widthStart:offset]
		}
		precision := ""
		if offset < len(format) && format[offset] == '.' {
			offset++
			if offset < len(format) && format[offset] == '*' {
				value, err := r.parameter(argumentIndex)
				if err != nil {
					return "", err
				}
				argumentIndex++
				if int32(value) >= 0 {
					precision = "." + strconv.FormatUint(uint64(value), 10)
				}
				offset++
			} else {
				precisionStart := offset
				for offset < len(format) &&
					format[offset] >= '0' && format[offset] <= '9' {
					offset++
				}
				precision = "." + format[precisionStart:offset]
			}
		}
		length := ""
		if offset < len(format) && strings.ContainsRune("hljztL", rune(format[offset])) {
			length = string(format[offset])
			offset++
			if offset < len(format) &&
				(format[offset] == 'h' && length == "h" ||
					format[offset] == 'l' && length == "l") {
				length += string(format[offset])
				offset++
			}
		}
		if offset >= len(format) {
			output.WriteByte('%')
			break
		}
		verb := format[offset]
		offset++
		if verb == 'n' {
			address, err := r.parameter(argumentIndex)
			if err != nil {
				return "", err
			}
			argumentIndex++
			if address != 0 {
				if err := r.WriteU32(address, uint32(output.Len())); err != nil {
					return "", err
				}
			}
			continue
		}

		if length == "ll" || length == "j" {
			if argumentIndex&1 != 0 {
				argumentIndex++
			}
			low, err := r.parameter(argumentIndex)
			if err != nil {
				return "", err
			}
			high, err := r.parameter(argumentIndex + 1)
			if err != nil {
				return "", err
			}
			argumentIndex += 2
			value := uint64(high)<<32 | uint64(low)
			goVerb := verb
			var argument any = value
			if verb == 'd' || verb == 'i' {
				goVerb = 'd'
				argument = int64(value)
			} else if verb == 'u' {
				goVerb = 'd'
			}
			output.WriteString(fmt.Sprintf(
				"%"+flags+width+precision+string(goVerb),
				argument,
			))
			continue
		}

		value, err := r.parameter(argumentIndex)
		if err != nil {
			return "", err
		}
		argumentIndex++
		goFormat := "%" + flags + width + precision
		switch verb {
		case 's':
			text := "(null)"
			if value != 0 {
				text, err = r.readCString(value, 64<<10)
				if err != nil {
					return "", err
				}
			}
			output.WriteString(fmt.Sprintf(goFormat+"s", text))
		case 'd', 'i':
			output.WriteString(fmt.Sprintf(goFormat+"d", int32(value)))
		case 'u':
			output.WriteString(fmt.Sprintf(goFormat+"d", value))
		case 'x', 'X', 'o':
			output.WriteString(fmt.Sprintf(goFormat+string(verb), value))
		case 'c':
			output.WriteString(fmt.Sprintf(goFormat+"c", rune(value)))
		case 'p':
			if !strings.Contains(flags, "#") {
				flags += "#"
			}
			output.WriteString(fmt.Sprintf(
				"%"+flags+width+precision+"x",
				value,
			))
		default:
			output.WriteByte('%')
			output.WriteString(flags)
			output.WriteString(width)
			output.WriteString(precision)
			output.WriteString(length)
			output.WriteByte(verb)
		}
	}
	return output.String(), nil
}

func ktfKernelAllocate(clear bool) ktfHostHandler {
	return func(_ context.Context, runtime *Runtime) (uint32, error) {
		size, err := runtime.parameter(0)
		if err != nil {
			return 0, err
		}
		return runtime.allocateWIPICMemory(size, clear)
	}
}

// allocateWIPICMemory creates the two-level buffer shape returned by KTF's
// kernel allocator and by provider APIs such as MC_grpEncodeImage. Clet support
// code dereferences the returned ID to find the INDIRECT_BUF_HEAD, then uses
// the payload at head+8.
func (r *Runtime) allocateWIPICMemory(size uint32, clear bool) (uint32, error) {
	if size == 0 {
		size = 1
	}
	if size > ^uint32(0)-8 {
		return 0, errors.New("KTF kernel allocation size overflows")
	}
	base, err := r.Heap.Allocate(size+8, clear)
	if err != nil || base == 0 {
		r.tracef(
			"wipic_memory_alloc_failed:size=%d:clear=%t:error=%v",
			size,
			clear,
			err,
		)
		return 0, err
	}
	data := base + 8
	memoryID, err := r.AllocateWords(2)
	if err != nil {
		r.Heap.Release(base)
		return 0, err
	}
	if err := r.writeWords(memoryID, []uint32{base, size}); err != nil {
		r.Heap.Release(base)
		r.Heap.Release(memoryID)
		return 0, err
	}
	r.wipicMemory[memoryID] = ktfWIPICMemory{
		base: base,
		data: data,
		size: size,
	}
	r.tracef(
		"wipic_memory_alloc:id=0x%08x:base=0x%08x:data=0x%08x:size=%d:clear=%t",
		memoryID,
		base,
		data,
		size,
		clear,
	)
	return memoryID, nil
}

func ktfKernelGetDLLInterface(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if nameAddress == 0 {
		return 0, nil
	}
	name, err := runtime.readCString(nameAddress, 256)
	if err != nil {
		return 0, err
	}
	requestedMajor, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	requestedMinor, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	returnMajor, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	returnMinor, err := runtime.parameter(4)
	if err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_knl_get_dll_interface:%s:%d.%d",
		name,
		int32(requestedMajor),
		int32(requestedMinor),
	)

	const (
		interfaceMajor = uint32(1)
		interfaceMinor = uint32(0)
	)
	majorMatches := int32(interfaceMajor) > int32(requestedMajor) ||
		interfaceMajor == requestedMajor &&
			int32(interfaceMinor) >= int32(requestedMinor)
	if !majorMatches {
		return 0, nil
	}
	address, err := runtime.lookupInterface(name)
	if err != nil || address == 0 {
		return address, err
	}
	if returnMajor != 0 {
		if err := runtime.WriteU32(returnMajor, interfaceMajor); err != nil {
			return 0, err
		}
	}
	if returnMinor != 0 {
		if err := runtime.WriteU32(returnMinor, interfaceMinor); err != nil {
			return 0, err
		}
	}
	return address, nil
}

func ktfIncrementalMemoryAdd(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if base == 0 || size == 0 ||
		uint64(base)+uint64(size) > uint64(^uint32(0))+1 {
		return 0, fmt.Errorf(
			"invalid KTF incremental memory region 0x%08x+0x%x",
			base,
			size,
		)
	}
	var probe [1]byte
	if err := runtime.CPU.ReadMemory(base, probe[:]); err != nil {
		return 0, fmt.Errorf("read KTF incremental memory region start: %w", err)
	}
	if err := runtime.CPU.ReadMemory(base+size-1, probe[:]); err != nil {
		return 0, fmt.Errorf("read KTF incremental memory region end: %w", err)
	}
	if runtime.incrementalHeaps == nil {
		runtime.incrementalHeaps = make(map[uint32]*guest.Heap)
	}
	start, end := uint64(base), uint64(base)+uint64(size)
	for _, region := range runtime.incrementalMemory {
		if region.base == base && region.size == size {
			// Re-declaring a region that is already registered hands the whole
			// buffer back to the arena. 액션히어로3D uses the call that way: it
			// allocates a scratch buffer, reads a file into it, and then
			// re-adds the region instead of freeing the block. Treating the
			// repeat as a no-op leaked every scratch buffer until a later
			// texture allocation returned null and the guest dereferenced it.
			heap := guest.NewHeap(runtime.CPU, base, size)
			runtime.incrementalHeaps[base] = &heap
			runtime.tracef(
				"mx_user_mem_reset:base=0x%08x:size=%d",
				base,
				size,
			)
			return 0, nil
		}
		regionStart := uint64(region.base)
		regionEnd := regionStart + uint64(region.size)
		if start < regionEnd && regionStart < end {
			return 0, fmt.Errorf(
				"KTF incremental memory region 0x%08x+0x%x overlaps 0x%08x+0x%x",
				base,
				size,
				region.base,
				region.size,
			)
		}
	}
	runtime.incrementalMemory = append(
		runtime.incrementalMemory,
		ktfIncrementalMemoryRegion{base: base, size: size},
	)
	heap := guest.NewHeap(runtime.CPU, base, size)
	runtime.incrementalHeaps[base] = &heap
	runtime.tracef(
		"mx_user_mem_add:base=0x%08x:size=%d",
		base,
		size,
	)
	return 0, nil
}

func ktfIncrementalMemoryAllocate(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	heap := runtime.incrementalHeaps[base]
	if heap == nil {
		return 0, fmt.Errorf(
			"KTF user-memory arena 0x%08x is not registered",
			base,
		)
	}
	address, err := heap.Allocate(size, false)
	if err != nil {
		return 0, err
	}
	runtime.tracef(
		"mx_user_mem_alloc:base=0x%08x:size=%d:address=0x%08x",
		base,
		size,
		address,
	)
	return address, nil
}

func ktfIncrementalMemoryReallocate(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	heap := runtime.incrementalHeaps[base]
	if heap == nil {
		return 0, fmt.Errorf(
			"KTF user-memory arena 0x%08x is not registered",
			base,
		)
	}
	if address == 0 {
		return heap.Allocate(size, false)
	}
	if size == 0 {
		heap.Release(address)
		return 0, nil
	}
	oldSize, ok := heap.Allocations[address]
	if !ok {
		return 0, fmt.Errorf(
			"KTF user-memory address 0x%08x is not allocated",
			address,
		)
	}
	replacement, err := heap.Allocate(size, false)
	if err != nil || replacement == 0 {
		return replacement, err
	}
	copySize := min(oldSize, size)
	data := make([]byte, copySize)
	if err := runtime.CPU.ReadMemory(address, data); err != nil {
		heap.Release(replacement)
		return 0, err
	}
	if err := runtime.CPU.WriteMemory(replacement, data); err != nil {
		heap.Release(replacement)
		return 0, err
	}
	heap.Release(address)
	runtime.tracef(
		"mx_user_mem_realloc:base=0x%08x:address=0x%08x:"+
			"size=%d:replacement=0x%08x",
		base,
		address,
		size,
		replacement,
	)
	return replacement, nil
}

func ktfIncrementalMemoryFree(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	heap := runtime.incrementalHeaps[base]
	if heap == nil {
		return 0, fmt.Errorf(
			"KTF user-memory arena 0x%08x is not registered",
			base,
		)
	}
	if address != 0 && !heap.Release(address) {
		return 0, fmt.Errorf(
			"KTF user-memory address 0x%08x is not allocated",
			address,
		)
	}
	runtime.tracef(
		"mx_user_mem_free:base=0x%08x:address=0x%08x",
		base,
		address,
	)
	return 0, nil
}

func ktfKernelFree(_ context.Context, runtime *Runtime) (uint32, error) {
	memoryID, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if memoryID == 0 {
		return 0, nil
	}
	if allocation, ok := runtime.wipicMemory[memoryID]; ok {
		runtime.Heap.Release(allocation.base)
		runtime.Heap.Release(memoryID)
		delete(runtime.wipicMemory, memoryID)
		// MC_knlFree is specified as void, but KTF's ARM provider leaves a
		// non-zero allocation word in r0. Some carrier Clet support libraries
		// tail-return that value and use it as a success predicate before
		// completing their graphics initialization. Preserve the non-zero
		// memory ID rather than synthesizing zero for the void result.
		return memoryID, nil
	}
	// A few mixed Java/C clients pass a direct bridge allocation here.
	runtime.Heap.Release(memoryID)
	return memoryID, nil
}

func ktfTotalMemory(context.Context, *Runtime) (uint32, error) {
	return guest.HeapSize, nil
}

func ktfFreeMemory(_ context.Context, runtime *Runtime) (uint32, error) {
	var available uint64
	for _, block := range runtime.Heap.Root().Free {
		available += uint64(block.Size)
	}
	if available > uint64(^uint32(0)) {
		available = uint64(^uint32(0))
	}
	return uint32(available), nil
}

func ktfKernelDefineTimer(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	callback, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return ^uint32(7), nil
	}
	timer := runtime.wipicTimers[address]
	if timer == nil {
		timer = &ktfWIPICTimer{}
		runtime.wipicTimers[address] = timer
	}
	serviceID := runtime.wipicTimerServices[address]
	if serviceID == 0 {
		serviceID, err = runtime.Services.Timers.Define(
			runtime.ServiceOwner,
			fmt.Sprintf("ktf.wipic.timer.%08x", address),
		)
		if err != nil {
			return 0, err
		}
		runtime.wipicTimerServices[address] = serviceID
	} else if err := runtime.Services.Timers.Cancel(
		serviceID,
		runtime.ServiceOwner,
	); err != nil {
		return 0, err
	}
	timer.callback = callback
	timer.active = false
	runtime.tracef(
		"wipic_timer_define:timer=0x%08x:callback=0x%08x",
		address,
		callback,
	)
	return 0, nil
}

func ktfKernelSetTimer(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	timeoutLow, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	timeoutHigh, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	parameter, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	timer := runtime.wipicTimers[address]
	if address == 0 || timer == nil || timer.callback == 0 {
		return ^uint32(7), nil
	}
	timeout := uint64(timeoutHigh)<<32 | uint64(timeoutLow)
	maxMillis := uint64((time.Duration(1<<63 - 1)) / time.Millisecond)
	if timeout > maxMillis || runtime.TickMS > maxMillis-timeout {
		return ^uint32(7), nil
	}
	timer.parameter = parameter
	timer.deadline = runtime.TickMS + timeout
	timer.active = true
	serviceID := runtime.wipicTimerServices[address]
	if serviceID == 0 {
		return ^uint32(7), nil
	}
	if err := runtime.Services.Timers.Set(
		serviceID,
		runtime.ServiceOwner,
		time.Duration(timer.deadline)*time.Millisecond,
		0,
		int64(address),
	); err != nil {
		timer.active = false
		return 0, err
	}
	runtime.tracef(
		"wipic_timer_set:timer=0x%08x:timeout=%d:parameter=0x%08x:deadline=%d",
		address,
		timeout,
		parameter,
		timer.deadline,
	)
	return 0, nil
}

func ktfKernelUnsetTimer(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if timer := runtime.wipicTimers[address]; timer != nil {
		timer.active = false
	}
	if serviceID := runtime.wipicTimerServices[address]; serviceID != 0 {
		if err := runtime.Services.Timers.Cancel(
			serviceID,
			runtime.ServiceOwner,
		); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

// ktfKernelExit implements MC_knlExit (kernel interface slot 0x1c). The clet
// hands the provider an exit code and expects the program to terminate; the
// call must not fall through to the guest's post-exit code. Without it a native
// Clet that exits from its game-loop timer (issue #53) leaves that timer armed
// and the handset appears frozen on the last drawn frame.
func ktfKernelExit(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	exitCode, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	runtime.tracef("wipic_knl_exit:code=%d", int32(exitCode))
	runtime.requestCletTermination()
	return 0, nil
}

// ktfBusyWaitInstrsPerMS models the CPU throughput a busy-wait delay loop was
// calibrated against: roughly a mid-2000s ARM9 at ~100 MIPS. monotonicReadMS
// credits one virtual millisecond per this many instructions the guest ran
// between two clock reads. Ordinary reads are a few hundred instructions apart
// and stay at the same value; a delay loop runs tens of millions of
// instructions between reads and so advances quickly.
const ktfBusyWaitInstrsPerMS = uint64(100_000)

// monotonicReadMS returns the millisecond clock for a guest read. Virtual time
// is frozen for the duration of a host call, so a guest busy-wait that spins
// until the clock advances would never end and would burn its whole
// instruction budget. To break that, the read credits the instructions the
// current host call ran since the previous read as elapsed virtual time. The
// real TickMS is left untouched (the presentation clock and timers are
// unaffected); the credit resets whenever a quantum advances TickMS.
func (r *Runtime) monotonicReadMS() uint64 {
	if r.TickMS != r.clockReadBaseTick {
		r.clockReadBaseTick = r.TickMS
		r.clockReadInstrs = r.TotalInstructions
		r.clockReadOffset = 0
		return r.TickMS
	}
	if r.TotalInstructions > r.clockReadInstrs {
		r.clockReadOffset += (r.TotalInstructions - r.clockReadInstrs) / ktfBusyWaitInstrsPerMS
	}
	r.clockReadInstrs = r.TotalInstructions
	return r.TickMS + r.clockReadOffset
}

func ktfKernelCurrentTime(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	now := runtime.monotonicReadMS()
	if err := runtime.CPU.WriteRegister(
		cpu.RegisterR1,
		uint32(now>>32),
	); err != nil {
		return 0, err
	}
	return uint32(now), nil
}

func ktfKernelGetSystemProperty(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	keyAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	capacity, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if keyAddress == 0 || output == 0 || capacity == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	key, err := runtime.readCString(keyAddress, 256)
	if err != nil {
		return 0, err
	}
	value, supported := runtime.wipicSystemProperty(key)
	runtime.tracef(
		"wipic_system_property_get:%s=%q:supported=%t",
		key,
		value,
		supported,
	)
	if !supported {
		return ^uint32(6), nil
	}
	if uint64(len(value))+1 > uint64(capacity) {
		return ktfWIPICErrorShortBuf, nil
	}
	if err := runtime.CPU.WriteMemory(
		output,
		append([]byte(value), 0),
	); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfKernelSetSystemProperty(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	keyAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	valueAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if keyAddress == 0 || valueAddress == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	key, err := runtime.readCString(keyAddress, 256)
	if err != nil {
		return 0, err
	}
	value, err := runtime.readCString(valueAddress, 4096)
	if err != nil {
		return 0, err
	}
	runtime.wipicSystemProperties[strings.ToUpper(strings.TrimSpace(key))] = value
	runtime.tracef("wipic_system_property_set:%s=%q", key, value)
	return 0, nil
}

func (r *Runtime) wipicSystemProperty(key string) (string, bool) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if value, ok := r.wipicSystemProperties[key]; ok {
		return value, true
	}
	if value := r.handsetSystemProperty(key); value != "" {
		return value, true
	}
	switch key {
	case "ESN":
		return "00000000", true
	case "NID", "SID", "BASEID", "BASELAT", "BASELONG", "CURRENTCH":
		return "0", true
	case "PHONENUMBER":
		return r.Services.Device.Config().PhoneNumber, true
	case "WIPIVERSION":
		return r.Services.Device.Config().WIPIVersion, true
	case "RSSILEVEL":
		_, signal, _ := r.Services.Device.Status()
		return strconv.Itoa(int(signal) * 5 / 100), true
	case "BATTERYLEVEL":
		return r.batteryLevelSystemProperty(), true
	case "MAXRSSILEVEL", "MAXBATTLEVEL":
		return "5", true
	case "MAXSERIALNUM":
		return "0", true
	case "MAXSOCKETNUM":
		return strconv.FormatUint(
			uint64(r.Services.Config.Limits.Network.MaxSockets),
			10,
		), true
	case "MEDIADEVICES":
		return "audio/MIDI,audio/MP3", true
	case "DNS":
		return "127.0.0.1", true
	case "TIMEZONE":
		minutes := r.Services.Device.Config().TimezoneMins
		sign := "+"
		if minutes < 0 {
			sign = "-"
			minutes = -minutes
		}
		return fmt.Sprintf(
			"GMT%s%02d:%02d",
			sign,
			minutes/60,
			minutes%60,
		), true
	case "KEYREPEAT":
		return "600:250", true
	case "VIBRATORLEVEL":
		return "1", true
	case "VOLUMELEVEL":
		return "10", true
	default:
		return "", false
	}
}

func (r *Runtime) batteryLevelSystemProperty() string {
	if r.Services == nil || r.Services.Device == nil {
		return "5"
	}
	battery, _, _ := r.Services.Device.Status()
	return strconv.Itoa(int(battery) * 5 / 100)
}

func ktfGetResourceID(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	sizeAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if nameAddress == 0 || sizeAddress == 0 {
		return ^uint32(7), nil
	}
	name, err := runtime.readCString(nameAddress, 4096)
	if err != nil {
		return 0, err
	}
	resource, ok := runtime.findKTFResource(name)
	if !ok {
		runtime.trace("wipic_resource_missing:" + name)
		return ^uint32(1), nil
	}
	if err := runtime.WriteU32(sizeAddress, uint32(len(resource))); err != nil {
		return 0, err
	}
	key := strings.ToLower(strings.ReplaceAll(path.Clean(name), `\`, "/"))
	id := runtime.wipicResourceIDs[key]
	if id == 0 {
		id = uint32(len(runtime.wipicResources) + 1)
		for runtime.wipicResources[id] != nil {
			id++
		}
		runtime.wipicResourceIDs[key] = id
		runtime.wipicResources[id] = resource
	}
	runtime.tracef(
		"wipic_resource_id:%s:id=%d:size=%d:lr=0x%08x",
		name,
		id,
		len(resource),
		mustKTFRegister(runtime, cpu.RegisterLR),
	)
	return id, nil
}

func ktfGetResource(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	id, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	resource := runtime.wipicResources[id]
	if resource == nil || output == 0 {
		return ^uint32(1), nil
	}
	output, size, err = runtime.resolveKTFResourceOutput(output, size)
	if err != nil {
		return 0, err
	}
	if uint64(size) < uint64(len(resource)) {
		return ^uint32(10), nil
	}
	if err := runtime.CPU.WriteMemory(output, resource); err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_resource_read:id=%d:size=%d:lr=0x%08x",
		id,
		len(resource),
		mustKTFRegister(runtime, cpu.RegisterLR),
	)
	return 0, nil
}

func mustKTFRegister(runtime *Runtime, register uint32) uint32 {
	value, _ := runtime.CPU.ReadRegister(register)
	return value
}

func (r *Runtime) resolveKTFResourceOutput(
	output, size uint32,
) (uint32, uint32, error) {
	if allocation, ok := r.wipicMemory[output]; ok {
		if size > allocation.size {
			size = allocation.size
		}
		return allocation.data, size, nil
	}
	// KTF's C support library can suballocate an INDIRECT_BUF from the large
	// arena returned by MC_knlCalloc. In that form the resource argument is a
	// one-word handle followed immediately by the two-word buffer head:
	//
	//   handle -> head (= handle+4), head words, payload (= head+8)
	//
	// These handles are invisible to wipicMemory because the guest allocator
	// creates them without another kernel call. A plain resource destination
	// remains valid, so only resolve the carrier library's exact self-relative
	// layout instead of treating every unknown pointer as an indirect handle.
	if output <= ^uint32(0)-4 {
		head, err := r.ReadU32(output)
		if err != nil {
			return 0, 0, err
		}
		if head == output+4 {
			if head > ^uint32(0)-8 {
				return 0, 0, errors.New(
					"KTF resource indirect buffer address overflows",
				)
			}
			return head + 8, size, nil
		}
	}
	return output, size, nil
}
