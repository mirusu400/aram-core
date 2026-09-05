package application

import (
	"context"
	"encoding/binary"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func (m *Machine) runRaptorStart(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("execute Raptor application from %s: %w", state, ErrInvalidState)
	}
	runtime := m.raptor
	m.state = machinecore.StateRunning
	if err := m.wipi.BeginServiceExecution(); err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	var (
		result cpu.Result
		err    error
	)
	if !runtime.ModuleInitialized {
		result, _, err = m.invokeWIPICallback(ctx, wipirt.GuestCallback{
			Procedure: runtime.Pkg.Image.Entry | 1,
			Args: [4]uint32{
				raptorrt.KernelBase,
				raptorrt.DletBase,
				raptorrt.WIPICBase,
			},
		})
		if err == nil {
			runtime.ModuleInitialized = true
			var dataBase [4]byte
			if readErr := m.cpu.ReadMemory(
				raptorrt.KernelBase+raptorrt.DependencyDataSlot,
				dataBase[:],
			); readErr != nil {
				err = readErr
			} else if got := binary.LittleEndian.Uint32(dataBase[:]); got != runtime.Clet.Table {
				err = fmt.Errorf(
					"Raptor initializer installed data base 0x%08x, want 0x%08x",
					got,
					runtime.Clet.Table,
				)
			}
		}
	}
	if err == nil && !runtime.Started {
		procedure := runtime.Clet.Start
		if procedure == 0 {
			procedure = runtime.Clet.Initialize
		}
		result, _, err = m.invokeWIPICallback(ctx, wipirt.GuestCallback{
			Procedure: procedure,
		})
		if err == nil {
			err = m.startRaptorJava(ctx)
		}
		if err == nil {
			runtime.Started = true
		}
	}
	if err == nil && runtime.Started && result.Instructions == 0 {
		result = cpu.Result{
			Reason: cpu.StopBreakpoint,
			PC:     guest.ReturnSentinel,
		}
	}
	return m.finishRaptorCall(result, err, result.Instructions)
}

func (m *Machine) startRaptor(ctx context.Context) error {
	m.mu.Lock()
	started := m.raptor != nil && m.raptor.Started
	m.mu.Unlock()
	if started {
		return m.stepRaptorFrame(ctx)
	}
	if err := m.runRaptorStart(ctx); err != nil {
		return err
	}
	// Raptor Clets commonly return from startClet after arming a timer. Advance
	// that event loop to the first visibly changed frame so the product Start
	// command reaches an actual application screen instead of the callback
	// return sentinel.
	for range raptorrt.StartupFrameLimit {
		if err := m.stepRaptorFrame(ctx); err != nil {
			return err
		}
		if m.raptorFrameVisible() {
			return nil
		}
		m.mu.Lock()
		stopped := m.state == machinecore.StateStopped
		m.mu.Unlock()
		if stopped {
			return nil
		}
	}
	return nil
}

func (m *Machine) raptorFrameVisible() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for offset := 0; offset+3 < len(m.frame.Pix); offset += 4 {
		if m.frame.Pix[offset] != 0 ||
			m.frame.Pix[offset+1] != 0 ||
			m.frame.Pix[offset+2] != 0 {
			return true
		}
	}
	return false
}

