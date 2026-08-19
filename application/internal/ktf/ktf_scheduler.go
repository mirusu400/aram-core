package ktf

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) call(
	ctx context.Context,
	procedure uint32,
	args []uint32,
	instructionLimit uint64,
) (result cpu.Result, returnValue uint32, returnedErr error) {
	if !r.Mapped {
		return cpu.Result{}, 0, errors.New("KTF runtime memory is not mapped")
	}
	if procedure == 0 {
		return cpu.Result{}, 0, errors.New("KTF procedure is null")
	}
	if instructionLimit == 0 {
		return cpu.Result{}, 0, errors.New("KTF instruction limit is zero")
	}
	nativeParameterBase := r.NativeParameterBase
	r.NativeParameterBase = 0
	r.executionDepth++
	defer func() {
		r.executionDepth--
		r.NativeParameterBase = nativeParameterBase
	}()
	saved, err := r.CPU.SaveContext()
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	callerStack, callerStackErr := r.CPU.ReadRegister(cpu.RegisterSP)
	if callerStackErr != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: callerStackErr}, 0, callerStackErr
	}
	defer func() {
		if restoreErr := r.CPU.RestoreContext(saved); restoreErr != nil && returnedErr == nil {
			result = cpu.Result{Reason: cpu.StopFault, Err: restoreErr}
			returnValue = 0
			returnedErr = restoreErr
		}
	}()

	stackLimit := guest.DefaultStackBase + guest.DefaultStackSize
	stack := stackLimit - 0x100
	if callerStack >= guest.DefaultStackBase && callerStack <= stackLimit {
		// A host callback can synchronously re-enter guest Java code. Grow that
		// call below the suspended guest frame instead of reusing the root stack
		// top and corrupting its locals.
		if callerStack < guest.DefaultStackBase+0x100 {
			return cpu.Result{}, 0, errors.New("KTF nested call exhausted guest stack")
		}
		stack = callerStack - 0x100
	}
	if extra := len(args) - 4; extra > 0 {
		extraSize := uint32(extra * 4)
		if stack < guest.DefaultStackBase+extraSize {
			return cpu.Result{}, 0, errors.New("KTF nested call exhausted guest stack")
		}
		stack -= extraSize
		for index, value := range args[4:] {
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], value)
			if err := r.CPU.WriteMemory(stack+uint32(index*4), encoded[:]); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
			}
		}
	}
	for register := uint32(cpu.RegisterR0); register <= cpu.RegisterR3; register++ {
		var value uint32
		if int(register) < len(args) {
			value = args[register]
		}
		if err := r.CPU.WriteRegister(register, value); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
		}
	}
	for _, registerValue := range []struct {
		register uint32
		value    uint32
	}{
		{register: cpu.RegisterSP, value: stack},
		{register: cpu.RegisterLR, value: ktfReturnSentinel | 1},
		{register: cpu.RegisterPC, value: procedure &^ 1},
		{register: cpu.RegisterCPSR, value: ModeStatus(procedure)},
	} {
		if err := r.CPU.WriteRegister(registerValue.register, registerValue.value); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
		}
	}
	mode := cpu.ModeARM
	if procedure&1 != 0 {
		mode = cpu.ModeThumb
	}
	pc := procedure &^ 1
	var instructions uint64
	for instructions < instructionLimit {
		run := r.CPU.Run(ctx, pc, mode, instructionLimit-instructions)
		instructions += run.Instructions
		run.Instructions = instructions
		result = run
		if run.Err != nil {
			registers := make([]uint32, cpu.RegisterR12+1)
			for register := range registers {
				registers[register], _ = r.CPU.ReadRegister(uint32(register))
			}
			sp, _ := r.CPU.ReadRegister(cpu.RegisterSP)
			lr, _ := r.CPU.ReadRegister(cpu.RegisterLR)
			status, _ := r.CPU.ReadRegister(cpu.RegisterCPSR)
			stack, _ := r.ReadWords(sp, 64)
			err := fmt.Errorf(
				"%w (r0-r12=%08x sp=%08x lr=%08x cpsr=%08x stack=%08x)",
				run.Err,
				registers,
				sp,
				lr,
				status,
				stack,
			)
			run.Err = err
			return run, 0, err
		}
		if run.Reason == cpu.StopBudget {
			if err := r.CPU.WriteRegister(cpu.RegisterPC, run.PC); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run, 0, err
			}
			status := uint32(0)
			if mode == cpu.ModeThumb {
				status = cpu.StatusThumb
			}
			if err := r.CPU.WriteRegister(cpu.RegisterCPSR, status); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run, 0, err
			}
			err := fmt.Errorf(
				"KTF procedure 0x%08x reached instruction budget at "+
					"0x%08x after %d instructions",
				procedure,
				run.PC,
				run.Instructions,
			)
			run.Err = err
			return run, 0, err
		}
		if run.Reason != cpu.StopBreakpoint || run.PC < 2 {
			err := fmt.Errorf(
				"KTF procedure 0x%08x stopped as %d at 0x%08x after %d instructions",
				procedure,
				run.Reason,
				run.PC,
				run.Instructions,
			)
			run.Reason = cpu.StopFault
			run.Err = err
			return run, 0, err
		}
		trap := run.PC - 2
		if trap == ktfReturnSentinel {
			returnValue, err = r.CPU.ReadRegister(cpu.RegisterR0)
			if err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run, 0, err
			}
			return run, returnValue, nil
		}
		host, ok := r.hostCalls[trap]
		if !ok {
			err := fmt.Errorf("unknown KTF host call at 0x%08x", trap)
			run.Reason = cpu.StopFault
			run.Err = err
			return run, 0, err
		}
		r.TraceHostCall(host.name)
		value, err := host.handler(ctx, r)
		if err != nil {
			var unwind *ktfJavaExceptionUnwind
			if errors.As(err, &unwind) &&
				r.callOwnsJavaExceptionUnwind(callerStack, unwind) {
				r.trace("java_exception_unwind_boundary:call")
				pc, mode, err = r.applyJavaExceptionUnwind(unwind)
				if err != nil {
					run.Reason = cpu.StopFault
					run.Err = err
					return run, 0, err
				}
				continue
			}
			err = fmt.Errorf("KTF host call %s: %w", host.name, err)
			run.Reason = cpu.StopFault
			run.Err = err
			return run, 0, err
		}
		if strings.HasPrefix(host.name, "java.method.") {
			r.LastJavaReturn = value
		}
		if r.terminationRequested {
			run.Reason = cpu.StopExited
			return run, value, nil
		}
		if err := r.CPU.WriteRegister(cpu.RegisterR0, value); err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
		lr, err := r.CPU.ReadRegister(cpu.RegisterLR)
		if err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
		pc = lr &^ 1
		mode = cpu.ModeARM
		status := uint32(0)
		if lr&1 != 0 {
			mode = cpu.ModeThumb
			status = cpu.StatusThumb
		}
		if err := r.CPU.WriteRegister(cpu.RegisterPC, pc); err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
		if err := r.CPU.WriteRegister(cpu.RegisterCPSR, status); err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
	}
	err = fmt.Errorf("KTF procedure 0x%08x exceeded %d instructions", procedure, instructionLimit)
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: instructionLimit,
		PC:           pc,
		Err:          err,
	}, 0, err
}

