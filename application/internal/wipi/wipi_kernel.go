package wipi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) dispatchKernel(name string) (guest.WIPIReturn, bool, error) {
	args, err := r.args(4)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	a0, a1, a2 := args[0], args[1], args[2]
	switch name {
	case "MC_knlAlloc", "MC_knlCalloc":
		address, err := r.Heap.Allocate(a0, name == "MC_knlCalloc")
		return guest.WIPIReturn{Low: address}, true, err
	case "MC_knlFree":
		r.Heap.Release(a0)
		return guest.WIPIReturn{}, true, nil
	case "MC_knlGetTotalMemory":
		return guest.WIPIReturn{Low: guest.HeapSize}, true, nil
	case "MC_knlGetFreeMemory":
		var total uint32
		for _, block := range r.Heap.Free {
			total += block.Size
		}
		return guest.WIPIReturn{Low: total}, true, nil
	case "MC_knlPrintk":
		if a0 == 0 {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIInvalid)}, true, nil
		}
		format, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		message, err := r.formatPrintf(format, 1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		r.Logs = append(r.Logs, string(message))
		return guest.WIPIReturn{Low: uint32(len(message))}, true, nil
	case "MC_knlSprintk":
		if a0 == 0 || a1 == 0 {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIInvalid)}, true, nil
		}
		format, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		message, err := r.formatPrintf(format, 2)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		count, err := r.writeCString(a0, message, -1)
		return guest.WIPIReturn{Low: count}, true, err
	case "MC_knlExit":
		r.ExitRequested = true
		r.exitCode = int32(a0)
		return guest.WIPIReturn{}, true, nil
	case "MC_knlGetExecNames":
		outputSize, err := r.arg(4)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		result, err := r.getExecNames(a0, a1, a2, args[3], int32(outputSize))
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(result)}, true, err
	case "MC_knlExecute":
		result, err := r.executeProgram(a0, int32(a1))
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(result)}, true, err
	case "MC_knlProgramStop":
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(r.stopProgram(int32(a0)))}, true, nil
	case "MC_knlGetCurProgramID":
		return guest.WIPIReturn{Low: uint32(r.CurrentProgram)}, true, nil
	case "MC_knlGetParentProgramID":
		if current := r.Programs[r.CurrentProgram]; current != nil {
			return guest.WIPIReturn{Low: uint32(current.ParentID)}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_knlGetAppManagerID":
		return guest.WIPIReturn{Low: uint32(r.appManager)}, true, nil
	case "MC_knlGetAccessLevel":
		if current := r.Programs[r.CurrentProgram]; current != nil {
			return guest.WIPIReturn{Low: uint32(current.accessLevel)}, true, nil
		}
		return guest.WIPIReturn{Low: uint32(DefaultAccessLevel)}, true, nil
	case "MC_knlGetProgramName":
		result, err := r.getProgramName(a0, int32(a1))
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(result)}, true, err
	case "MC_knlGetProgramInfo":
		result, err := r.getProgramInfo(a0, int32(a1))
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(result)}, true, err
	case "MC_knlGetResourceID":
		if a0 == 0 || a1 == 0 {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIInvalid)}, true, nil
		}
		name, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		resource := r.Resources[string(name)]
		if resource == nil {
			if err := r.WriteU32(a1, 0); err != nil {
				return guest.WIPIReturn{}, true, err
			}
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPINoEntry)}, true, nil
		}
		if err := r.WriteU32(a1, uint32(len(resource.Data))); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{Low: uint32(resource.Id)}, true, nil
	case "MC_knlGetResource":
		size := int32(a2)
		name, ok := r.ResourceIDs[int32(a0)]
		resource := r.Resources[name]
		if !ok || resource == nil {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPINoEntry)}, true, nil
		}
		if a1 == 0 || size < 0 {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIInvalid)}, true, nil
		}
		data, serviceErr := r.Services.Storage.ReadFile(
			shared.NamespacePackage,
			resource.name,
		)
		if serviceErr != nil {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPINoEntry)}, true, nil
		}
		if int(size) < len(data) {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIShortBuffer)}, true, nil
		}
		if err := r.CPU.WriteMemory(a1, data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_knlCreateSharedBuf":
		key, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		if existing := r.shared[string(key)]; existing != 0 {
			return guest.WIPIReturn{}, true, nil
		}
		address, err := r.Heap.Allocate(max(a1, 1), true)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		r.shared[string(key)] = address
		r.sharedSizes[address] = max(a1, 1)
		return guest.WIPIReturn{Low: address}, true, nil
	case "MC_knlGetSharedBuf":
		key, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{Low: r.shared[string(key)]}, true, nil
	case "MC_knlGetSharedBufSize":
		size, ok := r.sharedSizes[a0]
		if !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{Low: size}, true, nil
	case "MC_knlDestroySharedBuf":
		if _, ok := r.sharedSizes[a0]; !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		for key, address := range r.shared {
			if address == a0 {
				delete(r.shared, key)
			}
		}
		delete(r.sharedSizes, a0)
		r.Heap.Release(a0)
		return guest.WIPIReturn{}, true, nil
	case "MC_knlResizeSharedBuf":
		return r.resizeSharedBuffer(a0, a1)
	case "MC_knlDefTimer":
		if a0 == 0 {
			return guest.WIPIReturn{}, true, nil
		}
		var timer [28]byte
		binary.LittleEndian.PutUint32(timer[:], a1)
		return guest.WIPIReturn{}, true, r.CPU.WriteMemory(a0, timer[:])
	case "MC_knlSetTimer":
		parameter, err := r.arg(4)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		timeout := uint64(args[2]) | uint64(args[3])<<32
		result, err := r.SetTimer(a0, timeout, parameter, true)
		return result, true, err
	case "MC_knlUnsetTimer":
		return guest.WIPIReturn{}, true, r.UnsetTimer(a0, true)
	case "MC_knlCurrentTime":
		return wipiU64(
			uint64(r.Services.Clock.Monotonic() / time.Millisecond),
		), true, nil
	case "MC_knlGetSystemProperty":
		return r.getSystemProperty(a0, a1, a2)
	case "MC_knlSetSystemProperty":
		key, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		value, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		r.properties[string(key)] = append([]byte(nil), value...)
		return guest.WIPIReturn{}, true, nil
	default:
		return guest.WIPIReturn{}, false, nil
	}
}