func (m *Machine) stepRaptorFrame(ctx context.Context) error {
	m.mu.Lock()
	started := m.raptor != nil && m.raptor.Started
	hasCallbackTask := started && len(m.raptor.CallbackTasks) != 0
	m.mu.Unlock()
	if !started {
		if err := m.runRaptorStart(ctx); err != nil {
			return err
		}
	}
	m.mu.Lock()
	if m.raptor != nil {
		if err := m.raptor.ServiceAuthCompletion(); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()
	if hasCallbackTask {
		// A callback that spans frames still advances guest time by one video
		// quantum per frame: titles that implement a fixed delay as a busy
		// wait on MC_knlCurrentTime (제노니아1's data-loading screen spins
		// until the clock reaches a deadline) would otherwise deadlock,
		// because a frozen clock never reaches the deadline. The pump also
		// drains ready service events every frame, so timers that come due
		// during the callback fire and queue behind the in-progress task in
		// order rather than piling up unboundedly (issue #36).
		if _, stopped, err := m.pumpWIPICallbacks(
			ctx,
			guest.WIPIFrameDuration,
		); err != nil || stopped {
			return err
		}
		return m.stepRaptorCallbackTask(ctx, 0)
	}
	callbackResult, stopped, err := m.pumpWIPICallbacks(ctx, guest.WIPIFrameDuration)
	if err != nil || stopped {
		return err
	}
	m.mu.Lock()
	hasCallbackTask = len(m.raptor.CallbackTasks) != 0
	m.mu.Unlock()
	if hasCallbackTask {
		return m.stepRaptorCallbackTask(ctx, callbackResult.Instructions)
	}
	if m.raptor.Java != nil {
		m.raptor.Java.Host.TickMS = m.raptor.Public.TickMS
	}
	javaResult, ranJava, javaErr := m.stepRaptorJavaTask(ctx)
	if javaErr != nil {
		return m.finishRaptorCall(javaResult, javaErr, javaResult.Instructions)
	}
	if ranJava {
		m.mu.Lock()
		m.lastResult = javaResult
		m.state = machinecore.StatePaused
		m.mu.Unlock()
	}
	if m.raptor.Clet.Paint == 0 {
		// A Java title has no Clet paint entry; its card is repainted here
		// instead, once per frame, for the repaints it has asked for.
		if _, err := m.raptor.RepaintDirtyJavaCard(ctx); err != nil {
			return err
		}
		return nil
	}
	m.mu.Lock()
	runtime := m.raptor
	bounds := m.frame.Bounds()
	runtime.CallbackTasks = append(runtime.CallbackTasks, &raptorrt.CallbackTask{
		Callback: wipirt.GuestCallback{
			Procedure: runtime.Clet.Paint,
			Args: [4]uint32{
				0,
				0,
				uint32(bounds.Dx()),
				uint32(bounds.Dy()),
			},
		},
	})
	m.mu.Unlock()
	return m.stepRaptorCallbackTask(ctx, callbackResult.Instructions)
}

// maxRaptorCallbacksPerFrame bounds how many queued callbacks one frame runs,
// so a title whose callbacks queue each other cannot hold the frame open even
// while each one costs almost nothing.
const maxRaptorCallbacksPerFrame = 64

// stepRaptorCallbackTask runs the queued callbacks a frame can afford.
//
// A handset runs a callback to completion and takes the next one; only work
// that actually takes time is spread over frames. Running exactly one callback
// per frame instead starved a title whose timer callbacks arrive faster than a
// frame: 화이트데이 arms its splash timers in a loop, and each frame retired
// one 25-instruction callback out of a queue that grew all the while, so the
// Clet's paint - which is only queued when the queue is empty - never ran
// again and the title froze on the rating screen (issue #150).
//
// A callback that spends the frame's whole budget, or presents, still ends the
// frame: Raptor Clets legitimately spend more than one handset video quantum
// in a paint or event callback while loading and decoding resources, and the
// guest CPU context is preserved at that yield so the callback resumes on the
// next frame rather than being faulted at an arbitrary instruction cap.
func (m *Machine) stepRaptorCallbackTask(
	ctx context.Context,
	precedingInstructions uint64,
) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	if m.raptor == nil || len(m.raptor.CallbackTasks) == 0 {
		m.mu.Unlock()
		return nil
	}
	if m.state != machinecore.StatePaused && m.state != machinecore.StateReady {
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("resume Raptor callback from %s: %w", state, ErrInvalidState)
	}
	budget := max(m.frameRunBudget, uint64(1))
	presentations := m.wipi.Stats.PresentCount
	m.state = machinecore.StateRunning
	if err := m.wipi.BeginServiceExecution(); err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	var (
		result  cpu.Result
		callErr error
		spent   uint64
	)
	for range maxRaptorCallbacksPerFrame {
		m.mu.Lock()
		if m.raptor == nil || len(m.raptor.CallbackTasks) == 0 {
			m.mu.Unlock()
			break
		}
		task := m.raptor.CallbackTasks[0]
		m.mu.Unlock()

		slice := budget - spent
		if spent >= budget {
			slice = 1
		}
		var completed bool
		result, completed, callErr = m.runRaptorCallbackTask(ctx, task, slice)
		spent += result.Instructions
		if completed {
			m.mu.Lock()
			if len(m.raptor.CallbackTasks) != 0 &&
				m.raptor.CallbackTasks[0] == task {
				m.raptor.CallbackTasks = m.raptor.CallbackTasks[1:]
				if len(m.raptor.CallbackTasks) == 0 {
					m.raptor.CallbackTasks = nil
				}
			}
			m.mu.Unlock()
		}
		if callErr != nil || !completed || result.Reason == cpu.StopExited {
			break
		}
		if spent >= budget {
			break
		}
		m.mu.Lock()
		presented := m.wipi.Stats.PresentCount != presentations
		m.mu.Unlock()
		if presented {
			break
		}
	}
	serviceInstructions := spent
	result.Instructions = spent + precedingInstructions
	return m.finishRaptorCall(result, callErr, serviceInstructions)
}