func (r *Runtime) callOwnsJavaExceptionUnwind(
	callerStack uint32,
	unwind *ktfJavaExceptionUnwind,
) bool {
	if unwind == nil {
		return false
	}
	if r.executionDepth <= 1 {
		return true
	}
	// A synchronous re-entry starts below the suspended caller's SP. Exception
	// frames below that boundary belong to this call and must run their guest
	// restore trampoline here. Frames at or above it belong to the suspended
	// caller and remain for the outer execution boundary to unwind.
	return callerStack >= guest.DefaultStackBase &&
		callerStack <= guest.DefaultStackBase+guest.DefaultStackSize &&
		unwind.Target.contextBase >= guest.DefaultStackBase &&
		unwind.Target.contextBase < callerStack
}

func (r *Runtime) QueueJavaVirtual(
	instance uint32,
	name, descriptor string,
	args ...uint32,
) error {
	if !r.HasJavaTaskCapacity() {
		if len(r.PendingJavaCalls) >= ktfMaxPendingJavaCalls {
			return fmt.Errorf(
				"KTF pending Java call limit %d reached",
				ktfMaxPendingJavaCalls,
			)
		}
		r.PendingJavaCalls = append(r.PendingJavaCalls, ktfPendingJavaCall{
			instance:   instance,
			name:       name,
			descriptor: descriptor,
			args:       append([]uint32(nil), args...),
		})
		r.tracef(
			"java_task_defer:%s%s:instance=0x%08x:pending=%d",
			name,
			descriptor,
			instance,
			len(r.PendingJavaCalls),
		)
		return nil
	}
	_, err := r.queueJavaVirtualTask(instance, name, descriptor, args...)
	return err
}

