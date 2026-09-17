package brewrt

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

const (
	moduleBase = uint32(0x01000000)
	helperBase = uint32(0x02000000)
	allocTrap  = helperBase + 0x800
	returnTrap = helperBase + 0x802
	shellTrap  = helperBase + 0x804
	freeTrap   = helperBase + 0x806
	heapBase   = uint32(0x03000000)
	heapSize   = uint32(0x00010000)
	stackBase  = uint32(0x04000000)
	stackSize  = uint32(0x00010000)
	outputAddr = stackBase + 0x100

	bootstrapBudget = uint64(2_000_000)
)

// Runtime executes the authenticated module's real offset-zero ARM entry with
// the portable interpreter. It supplies only the loader prefix and allocator
// contract proven by static analysis, then stops before constructing an applet.
type Runtime struct {
	cpu          cpu.Backend
	moduleObject uint32
	heapNext     uint32
	boundary     *ExecutionBoundaryError
}

// ExecutionBoundaryError identifies the first unimplemented proprietary ABI
// request without pretending that a package splash is a guest-rendered frame.
type ExecutionBoundaryError struct {
	ClassID       uint32
	GuestReturnPC uint32
}

func (e *ExecutionBoundaryError) Error() string {
	return fmt.Sprintf(
		"BREW execution boundary: shell CreateInstance class 0x%08x is not implemented (guest return PC 0x%08x)",
		e.ClassID,
		e.GuestReturnPC,
	)
}

func New(pkg Package) (*Runtime, error) {
	if len(pkg.Module) == 0 {
		return nil, fmt.Errorf("BREW module is empty")
	}
	backend := interpreter.New()
	r := &Runtime{cpu: backend, heapNext: heapBase}
	if err := r.mapImage(pkg.Module); err != nil {
		_ = backend.Close()
		return nil, err
	}
	return r, nil
}

func (r *Runtime) mapImage(module []byte) error {
	imageBase := moduleBase - 8
	imageData := make([]byte, len(module)+8)
	binary.LittleEndian.PutUint32(imageData[0:4], 0x00010000)
	binary.LittleEndian.PutUint32(imageData[4:8], helperBase)
	copy(imageData[8:], module)
	if err := r.cpu.Map(imageBase, uint32(len(imageData)), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW module: %w", err)
	}
	if err := r.cpu.WriteMemory(imageBase, imageData); err != nil {
		return fmt.Errorf("write BREW module: %w", err)
	}
	if err := r.cpu.Map(helperBase, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW loader helper: %w", err)
	}
	var helper [0x1000]byte
	binary.LittleEndian.PutUint32(helper[0x68:], allocTrap|1)
	binary.LittleEndian.PutUint32(helper[0x6c:], freeTrap|1)
	binary.LittleEndian.PutUint16(helper[0x800:], 0xbe00)
	binary.LittleEndian.PutUint16(helper[0x802:], 0xbe01)
	binary.LittleEndian.PutUint16(helper[0x804:], 0xbe02)
	binary.LittleEndian.PutUint16(helper[0x806:], 0xbe03)
	// Minimal IShell object. Only CreateInstance is installed because probing
	// stops at that call rather than inventing an unknown service object.
	binary.LittleEndian.PutUint32(helper[0x100:], helperBase+0x200)
	binary.LittleEndian.PutUint32(helper[0x208:], shellTrap|1)
	if err := r.cpu.WriteMemory(helperBase, helper[:]); err != nil {
		return fmt.Errorf("write BREW loader helper: %w", err)
	}
	if err := r.cpu.Map(heapBase, heapSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return fmt.Errorf("map BREW bootstrap heap: %w", err)
	}
	if err := r.cpu.Map(stackBase, stackSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return fmt.Errorf("map BREW bootstrap stack: %w", err)
	}
	return nil
}