func (m *Machine) runRaptorCallbackTask(
	ctx context.Context,
	task *raptorrt.CallbackTask,
	budget uint64,
) (result cpu.Result, completed bool, returnedErr error) {
	outer, err := cpu.SaveScopedContext(m.cpu, cpu.ScopedContext{})
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}
	defer func() {
		if restoreErr := outer.Restore(m.cpu); restoreErr != nil && returnedErr == nil {
			result = cpu.Result{Reason: cpu.StopFault, Err: restoreErr}
			completed = false
			returnedErr = restoreErr
		}
	}()

	if len(task.Context) == 0 {
		for register := cpu.RegisterR0; register <= cpu.RegisterR3; register++ {
			if err := m.cpu.WriteRegister(register, task.Callback.Args[register]); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
			}
		}
		if err := m.cpu.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
		}
		if err := m.cpu.WriteRegister(
			cpu.RegisterPC,
			task.Callback.Procedure&^1,
		); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
		}
		status, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
		if err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
		}
		if task.Callback.Procedure&1 != 0 {
			status |= cpu.StatusThumb
		} else {
			status &^= cpu.StatusThumb
		}
		if err := m.cpu.WriteRegister(cpu.RegisterCPSR, status); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
		}
	} else if err := m.cpu.RestoreContext(task.Context); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}

	pc, err := m.cpu.ReadRegister(cpu.RegisterPC)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}
	status, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}
	mode := cpu.ModeARM
	if status&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	result = m.runWIPISlice(ctx, pc, mode, max(budget, uint64(1)), true)
	if result.Err != nil {
		return result, false, result.Err
	}
	if result.Reason == cpu.StopExited {
		return result, true, nil
	}
	if result.Reason == cpu.StopBreakpoint && result.PC >= 2 &&
		result.PC-2 == guest.ReturnSentinel {
		return result, true, nil
	}
	if result.Reason != cpu.StopBudget {
		err := fmt.Errorf(
			"Raptor callback 0x%08x stopped before returning (stop %d at 0x%08x)",
			task.Callback.Procedure,
			result.Reason,
			result.PC,
		)
		result.Reason = cpu.StopFault
		result.Err = err
		return result, false, err
	}
	task.Context, err = m.cpu.SaveContext()
	if err != nil {
		result.Reason = cpu.StopFault
		result.Err = err
		return result, false, err
	}
	return result, false, nil
}

func (m *Machine) finishRaptorCall(
	result cpu.Result,
	err error,
	serviceInstructions uint64,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastResult = result
	switch {
	case err != nil:
		m.state = machinecore.StateFaulted
	case result.Reason == cpu.StopExited:
		m.state = machinecore.StateStopped
	default:
		m.state = machinecore.StatePaused
	}
	fault := ""
	if err != nil {
		fault = err.Error()
	}
	if serviceErr := m.wipi.FinishServiceExecution(
		m.state,
		serviceInstructions,
		fault,
	); serviceErr != nil {
		m.state = machinecore.StateFaulted
		return serviceErr
	}
	if err != nil {
		return fmt.Errorf("execute Raptor Clet at 0x%08x: %w", result.PC, err)
	}
	return nil
}

func (m *Machine) stepRaptorJavaTask(ctx context.Context) (cpu.Result, bool, error) {
	runtime := m.raptor
	if runtime == nil || runtime.Java == nil {
		return cpu.Result{}, false, nil
	}
	task := runtime.NextRunnableJavaTask()
	if task == nil {
		return cpu.Result{}, false, nil
	}
	runtime.SetActiveJavaTask(task)
	defer runtime.SetActiveJavaTask(nil)
	outer, err := cpu.SaveScopedContext(m.cpu, cpu.ScopedContext{})
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	defer func() { _ = outer.Restore(m.cpu) }()
	if len(task.Context) == 0 {
		stack := task.Stack
		if stack == 0 {
			stack = raptorrt.RaptorJavaTaskStack(0)
		}
		for register := cpu.RegisterR0; register <= cpu.RegisterR12; register++ {
			if err := m.cpu.WriteRegister(register, 0); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
			}
		}
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   task.Target,
			cpu.RegisterSP:   stack,
			cpu.RegisterLR:   guest.ReturnSentinel | 1,
			cpu.RegisterPC:   task.Procedure &^ 1,
			cpu.RegisterCPSR: ktfrt.ModeStatus(task.Procedure),
		} {
			if err := m.cpu.WriteRegister(register, value); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
			}
		}
	} else if err := m.cpu.RestoreContext(task.Context); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	pc, err := m.cpu.ReadRegister(cpu.RegisterPC)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	status, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	mode := cpu.ModeARM
	if status&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	result := m.runWIPISlice(
		ctx,
		pc,
		mode,
		raptorrt.JavaTaskInstructionBudget,
		true,
	)
	if result.Err != nil {
		registers := make([]uint32, cpu.RegisterR12+1)
		for register := range registers {
			registers[register], _ = m.cpu.ReadRegister(uint32(register))
		}
		sp, _ := m.cpu.ReadRegister(cpu.RegisterSP)
		lr, _ := m.cpu.ReadRegister(cpu.RegisterLR)
		result.Err = fmt.Errorf(
			"%w (r0-r12=%08x sp=%08x lr=%08x)",
			result.Err,
			registers,
			sp,
			lr,
		)
		return result, true, result.Err
	}
	if result.Reason == cpu.StopBreakpoint && result.PC >= 2 &&
		result.PC-2 == guest.ReturnSentinel {
		task.Done = true
		return result, true, nil
	}
	task.Context, err = m.cpu.SaveContext()
	if err != nil {
		result.Reason = cpu.StopFault
		result.Err = err
		return result, true, err
	}
	return result, true, nil
}