func (r *Runtime) ActivatePendingJavaCalls() error {
	for len(r.PendingJavaCalls) != 0 && r.HasJavaTaskCapacity() {
		call := r.PendingJavaCalls[0]
		copy(r.PendingJavaCalls, r.PendingJavaCalls[1:])
		r.PendingJavaCalls = r.PendingJavaCalls[:len(r.PendingJavaCalls)-1]
		if _, err := r.queueJavaVirtualTask(
			call.instance,
			call.name,
			call.descriptor,
			call.args...,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) queueJavaVirtualTask(
	instance uint32,
	name, descriptor string,
	args ...uint32,
) (*Task, error) {
	if instance == 0 {
		return nil, fmt.Errorf(
			"queue Java method %s%s: instance is null",
			name,
			descriptor,
		)
	}
	taskIndex := len(r.Tasks)
	for index, task := range r.Tasks {
		if task.Done {
			taskIndex = index
			break
		}
	}
	if taskIndex >= MaxTasks {
		return nil, fmt.Errorf("KTF Java task limit %d reached", MaxTasks)
	}
	instanceWords, err := r.ReadWords(instance, 2)
	if err != nil {
		return nil, err
	}
	methodAddress, err := r.resolveJavaMethod(instanceWords[1], name, descriptor)
	if err != nil {
		return nil, err
	}
	method, err := r.InspectJavaMethod(methodAddress)
	if err != nil {
		return nil, err
	}
	if method.Body == 0 {
		return nil, fmt.Errorf(
			"queue Java class 0x%08x method %s%s: method has no executable body",
			instanceWords[1],
			name,
			descriptor,
		)
	}
	callArgs := make([]uint32, 0, len(args)+2)
	callArgs = append(callArgs, 0, instance)
	callArgs = append(callArgs, args...)
	task, err := r.NewTask(method.Body, callArgs, taskIndex)
	if err != nil {
		return nil, err
	}
	if taskIndex < len(r.Tasks) {
		r.Tasks[taskIndex] = task
	} else {
		r.Tasks = append(r.Tasks, task)
	}
	r.tracef(
		"java_task_queue:%s%s:instance=0x%08x:procedure=0x%08x",
		name,
		descriptor,
		instance,
		method.Body,
	)
	return task, nil
}

func (r *Runtime) HasJavaTaskCapacity() bool {
	if len(r.Tasks) < MaxTasks {
		return true
	}
	for _, task := range r.Tasks {
		if task.Done {
			return true
		}
	}
	return false
}

// queueKeyEvent posts one handset key event to the card currently on the
// primary display. Returning false means the event must remain in Machine's
// input queue until a card or a task slot becomes available.
func (r *Runtime) QueueKeyEvent(pressed bool, key int32) (bool, error) {
	card := r.DisplayCards[r.DefaultDisplay]
	if card == 0 || r.pendingKeyTask(card) != nil ||
		r.pendingWIPICTimerTask() != nil ||
		!r.HasJavaTaskCapacity() {
		return false, nil
	}
	if task := r.PaintTasks[card]; task != nil && !task.Done {
		return false, nil
	}
	eventType := KeyReleased
	if pressed {
		eventType = KeyPressed
	}
	task, err := r.queueJavaVirtualTask(
		card,
		"keyNotify",
		"(II)Z",
		eventType,
		uint32(key),
	)
	if err != nil {
		return false, err
	}
	task.KeyCard = card
	r.tracef(
		"java_key_event:type=%d:key=%d:card=0x%08x",
		eventType,
		key,
		card,
	)
	return true, nil
}

func (r *Runtime) pendingKeyTask(card uint32) *Task {
	for _, task := range r.Tasks {
		if task != nil && !task.Done && task.KeyCard == card {
			return task
		}
	}
	return nil
}

func (r *Runtime) pendingWIPICTimerTask() *Task {
	for _, task := range r.Tasks {
		if task != nil && !task.Done && task.WipicTimer {
			return task
		}
	}
	return nil
}

func (r *Runtime) CanAwaitEvents() bool {
	return !r.terminationRequested &&
		r.DefaultDisplay != 0 &&
		r.DisplayCards[r.DefaultDisplay] != 0
}

func (r *Runtime) requestJavaTermination(instance uint32) {
	if r.terminationRequested {
		return
	}
	r.terminationRequested = true
	r.PendingJavaCalls = nil
	for _, task := range r.Tasks {
		task.Done = true
	}
	r.tracef(
		"java_lifecycle:notifyDestroyed:instance=0x%08x",
		instance,
	)
}

// requestCletTermination ends the running Clet the way MC_knlExit does on a
// handset: the provider tears the program down and never returns to the caller.
// A native Clet that reaches its exit path (for example the first-run "restart
// required" notice in 에픽크로니클PE) otherwise keeps its game-loop timer alive
// and the handset appears hung, because the host call would simply return.
func (r *Runtime) requestCletTermination() {
	if r.terminationRequested {
		return
	}
	r.terminationRequested = true
	r.PendingJavaCalls = nil
	for _, task := range r.Tasks {
		task.Done = true
	}
}

func (r *Runtime) deferStartedThread(task *Task) {
	parent := r.activeTask
	if task == nil || parent == nil || parent == task || parent.Done {
		return
	}
	grace := ktfThreadStartGrace
	if r.DefaultDisplay == 0 || r.DisplayCards[r.DefaultDisplay] == 0 {
		grace = ktfInitialThreadStartGrace
	}
	task.startBlocker = parent
	parent.childStartGrace = grace + r.ActiveInstructions
	r.tracef(
		"java_thread_start_defer:grace=%d",
		grace,
	)
}

func (r *Runtime) chargeThreadStartGrace(task *Task, instructions uint64) {
	if task == nil || task.childStartGrace == 0 {
		return
	}
	if instructions < task.childStartGrace {
		task.childStartGrace -= instructions
		return
	}
	r.releaseStartedThreads(task, "grace")
}

func (r *Runtime) releaseStartedThreads(parent *Task, reason string) {
	if parent == nil {
		return
	}
	released := 0
	for _, task := range r.Tasks {
		if task.startBlocker == parent {
			task.startBlocker = nil
			released++
		}
	}
	parent.childStartGrace = 0
	if released != 0 {
		r.tracef(
			"java_thread_start_release:reason=%s:tasks=%d",
			reason,
			released,
		)
	}
}

func (r *Runtime) NewTask(
	procedure uint32,
	args []uint32,
	index int,
) (*Task, error) {
	saved, err := r.CPU.SaveContext()
	if err != nil {
		return nil, err
	}
	restore := func() error {
		return r.CPU.RestoreContext(saved)
	}
	stackTop := guest.DefaultStackBase + guest.DefaultStackSize -
		uint32(index)*ktfTaskStackSize
	stack := stackTop - 0x100
	if extra := len(args) - 4; extra > 0 {
		extraSize := uint32(extra * 4)
		if stack < guest.DefaultStackBase+extraSize {
			_ = restore()
			return nil, errors.New("KTF Java task exhausted guest stack")
		}
		stack -= extraSize
		if err := r.writeWords(stack, args[4:]); err != nil {
			_ = restore()
			return nil, err
		}
	}
	for register := uint32(cpu.RegisterR0); register <= cpu.RegisterR12; register++ {
		var value uint32
		if int(register) < len(args) {
			value = args[register]
		}
		if err := r.CPU.WriteRegister(register, value); err != nil {
			_ = restore()
			return nil, err
		}
	}
	for _, registerValue := range []struct {
		register uint32
		value    uint32
	}{
		{register: cpu.RegisterSP, value: stack},
		{register: cpu.RegisterLR, value: ktfReturnSentinel | 1},
		{register: cpu.RegisterPC, value: procedure &^ 1},
		{register: cpu.RegisterCPSR, value: ModeStatus(procedure)},
	} {
		if err := r.CPU.WriteRegister(registerValue.register, registerValue.value); err != nil {
			_ = restore()
			return nil, err
		}
	}
	taskContext, err := r.CPU.SaveContext()
	if err != nil {
		_ = restore()
		return nil, err
	}
	if err := restore(); err != nil {
		return nil, err
	}
	return &Task{Context: taskContext}, nil
}

func (r *Runtime) RunTaskSlice(
	ctx context.Context,
	instructionLimit uint64,
) cpu.Result {
	if instructionLimit == 0 {
		return cpu.Result{
			Reason: cpu.StopFault,
			Err:    errors.New("KTF task instruction limit is zero"),
		}
	}
	if r.terminationRequested {
		for _, task := range r.Tasks {
			task.Done = true
		}
		return cpu.Result{Reason: cpu.StopExited}
	}
	r.executionDepth++
	defer func() {
		r.executionDepth--
	}()
	if err := r.ActivatePendingJavaCalls(); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	if err := r.activateDueWIPICTimers(); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	task := r.nextRunnableTask()
	if task == nil {
		reason := cpu.StopExited
		if r.hasLiveTask() {
			reason = cpu.StopBudget
		}
		return cpu.Result{Reason: reason}
	}
	r.beginJavaTimerTask(task)
	lastJavaMethod := r.LastJavaMethod
	r.LastJavaMethod = task.LastJavaMethod
	r.activeTask = task
	r.ActiveInstructions = 0
	defer func() {
		r.chargeThreadStartGrace(task, r.ActiveInstructions)
		task.LastJavaMethod = r.LastJavaMethod
		r.LastJavaMethod = lastJavaMethod
		r.activeTask = nil
		r.ActiveInstructions = 0
	}()
	if err := r.CPU.RestoreContext(task.Context); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	if err := r.restoreTaskExceptionFrame(task); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	taskIndex := -1
	for index, candidate := range r.Tasks {
		if candidate == task {
			taskIndex = index
			break
		}
	}
	pc, err := r.CPU.ReadRegister(cpu.RegisterPC)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	status, err := r.CPU.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, PC: pc, Err: err}
	}
	mode := cpu.ModeARM
	if status&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	stack, _ := r.CPU.ReadRegister(cpu.RegisterSP)
	register10, _ := r.CPU.ReadRegister(cpu.RegisterR10)
	link, _ := r.CPU.ReadRegister(cpu.RegisterLR)
	r.tracef(
		"java_task_slice:index=%d:pc=0x%08x:sp=0x%08x:r10=0x%08x:lr=0x%08x",
		taskIndex,
		pc,
		stack,
		register10,
		link,
	)
	r.yieldRequested = false
	var instructions uint64
	for instructions < instructionLimit {
		runBudget := instructionLimit - instructions
		run := r.CPU.Run(ctx, pc, mode, runBudget)
		instructions += run.Instructions
		r.ActiveInstructions = instructions
		run.Instructions = instructions
		if run.Err != nil {
			r.tracef(
				"java_task_fault:index=%d:pc=0x%08x:error=%v",
				taskIndex,
				run.PC,
				run.Err,
			)
			return run
		}
		if run.Reason == cpu.StopBudget {
			if err := r.saveTaskContext(task); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
			}
			return run
		}
		if run.Reason != cpu.StopBreakpoint || run.PC < 2 {
			run.Reason = cpu.StopFault
			run.Err = fmt.Errorf(
				"KTF task stopped as %d at 0x%08x after %d instructions",
				run.Reason,
				run.PC,
				run.Instructions,
			)
			return run
		}
		trap := run.PC - 2
		if trap == ktfReturnSentinel {
			task.Done = true
			if err := r.completeJavaTimerTask(task); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run
			}
			r.releaseStartedThreads(task, "return")
			if task.layoutOnReturn != 0 {
				instance := task.layoutOnReturn
				task.layoutOnReturn = 0
				if _, err := r.queueJavaVirtualTask(
					instance,
					"layout",
					"()V",
				); err != nil {
					run.Reason = cpu.StopFault
					run.Err = err
					return run
				}
				r.tracef(
					"java_main_layout:instance=0x%08x",
					instance,
				)
			}
			if err := r.releaseDeferredCardPaints(ctx, task); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run
			}
			if task.presentOnReturn {
				task.presentOnReturn = false
				r.paintInitializedCards[task.paintCard] = true
				if err := r.RecordPresentation(); err != nil {
					run.Reason = cpu.StopFault
					run.Err = err
					return run
				}
			}
			if !r.hasLiveTask() {
				run.Reason = cpu.StopExited
				return run
			}
			run.Reason = cpu.StopBudget
			return run
		}
		host, ok := r.hostCalls[trap]
		if !ok {
			run.Reason = cpu.StopFault
			run.Err = fmt.Errorf("unknown KTF host call at 0x%08x", trap)
			return run
		}
		r.TraceHostCall(host.name)
		value, err := host.handler(ctx, r)
		if err != nil {
			var unwind *ktfJavaExceptionUnwind
			if errors.As(err, &unwind) {
				r.tracef(
					"java_exception_unwind_boundary:task=%d",
					taskIndex,
				)
				pc, mode, err = r.applyJavaExceptionUnwind(unwind)
				if err == nil {
					continue
				}
			}
			var unhandled *ktfUnhandledJavaException
			if task.bestEffortPaint && errors.As(err, &unhandled) {
				task.Done = true
				task.bestEffortPaint = false
				delete(r.PaintTasks, task.paintCard)
				r.tracef(
					"java_initial_paint_discard:%s:card=0x%08x",
					unhandled.name,
					task.paintCard,
				)
				run.Reason = cpu.StopBudget
				run.Err = nil
				if !r.hasLiveTask() {
					run.Reason = cpu.StopExited
				}
				return run
			}
			run.Reason = cpu.StopFault
			run.Err = fmt.Errorf("KTF host call %s: %w", host.name, err)
			return run
		}
		if strings.HasPrefix(host.name, "java.bridge.") {
			register10, _ := r.CPU.ReadRegister(cpu.RegisterR10)
			link, _ := r.CPU.ReadRegister(cpu.RegisterLR)
			stack, _ := r.CPU.ReadRegister(cpu.RegisterSP)
			r.tracef(
				"java_bridge_return:%s:r10=0x%08x:sp=0x%08x:lr=0x%08x",
				host.name,
				register10,
				stack,
				link,
			)
		}
		if strings.HasPrefix(host.name, "java.method.") {
			r.LastJavaReturn = value
		}
		if r.terminationRequested {
			run.Reason = cpu.StopExited
			return run
		}
		if err := r.CPU.WriteRegister(cpu.RegisterR0, value); err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		lr, err := r.CPU.ReadRegister(cpu.RegisterLR)
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		pc = lr &^ 1
		mode = cpu.ModeARM
		status = 0
		if lr&1 != 0 {
			mode = cpu.ModeThumb
			status = cpu.StatusThumb
		}
		if err := r.CPU.WriteRegister(cpu.RegisterPC, pc); err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		if err := r.CPU.WriteRegister(cpu.RegisterCPSR, status); err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		if r.yieldRequested {
			r.yieldRequested = false
			r.releaseStartedThreads(task, "yield")
			if err := r.saveTaskContext(task); err != nil {
				return cpu.Result{
					Reason:       cpu.StopFault,
					Instructions: instructions,
					PC:           pc,
					Err:          err,
				}
			}
			if err := r.releaseDeferredCardPaints(ctx, task); err != nil {
				return cpu.Result{
					Reason:       cpu.StopFault,
					Instructions: instructions,
					PC:           pc,
					Err:          err,
				}
			}
			return cpu.Result{
				Reason:       cpu.StopBudget,
				Instructions: instructions,
				PC:           pc,
			}
		}
	}
	if err := r.saveTaskContext(task); err != nil {
		return cpu.Result{
			Reason:       cpu.StopFault,
			Instructions: instructions,
			PC:           pc,
			Err:          err,
		}
	}
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: instructions,
		PC:           pc,
	}
}