func (r *Runtime) SetTimer(
	address uint32,
	timeout uint64,
	parameter uint32,
	writeGuestState bool,
) (guest.WIPIReturn, error) {
	if address == 0 || int64(timeout) < 0 ||
		timeout > uint64((time.Duration(1<<63-1)-r.Services.Clock.Monotonic())/time.Millisecond) {
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIInvalid)}, nil
	}
	if _, active := r.Timers[address]; active {
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIExists)}, nil
	}
	callback, err := r.ReadU32(address)
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	if callback == 0 {
		return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIInvalid)}, nil
	}
	if writeGuestState {
		if err := r.WriteU32(address+4, parameter); err != nil {
			return guest.WIPIReturn{}, err
		}
		if err := r.writeU64(address+8, timeout); err != nil {
			return guest.WIPIReturn{}, err
		}
		if err := r.writeU64(address+16, r.TickMS+timeout); err != nil {
			return guest.WIPIReturn{}, err
		}
		if err := r.WriteU32(address+24, 1); err != nil {
			return guest.WIPIReturn{}, err
		}
	}
	serviceID := r.TimerServices[address]
	if serviceID == 0 {
		serviceID, err = r.Services.Timers.Define(
			r.ServiceOwner,
			fmt.Sprintf("wipi.timer.%08x", address),
		)
		if err != nil {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPINoMemory)}, nil
		}
		r.TimerServices[address] = serviceID
	}
	deadline := r.Services.Clock.Monotonic() +
		time.Duration(timeout)*time.Millisecond
	if err := r.Services.Timers.Set(
		serviceID,
		r.ServiceOwner,
		deadline,
		0,
		int64(address),
	); err != nil {
		return guest.WIPIReturn{}, err
	}
	r.Timers[address] = wipiTimer{
		Callback:  callback,
		Parameter: parameter,
		Deadline:  r.TickMS + timeout,
	}
	return guest.WIPIReturn{}, nil
}

func (r *Runtime) UnsetTimer(address uint32, writeGuestState bool) error {
	delete(r.Timers, address)
	if serviceID := r.TimerServices[address]; serviceID != 0 {
		if err := r.Services.Timers.Cancel(serviceID, r.ServiceOwner); err != nil &&
			!errors.Is(err, shared.ErrNotFound) {
			return err
		}
	}
	if address != 0 && writeGuestState {
		return r.WriteU32(address+24, 0)
	}
	return nil
}

func (r *Runtime) resizeSharedBuffer(address, size uint32) (guest.WIPIReturn, bool, error) {
	oldSize, ok := r.sharedSizes[address]
	if !ok {
		return guest.WIPIReturn{}, true, nil
	}
	replacement, err := r.Heap.Allocate(size, true)
	if err != nil || replacement == 0 {
		return guest.WIPIReturn{}, true, err
	}
	data := make([]byte, min(oldSize, size))
	if err := r.CPU.ReadMemory(address, data); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	if err := r.CPU.WriteMemory(replacement, data); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	r.Heap.Release(address)
	for key, current := range r.shared {
		if current == address {
			r.shared[key] = replacement
		}
	}
	delete(r.sharedSizes, address)
	r.sharedSizes[replacement] = size
	return guest.WIPIReturn{Low: replacement}, true, nil
}

func (r *Runtime) getSystemProperty(keyAddress, output, size uint32) (guest.WIPIReturn, bool, error) {
	key, err := r.ReadCString(keyAddress)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	value, ok := r.properties[string(key)]
	if !ok {
		defaults := map[string][]byte{
			"microedition.platform": []byte("ARAM WIPI"),
			"wipi.version":          []byte("1.2.1"),
			"device.model":          []byte("generic"),
			"SCREENWIDTH":           []byte(fmt.Sprint(r.Frame.Bounds().Dx())),
			"SCREENHEIGHT":          []byte(fmt.Sprint(r.Frame.Bounds().Dy())),
			"MAXSERIALNUM":          []byte("0"),
		}
		value = defaults[string(key)]
	}
	if size == 0 {
		return guest.WIPIReturn{}, true, nil
	}
	count := len(value)
	if count >= int(size) {
		count = int(size) - 1
	}
	data := append(append([]byte(nil), value[:count]...), 0)
	if err := r.CPU.WriteMemory(output, data); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	return guest.WIPIReturn{Low: uint32(count)}, true, nil
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