func (m *Machine) startRaptorJava(ctx context.Context) error {
	runtime := m.raptor
	java := runtime.Java
	if java == nil || !java.LaunchRequested || java.MainInstance != 0 {
		return nil
	}
	class := java.ClassByName[java.MainClass]
	if class == nil {
		recovered, err := runtime.RecoverUnregisteredMainClass(java, java.MainClass)
		if err != nil {
			return fmt.Errorf(
				"recover Raptor Java main class %q: %w", java.MainClass, err)
		}
		class = recovered
	}
	if class == nil {
		return fmt.Errorf("Raptor Java main class %q was not registered", java.MainClass)
	}
	// An obfuscated or launcher-wrapped title can name a helper as the main class
	// while the real Jlet subclass carries an obfuscated name; run the lifecycle
	// on the class that actually declares startApp when the named one does not.
	construct := func(cls *raptorrt.JavaClass) (uint32, error) {
		instance, err := runtime.NewRaptorJavaObject(cls.Holder)
		if err != nil {
			return 0, fmt.Errorf("allocate Raptor Java main class %q: %w", cls.Name, err)
		}
		constructor, ok := raptorrt.DeclaredMethod(cls, "<init>", "()V")
		if !ok || constructor.Body == 0 {
			return 0, fmt.Errorf("Raptor Java main class %q has no default constructor", cls.Name)
		}
		result, _, err := m.invokeWIPICallback(ctx, wipirt.GuestCallback{
			Procedure: constructor.Body,
			Args:      [4]uint32{instance},
		})
		if err != nil {
			return 0, fmt.Errorf(
				"construct Raptor Java main class %q at PC 0x%08x after %d instructions: %w",
				cls.Name, result.PC, result.Instructions, err,
			)
		}
		return instance, nil
	}
	class = runtime.ResolveRaptorJletMainClass(class)
	instance, err := construct(class)
	if err != nil {
		return err
	}
	// Constructing a launcher/helper class can register the obfuscated Jlet
	// subclass only during its <init>; re-resolve and construct the real Jlet
	// (배틀몬스터: "Game".<init> links class "a", which extends Jlet with startApp).
	if resolved := runtime.ResolveRaptorJletMainClass(class); resolved != class {
		class = resolved
		instance, err = construct(class)
		if err != nil {
			return err
		}
	}
	values := []string{class.Name, "", "true", "true"}
	strings := make([]uint32, len(values))
	for index, value := range values {
		strings[index], err = runtime.NewRaptorJavaString(value)
		if err != nil {
			return err
		}
	}
	arguments, err := runtime.NewRaptorJavaReferenceArray(strings)
	if err != nil {
		return err
	}
	start, ok := raptorrt.DeclaredMethod(
		class,
		"startApp",
		"([Ljava/lang/String;)V",
	)
	if !ok || start.Body == 0 {
		return fmt.Errorf("Raptor Java main class %q has no startApp(String[])", class.Name)
	}
	result, _, err := m.invokeWIPICallback(ctx, wipirt.GuestCallback{
		Procedure: start.Body,
		Args:      [4]uint32{instance, arguments},
	})
	if err != nil {
		return fmt.Errorf(
			"start Raptor Java main class %q at PC 0x%08x after %d instructions: %w",
			class.Name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	java.MainInstance = instance
	return runtime.SyncRaptorJavaVTables(java)
}