func (r *Runtime) activateDueWIPICTimers() error {
	// Carrier WIPI-C timer callbacks are serialized within an application.
	// Starting another callback while one is suspended at a host slice can
	// expose partially initialized Clet globals that the handset scheduler
	// would not make concurrently observable.
	for _, task := range r.Tasks {
		if task != nil && !task.Done &&
			(task.WipicTimer || task.KeyCard != 0) {
			return nil
		}
	}
	// Media completion callbacks share the timer callbacks' serialization
	// discipline and take priority over due timers: the handset reports a
	// finished clip before the next tick so titles that share one clip
	// handle can reload their background track (issue #48).
	if len(r.pendingMediaCallbacks) != 0 {
		handle := r.pendingMediaCallbacks[0]
		clip := r.wipicMediaClips[handle]
		if clip == nil || clip.callback == 0 {
			r.pendingMediaCallbacks = r.pendingMediaCallbacks[1:]
			return nil
		}
		taskIndex := len(r.Tasks)
		for index, task := range r.Tasks {
			if task.Done {
				taskIndex = index
				break
			}
		}
		if taskIndex >= MaxTasks {
			return nil
		}
		r.pendingMediaCallbacks = r.pendingMediaCallbacks[1:]
		task, err := r.NewTask(
			clip.callback,
			[]uint32{handle, 0},
			taskIndex,
		)
		if err != nil {
			return fmt.Errorf(
				"queue KTF WIPI-C media callback 0x%08x for clip 0x%08x: %w",
				clip.callback,
				handle,
				err,
			)
		}
		if taskIndex < len(r.Tasks) {
			r.Tasks[taskIndex] = task
		} else {
			r.Tasks = append(r.Tasks, task)
		}
		task.WipicTimer = true
		r.tracef(
			"wipic_media_callback:handle=0x%08x:callback=0x%08x:tick=%d",
			handle,
			clip.callback,
			r.TickMS,
		)
		return nil
	}
	for {
		var selected uint32
		found := false
		for address, timer := range r.wipicTimers {
			if timer == nil || !timer.active || timer.deadline > r.TickMS {
				continue
			}
			if !found || address < selected {
				selected = address
				found = true
			}
		}
		if !found {
			return nil
		}
		timer := r.wipicTimers[selected]
		if timer.callback == 0 {
			timer.active = false
			continue
		}
		taskIndex := len(r.Tasks)
		for index, task := range r.Tasks {
			if task.Done {
				taskIndex = index
				break
			}
		}
		if taskIndex >= MaxTasks {
			// Timer callbacks share the cooperative Java/WIPI-C task pool.
			// A full pool delays the callback until a later host slice; it is
			// not a guest fault and must not consume the one-shot timer.
			return nil
		}
		timer.active = false
		task, err := r.NewTask(
			timer.callback,
			[]uint32{selected, timer.parameter},
			taskIndex,
		)
		if err != nil {
			return fmt.Errorf(
				"queue KTF WIPI-C timer 0x%08x callback 0x%08x: %w",
				selected,
				timer.callback,
				err,
			)
		}
		if taskIndex < len(r.Tasks) {
			r.Tasks[taskIndex] = task
		} else {
			r.Tasks = append(r.Tasks, task)
		}
		task.WipicTimer = true
		r.tracef(
			"wipic_timer_fire:timer=0x%08x:callback=0x%08x:parameter=0x%08x:tick=%d",
			selected,
			timer.callback,
			timer.parameter,
			r.TickMS,
		)
		// Only one native timer callback may be live at a time. Other expired
		// timers remain active and are selected after this callback returns.
		return nil
	}
}