// ProbeAppletBoundary calls the authenticated module's real CreateInstance
// method and executes the applet constructor until its first unknown shell
// dependency. Reaching this error is the current honest execution milestone.
func (r *Runtime) ProbeAppletBoundary(ctx context.Context) error {
	if r.boundary != nil {
		return r.boundary
	}
	if r.moduleObject == 0 {
		return fmt.Errorf("probe BREW applet before module bootstrap")
	}
	var encoded [4]byte
	if err := r.cpu.ReadMemory(r.moduleObject, encoded[:]); err != nil {
		return fmt.Errorf("read BREW module vtable: %w", err)
	}
	vtable := binary.LittleEndian.Uint32(encoded[:])
	if err := r.cpu.ReadMemory(vtable+8, encoded[:]); err != nil {
		return fmt.Errorf("read BREW module CreateInstance: %w", err)
	}
	createInstance := binary.LittleEndian.Uint32(encoded[:])
	if createInstance == 0 {
		return fmt.Errorf("BREW module CreateInstance is null")
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: r.moduleObject,
		cpu.RegisterR1: helperBase + 0x100,
		cpu.RegisterR2: ClassID,
		cpu.RegisterR3: outputAddr + 4,
		cpu.RegisterSP: stackBase + stackSize - 16,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := r.cpu.WriteRegister(register, value); err != nil {
			return fmt.Errorf("initialize BREW applet register r%d: %w", register, err)
		}
	}
	pc, mode := branchTarget(createInstance)
	for traps := 0; traps < 16; traps++ {
		result := r.cpu.Run(ctx, pc, mode, bootstrapBudget)
		if result.Err != nil {
			return fmt.Errorf("execute BREW applet constructor at PC 0x%08x: %w", result.PC, result.Err)
		}
		if result.Reason != cpu.StopBreakpoint {
			return fmt.Errorf("execute BREW applet constructor stopped at PC 0x%08x with reason %d", result.PC, result.Reason)
		}
		switch result.PC {
		case allocTrap + 2:
			if err := r.returnAllocation(); err != nil {
				return err
			}
			lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
			if err != nil {
				return fmt.Errorf("read BREW allocator return address: %w", err)
			}
			pc, mode = branchTarget(lr)
		case shellTrap + 2:
			classID, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return fmt.Errorf("read BREW shell class ID: %w", err)
			}
			returnAddress, err := r.cpu.ReadRegister(cpu.RegisterLR)
			if err != nil {
				return fmt.Errorf("read BREW shell return address: %w", err)
			}
			r.boundary = &ExecutionBoundaryError{
				ClassID:       classID,
				GuestReturnPC: returnAddress &^ 1,
			}
			return r.boundary
		case freeTrap + 2:
			return fmt.Errorf("BREW applet constructor released an object before its first shell dependency")
		case returnTrap + 2:
			return fmt.Errorf("BREW applet constructor returned before its researched shell dependency")
		default:
			return fmt.Errorf("unexpected BREW applet breakpoint at PC 0x%08x", result.PC-2)
		}
	}
	return fmt.Errorf("BREW applet probe exceeded host-call limit")
}

// Bootstrap executes the genuine module entry and module factory. A successful
// result proves executable ARM code was reached and produced its module object.
func (r *Runtime) Bootstrap(ctx context.Context) error {
	if r.moduleObject != 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for register, value := range map[uint32]uint32{
		// The offset-zero veneer reshuffles these into the generic module
		// constructor as shell and loader-helper arguments respectively.
		cpu.RegisterR0: helperBase + 0x100,
		cpu.RegisterR1: helperBase,
		cpu.RegisterR2: outputAddr,
		cpu.RegisterSP: stackBase + stackSize - 16,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := r.cpu.WriteRegister(register, value); err != nil {
			return fmt.Errorf("initialize BREW register r%d: %w", register, err)
		}
	}
	var zero [4]byte
	if err := r.cpu.WriteMemory(outputAddr, zero[:]); err != nil {
		return fmt.Errorf("clear BREW module output: %w", err)
	}

	pc, mode := moduleBase, cpu.ModeARM
	for traps := 0; traps < 16; traps++ {
		result := r.cpu.Run(ctx, pc, mode, bootstrapBudget)
		if result.Err != nil {
			return fmt.Errorf("execute BREW module entry at PC 0x%08x: %w", result.PC, result.Err)
		}
		if result.Reason != cpu.StopBreakpoint {
			return fmt.Errorf("execute BREW module entry stopped at PC 0x%08x with reason %d", result.PC, result.Reason)
		}
		switch result.PC {
		case allocTrap + 2:
			if err := r.returnAllocation(); err != nil {
				return err
			}
			lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
			if err != nil {
				return fmt.Errorf("read BREW allocator return address: %w", err)
			}
			pc, mode = branchTarget(lr)
		case returnTrap + 2:
			var encoded [4]byte
			if err := r.cpu.ReadMemory(outputAddr, encoded[:]); err != nil {
				return fmt.Errorf("read BREW module object: %w", err)
			}
			r.moduleObject = binary.LittleEndian.Uint32(encoded[:])
			if r.moduleObject < heapBase || r.moduleObject >= heapBase+heapSize {
				return fmt.Errorf("BREW module factory returned invalid object 0x%08x", r.moduleObject)
			}
			return nil
		default:
			return fmt.Errorf("unexpected BREW bootstrap breakpoint at PC 0x%08x", result.PC-2)
		}
	}
	return fmt.Errorf("BREW bootstrap exceeded host-call limit")
}

func (r *Runtime) returnAllocation() error {
	size, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW allocation size: %w", err)
	}
	size = (size + 7) &^ 7
	if size == 0 || size > heapBase+heapSize-r.heapNext {
		return fmt.Errorf("BREW bootstrap allocation %d exceeds heap", size)
	}
	address := r.heapNext
	r.heapNext += size
	if err := r.cpu.WriteRegister(cpu.RegisterR0, address); err != nil {
		return fmt.Errorf("return BREW allocation: %w", err)
	}
	return nil
}

func branchTarget(address uint32) (uint32, cpu.Mode) {
	if address&1 != 0 {
		return address &^ 1, cpu.ModeThumb
	}
	return address &^ 3, cpu.ModeARM
}

func (r *Runtime) ModuleObject() uint32 { return r.moduleObject }

func (r *Runtime) Close() error {
	if r == nil || r.cpu == nil {
		return nil
	}
	return r.cpu.Close()
}
