package ktf

import (
	"context"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	mnContextExceptionFrameField     = 0x2c
	mnContextExceptionFunctionsField = 0x30
)

// MN setjmp glue stores the linked exception frame at [r11+0x2c] and copies
// [r11+0x30] into the frame's restore-functions field. Its six-word header is
// method, receiver, previous, thrown object, bytecode PC, restore functions.
// Alias its environment view so
// dispatch and task switching use the guest's real frame head, not a detached
// zero-filled environment.
func (r *Runtime) prepareMNExceptions(contextAddress uint32) error {
	r.exceptionContext = contextAddress + mnContextExceptionFrameField - 8*4
	functions, err := r.AllocateWords(2)
	if err != nil {
		return err
	}
	restore := r.RegisterHostCall("mn.restore_exception", ktfMNRestoreException)
	if err := r.writeWords(functions, []uint32{0, restore}); err != nil {
		return err
	}
	return r.WriteU32(contextAddress+mnContextExceptionFunctionsField, functions)
}

// The MN context after the six frame words is SP, LR, r4-r9, a reserved word,
// and r10. The setjmp return value is the handler bytecode PC. Returning through
// the restored LR lets the existing host-call dispatcher resume the guest's
// catch selection, including its normal frame unlinking and cleanup.
func ktfMNRestoreException(_ context.Context, runtime *Runtime) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	handler, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	words, err := runtime.ReadWords(address, 10)
	if err != nil {
		return 0, err
	}
	for index := uint32(0); index < 6; index++ {
		if err := runtime.CPU.WriteRegister(cpu.RegisterR4+index, words[2+index]); err != nil {
			return 0, err
		}
	}
	for _, register := range []struct{ register, value uint32 }{
		{cpu.RegisterSP, words[0]},
		{cpu.RegisterLR, words[1]},
		{cpu.RegisterR10, words[9]},
	} {
		if err := runtime.CPU.WriteRegister(register.register, register.value); err != nil {
			return 0, err
		}
	}
	return handler, nil
}

// Catch classes use the same odd import-index encoding as parent classes.
// Resolve them after publishing all module classes and before Java execution.
func (r *Runtime) linkMNClassExceptions(object, imports uint32) error {
	class, err := r.InspectJavaClass(object)
	if err != nil {
		return err
	}
	for _, method := range class.Methods {
		if method.ExceptionCount > 4096 {
			return fmt.Errorf("MN method %s.%s has excessive exception count %d", class.Name, method.Name, method.ExceptionCount)
		}
		if method.ExceptionCount == 0 {
			continue
		}
		entries, err := r.ReadWords(method.ExceptionTableRaw, int(method.ExceptionCount))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			catch, err := r.ReadU32(entry + 12)
			if err != nil {
				return err
			}
			if catch&1 == 0 {
				continue
			}
			name, err := r.mnImportName(imports, catch>>1)
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("MN method %s.%s has unresolved catch import %d", class.Name, method.Name, catch>>1)
			}
			address, err := r.EnsureJavaClass(name)
			if err != nil {
				return err
			}
			if err := r.WriteU32(entry+12, address); err != nil {
				return err
			}
		}
	}
	return nil
}