func (r *Runtime) DrainServiceEvents(now time.Duration) error {
	for {
		event, ok := r.Services.Events.Peek()
		if !ok || event.At > now {
			return nil
		}
		if event.Owner != 0 && event.Owner != r.ServiceOwner {
			return fmt.Errorf(
				"KTF service event %d belongs to owner %d",
				event.Sequence,
				event.Owner,
			)
		}
		switch event.Kind {
		case shared.EventInputPress,
			shared.EventInputRelease,
			shared.EventInputRepeat:
			key, known := guest.InputKeyCode(event.Control)
			if !known {
				r.tracef(
					"java_input_drop:control=%q:kind=%s",
					event.Control,
					event.Kind,
				)
				break
			}
			queued, err := r.QueueKeyEvent(
				event.Kind != shared.EventInputRelease,
				int32(key),
			)
			if err != nil {
				return fmt.Errorf(
					"deliver shared KTF input %q: %w",
					event.Control,
					err,
				)
			}
			if !queued {
				return nil
			}
		case shared.EventAudioComplete:
			for instance, serviceID := range r.clipServices {
				if serviceID == event.ServiceID {
					if clip := r.clips[instance]; clip != nil {
						clip.playing = false
					}
					break
				}
			}
			for handle, serviceID := range r.wipicMediaServices {
				if serviceID == event.ServiceID {
					if clip := r.wipicMediaClips[handle]; clip != nil && !clip.repeat {
						clip.state = 0
						r.tracef("wipic_media_complete:handle=0x%08x", handle)
						if clip.callback != 0 {
							r.pendingMediaCallbacks = append(
								r.pendingMediaCallbacks,
								handle,
							)
						}
					}
					break
				}
			}
		}
		popped, ok := r.Services.Events.PopReady(now)
		if !ok || popped.Sequence != event.Sequence {
			return fmt.Errorf(
				"KTF service event queue changed while delivering event %d",
				event.Sequence,
			)
		}
	}
}

