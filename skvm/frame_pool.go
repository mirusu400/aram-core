package skvm

// Fresh method calls own their local and operand buffers. A yielding call
// clones those buffers into its parked continuation before unwinding, so the
// originals can be reused even when the guest has many sleeping threads.
func (vm *VM) borrowFrame(localCount, stackCount int) *frame {
	var current *frame
	if count := len(vm.framePool); count != 0 {
		current = vm.framePool[count-1]
		vm.framePool[count-1] = nil
		vm.framePool = vm.framePool[:count-1]
	} else {
		current = &frame{}
	}
	locals, stack := current.locals, current.stack
	if cap(locals) < localCount {
		locals = make([]Value, localCount)
	} else {
		locals = locals[:localCount]
		clear(locals)
	}
	if cap(stack) < stackCount {
		stack = make([]Value, 0, stackCount)
	} else {
		stack = stack[:0]
	}
	*current = frame{locals: locals, stack: stack, invokePC: -1}
	return current
}

func (vm *VM) releaseFrame(current *frame) {
	// Bound retained storage independently of guest recursion or unusually
	// large method declarations. The pool is derived state, never a GC root
	// for guest references, and never retains old class bodies after a patch.
	if len(vm.framePool) >= 64 || cap(current.locals)+cap(current.stack) > 4096 {
		return
	}
	current.class = nil
	current.method = Method{}
	clear(current.locals)
	current.stack = current.stack[:0]
	vm.framePool = append(vm.framePool, current)
}
