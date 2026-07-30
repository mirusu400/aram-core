package application

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *wipiRuntime) dispatchKernel(name string) (wipiReturn, bool, error) {
	args, err := r.args(4)
	if err != nil {
		return wipiReturn{}, true, err
	}
	a0, a1, a2 := args[0], args[1], args[2]
	switch name {
	case "MC_knlAlloc", "MC_knlCalloc":
		address, err := r.heap.allocate(a0, name == "MC_knlCalloc")
		return wipiReturn{low: address}, true, err
	case "MC_knlFree":
		r.heap.release(a0)
		return wipiReturn{}, true, nil
	case "MC_knlGetTotalMemory":
		return wipiReturn{low: guestHeapSize}, true, nil
	case "MC_knlGetFreeMemory":
		var total uint32
		for _, block := range r.heap.free {
			total += block.size
		}
		return wipiReturn{low: total}, true, nil
	case "MC_knlPrintk":
		if a0 == 0 {
			return wipiReturn{low: wipiReturnCode(wipiInvalid)}, true, nil
		}
		format, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		message, err := r.formatPrintf(format, 1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		r.logs = append(r.logs, string(message))
		return wipiReturn{low: uint32(len(message))}, true, nil
	case "MC_knlSprintk":
		if a0 == 0 || a1 == 0 {
			return wipiReturn{low: wipiReturnCode(wipiInvalid)}, true, nil
		}
		format, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		message, err := r.formatPrintf(format, 2)
		if err != nil {
			return wipiReturn{}, true, err
		}
		count, err := r.writeCString(a0, message, -1)
		return wipiReturn{low: count}, true, err
	case "MC_knlExit":
		r.exitRequested = true
		r.exitCode = int32(a0)
		return wipiReturn{}, true, nil
	case "MC_knlGetExecNames":
		outputSize, err := r.arg(4)
		if err != nil {
			return wipiReturn{}, true, err
		}
		result, err := r.getExecNames(a0, a1, a2, args[3], int32(outputSize))
		return wipiReturn{low: wipiReturnCode(result)}, true, err
	case "MC_knlExecute":
		result, err := r.executeProgram(a0, int32(a1))
		return wipiReturn{low: wipiReturnCode(result)}, true, err
	case "MC_knlProgramStop":
		return wipiReturn{low: wipiReturnCode(r.stopProgram(int32(a0)))}, true, nil
	case "MC_knlGetCurProgramID":
		return wipiReturn{low: uint32(r.currentProgram)}, true, nil
	case "MC_knlGetParentProgramID":
		if current := r.programs[r.currentProgram]; current != nil {
			return wipiReturn{low: uint32(current.parentID)}, true, nil
		}
		return wipiReturn{}, true, nil
	case "MC_knlGetAppManagerID":
		return wipiReturn{low: uint32(r.appManager)}, true, nil
	case "MC_knlGetAccessLevel":
		if current := r.programs[r.currentProgram]; current != nil {
			return wipiReturn{low: uint32(current.accessLevel)}, true, nil
		}
		return wipiReturn{low: uint32(wipiDefaultAccessLevel)}, true, nil
	case "MC_knlGetProgramName":
		result, err := r.getProgramName(a0, int32(a1))
		return wipiReturn{low: wipiReturnCode(result)}, true, err
	case "MC_knlGetProgramInfo":
		result, err := r.getProgramInfo(a0, int32(a1))
		return wipiReturn{low: wipiReturnCode(result)}, true, err
	case "MC_knlGetResourceID":
		if a0 == 0 || a1 == 0 {
			return wipiReturn{low: wipiReturnCode(wipiInvalid)}, true, nil
		}
		name, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		resource := r.resources[string(name)]
		if resource == nil {
			if err := r.writeU32(a1, 0); err != nil {
				return wipiReturn{}, true, err
			}
			return wipiReturn{low: wipiReturnCode(wipiNoEntry)}, true, nil
		}
		if err := r.writeU32(a1, uint32(len(resource.data))); err != nil {
			return wipiReturn{}, true, err
		}
		return wipiReturn{low: uint32(resource.id)}, true, nil
	case "MC_knlGetResource":
		size := int32(a2)
		name, ok := r.resourceIDs[int32(a0)]
		resource := r.resources[name]
		if !ok || resource == nil {
			return wipiReturn{low: wipiReturnCode(wipiNoEntry)}, true, nil
		}
		if a1 == 0 || size < 0 {
			return wipiReturn{low: wipiReturnCode(wipiInvalid)}, true, nil
		}
		data, serviceErr := r.services.Storage.ReadFile(
			shared.NamespacePackage,
			resource.name,
		)
		if serviceErr != nil {
			return wipiReturn{low: wipiReturnCode(wipiNoEntry)}, true, nil
		}
		if int(size) < len(data) {
			return wipiReturn{low: wipiReturnCode(wipiShortBuffer)}, true, nil
		}
		if err := r.cpu.WriteMemory(a1, data); err != nil {
			return wipiReturn{}, true, err
		}
		return wipiReturn{}, true, nil
	case "MC_knlCreateSharedBuf":
		key, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		if existing := r.shared[string(key)]; existing != 0 {
			return wipiReturn{}, true, nil
		}
		address, err := r.heap.allocate(max(a1, 1), true)
		if err != nil {
			return wipiReturn{}, true, err
		}
		r.shared[string(key)] = address
		r.sharedSizes[address] = max(a1, 1)
		return wipiReturn{low: address}, true, nil
	case "MC_knlGetSharedBuf":
		key, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		return wipiReturn{low: r.shared[string(key)]}, true, nil
	case "MC_knlGetSharedBufSize":
		size, ok := r.sharedSizes[a0]
		if !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{low: size}, true, nil
	case "MC_knlDestroySharedBuf":
		if _, ok := r.sharedSizes[a0]; !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		for key, address := range r.shared {
			if address == a0 {
				delete(r.shared, key)
			}
		}
		delete(r.sharedSizes, a0)
		r.heap.release(a0)
		return wipiReturn{}, true, nil
	case "MC_knlResizeSharedBuf":
		return r.resizeSharedBuffer(a0, a1)
	case "MC_knlDefTimer":
		if a0 == 0 {
			return wipiReturn{}, true, nil
		}
		var timer [28]byte
		binary.LittleEndian.PutUint32(timer[:], a1)
		return wipiReturn{}, true, r.cpu.WriteMemory(a0, timer[:])
	case "MC_knlSetTimer":
		parameter, err := r.arg(4)
		if err != nil {
			return wipiReturn{}, true, err
		}
		timeout := uint64(args[2]) | uint64(args[3])<<32
		if a0 == 0 || int64(timeout) < 0 ||
			timeout > uint64((time.Duration(1<<63-1)-r.services.Clock.Monotonic())/time.Millisecond) {
			return wipiReturn{low: wipiReturnCode(wipiInvalid)}, true, nil
		}
		if _, active := r.timers[a0]; active {
			return wipiReturn{low: wipiReturnCode(wipiExists)}, true, nil
		}
		callback, err := r.readU32(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		if callback == 0 {
			return wipiReturn{low: wipiReturnCode(wipiInvalid)}, true, nil
		}
		if err := r.writeU32(a0+4, parameter); err != nil {
			return wipiReturn{}, true, err
		}
		if err := r.writeU64(a0+8, timeout); err != nil {
			return wipiReturn{}, true, err
		}
		if err := r.writeU64(a0+16, r.tickMS+timeout); err != nil {
			return wipiReturn{}, true, err
		}
		if err := r.writeU32(a0+24, 1); err != nil {
			return wipiReturn{}, true, err
		}
		serviceID := r.timerServices[a0]
		if serviceID == 0 {
			serviceID, err = r.services.Timers.Define(
				r.serviceOwner,
				fmt.Sprintf("wipi.timer.%08x", a0),
			)
			if err != nil {
				return wipiReturn{low: wipiReturnCode(wipiNoMemory)}, true, nil
			}
			r.timerServices[a0] = serviceID
		}
		deadline := r.services.Clock.Monotonic() +
			time.Duration(timeout)*time.Millisecond
		if err := r.services.Timers.Set(
			serviceID,
			r.serviceOwner,
			deadline,
			0,
			int64(a0),
		); err != nil {
			return wipiReturn{}, true, err
		}
		r.timers[a0] = wipiTimer{callback: callback, parameter: parameter, deadline: r.tickMS + timeout}
		return wipiReturn{}, true, nil
	case "MC_knlUnsetTimer":
		delete(r.timers, a0)
		if serviceID := r.timerServices[a0]; serviceID != 0 {
			if err := r.services.Timers.Cancel(serviceID, r.serviceOwner); err != nil &&
				!errors.Is(err, shared.ErrNotFound) {
				return wipiReturn{}, true, err
			}
		}
		if a0 != 0 {
			return wipiReturn{}, true, r.writeU32(a0+24, 0)
		}
		return wipiReturn{}, true, nil
	case "MC_knlCurrentTime":
		return wipiU64(
			uint64(r.services.Clock.Monotonic() / time.Millisecond),
		), true, nil
	case "MC_knlGetSystemProperty":
		return r.getSystemProperty(a0, a1, a2)
	case "MC_knlSetSystemProperty":
		key, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		value, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		r.properties[string(key)] = append([]byte(nil), value...)
		return wipiReturn{}, true, nil
	default:
		return wipiReturn{}, false, nil
	}
}

func (r *wipiRuntime) resizeSharedBuffer(address, size uint32) (wipiReturn, bool, error) {
	oldSize, ok := r.sharedSizes[address]
	if !ok {
		return wipiReturn{}, true, nil
	}
	replacement, err := r.heap.allocate(size, true)
	if err != nil || replacement == 0 {
		return wipiReturn{}, true, err
	}
	data := make([]byte, min(oldSize, size))
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return wipiReturn{}, true, err
	}
	if err := r.cpu.WriteMemory(replacement, data); err != nil {
		return wipiReturn{}, true, err
	}
	r.heap.release(address)
	for key, current := range r.shared {
		if current == address {
			r.shared[key] = replacement
		}
	}
	delete(r.sharedSizes, address)
	r.sharedSizes[replacement] = size
	return wipiReturn{low: replacement}, true, nil
}

func (r *wipiRuntime) getSystemProperty(keyAddress, output, size uint32) (wipiReturn, bool, error) {
	key, err := r.readCString(keyAddress)
	if err != nil {
		return wipiReturn{}, true, err
	}
	value, ok := r.properties[string(key)]
	if !ok {
		defaults := map[string][]byte{
			"microedition.platform": []byte("ARAM WIPI"),
			"wipi.version":          []byte("1.2.1"),
			"device.model":          []byte("generic"),
			"SCREENWIDTH":           []byte(fmt.Sprint(r.frame.Bounds().Dx())),
			"SCREENHEIGHT":          []byte(fmt.Sprint(r.frame.Bounds().Dy())),
			"MAXSERIALNUM":          []byte("0"),
		}
		value = defaults[string(key)]
	}
	if size == 0 {
		return wipiReturn{}, true, nil
	}
	count := len(value)
	if count >= int(size) {
		count = int(size) - 1
	}
	data := append(append([]byte(nil), value[:count]...), 0)
	if err := r.cpu.WriteMemory(output, data); err != nil {
		return wipiReturn{}, true, err
	}
	return wipiReturn{low: uint32(count)}, true, nil
}

func modeledKernelAPIs() []string {
	return []string{
		"MC_knlAlloc", "MC_knlCalloc", "MC_knlFree", "MC_knlGetTotalMemory",
		"MC_knlGetFreeMemory", "MC_knlPrintk", "MC_knlSprintk", "MC_knlExit",
		"MC_knlGetExecNames", "MC_knlExecute", "MC_knlProgramStop",
		"MC_knlGetCurProgramID", "MC_knlGetParentProgramID", "MC_knlGetAppManagerID",
		"MC_knlGetAccessLevel", "MC_knlGetProgramName", "MC_knlGetProgramInfo",
		"MC_knlGetResourceID", "MC_knlGetResource", "MC_knlCreateSharedBuf",
		"MC_knlGetSharedBuf", "MC_knlGetSharedBufSize", "MC_knlDestroySharedBuf",
		"MC_knlResizeSharedBuf", "MC_knlDefTimer", "MC_knlSetTimer",
		"MC_knlUnsetTimer", "MC_knlCurrentTime", "MC_knlGetSystemProperty",
		"MC_knlSetSystemProperty",
	}
}