func (r *Runtime) nextRunnableTask() *Task {
	if len(r.Tasks) == 0 {
		return nil
	}
	for offset := range r.Tasks {
		index := (r.taskCursor + offset) % len(r.Tasks)
		task := r.Tasks[index]
		if task.startBlocker != nil &&
			(task.startBlocker.Done || task.startBlocker.childStartGrace == 0) {
			task.startBlocker = nil
		}
		if task.WakeAtMS != 0 && task.WakeAtMS <= r.TickMS {
			task.WakeAtMS = 0
		}
		if !task.Done && task.startBlocker == nil && task.WakeAtMS == 0 {
			r.taskCursor = (index + 1) % len(r.Tasks)
			return task
		}
	}
	return nil
}

func (r *Runtime) hasRunnableTask() bool {
	for _, task := range r.Tasks {
		if task.startBlocker != nil &&
			(task.startBlocker.Done || task.startBlocker.childStartGrace == 0) {
			task.startBlocker = nil
		}
		if task.WakeAtMS != 0 && task.WakeAtMS <= r.TickMS {
			task.WakeAtMS = 0
		}
		if !task.Done && task.startBlocker == nil && task.WakeAtMS == 0 {
			return true
		}
	}
	return false
}

func (r *Runtime) hasLiveTask() bool {
	for _, task := range r.Tasks {
		if task != nil && !task.Done {
			return true
		}
	}
	return false
}

func (r *Runtime) saveTaskContext(task *Task) error {
	contextData, err := r.CPU.SaveContext()
	if err != nil {
		return err
	}
	task.Context = contextData
	if r.exceptionContext != 0 {
		task.exceptionFrame, err = r.ReadU32(r.exceptionContext + 8*4)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) restoreTaskExceptionFrame(task *Task) error {
	if r.exceptionContext == 0 {
		return nil
	}
	return r.WriteU32(r.exceptionContext+8*4, task.exceptionFrame)
}

func (r *Runtime) applyJavaExceptionUnwind(
	unwind *ktfJavaExceptionUnwind,
) (uint32, cpu.Mode, error) {
	if unwind == nil || unwind.Target.contextBase == 0 ||
		unwind.Target.restore == 0 {
		return 0, cpu.ModeARM, errors.New("invalid KTF Java exception unwind")
	}
	if err := r.CPU.WriteRegister(
		cpu.RegisterR0,
		unwind.Target.contextBase,
	); err != nil {
		return 0, cpu.ModeARM, err
	}
	if err := r.CPU.WriteRegister(
		cpu.RegisterR1,
		unwind.Target.handler,
	); err != nil {
		return 0, cpu.ModeARM, err
	}
	pc := unwind.Target.restore &^ 1
	mode := cpu.ModeARM
	if unwind.Target.restore&1 != 0 {
		mode = cpu.ModeThumb
	}
	if err := r.CPU.WriteRegister(cpu.RegisterPC, pc); err != nil {
		return 0, cpu.ModeARM, err
	}
	if err := r.CPU.WriteRegister(
		cpu.RegisterCPSR,
		ModeStatus(unwind.Target.restore),
	); err != nil {
		return 0, cpu.ModeARM, err
	}
	r.tracef(
		"java_exception_unwind:context=0x%08x:handler=0x%08x:restore=0x%08x",
		unwind.Target.contextBase,
		unwind.Target.handler,
		unwind.Target.restore,
	)
	return pc, mode, nil
}

func ktfWIPICGraphicsPostEvent(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	values, err := readKTFWIPICParameters(runtime, 4, "post-event")
	if err != nil {
		return 0, err
	}
	data := make([]byte, 16)
	for index, value := range values {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	_, err = runtime.Services.Events.Enqueue(shared.Event{
		At:    runtime.Services.Clock.Monotonic(),
		Kind:  shared.EventApplication,
		Owner: runtime.ServiceOwner,
		Name:  "wipic.graphics",
		Value: int64(int32(values[0])),
		Data:  data,
	})
	if errors.Is(err, shared.ErrLimitExceeded) {
		return guest.WIPIReturnCode(guest.WIPINoMemory), nil
	}
	return 0, err
}
