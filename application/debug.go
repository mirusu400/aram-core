package application

import (
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

const (
	DefaultDebugSnapshotEntries = guest.DefaultDebugSnapshotEntries
	MaxDebugSnapshotEntries     = guest.MaxDebugSnapshotEntries
)

// DebugSnapshot returns a consistent diagnostic snapshot. Callers should
// serialize machine operations around this method if they also maintain a
// higher-level logical running state.

type (
	DebugSnapshot            = guest.DebugSnapshot
	DebugCPUSnapshot         = guest.DebugCPUSnapshot
	DebugRegister            = guest.DebugRegister
	DebugExecutionResult     = guest.DebugExecutionResult
	DebugLogSnapshot         = guest.DebugLogSnapshot
	DebugAudioSnapshot       = guest.DebugAudioSnapshot
	DebugFramebufferSnapshot = guest.DebugFramebufferSnapshot
	DebugKTFSnapshot         = guest.DebugKTFSnapshot
	DebugKTFTaskSnapshot     = guest.DebugKTFTaskSnapshot
	DebugRaptorSnapshot      = guest.DebugRaptorSnapshot
	DebugRaptorImportCall    = guest.DebugRaptorImportCall
	DebugSKVMSnapshot        = guest.DebugSKVMSnapshot
	DebugMemoryRegion        = guest.DebugMemoryRegion
)

const (
	// defaultDebugMemoryLimit caps the total guest bytes returned across all
	// crash-triage windows when a caller does not request its own bound.
	defaultDebugMemoryLimit = 64 << 10
	debugMemoryPCTrailing   = 128
	debugMemoryPCWindow     = 512
	debugMemoryStackWindow  = 1024
	debugMemoryTextWindow   = 1024
)

// DebugMemoryRegions returns bounded windows of guest memory for crash triage:
// code around the faulting PC, the top of the stack, and the head of the text
// segment. It returns nil unless the machine has faulted, because guest runtime
// memory can hold game data and is only meant for a user-submitted crash
// report. limit caps the total bytes summed across the returned regions.
func (m *Machine) DebugMemoryRegions(limit int) []DebugMemoryRegion {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != machinecore.StateFaulted || m.cpu == nil {
		return nil
	}
	if limit <= 0 {
		limit = defaultDebugMemoryLimit
	}

	windows := make([]DebugMemoryRegion, 0, 3)
	if pc := m.lastResult.PC; pc != 0 {
		base := uint32(0)
		if pc > debugMemoryPCTrailing {
			base = pc - debugMemoryPCTrailing
		}
		windows = append(windows, guest.ReadDebugMemoryRegion(
			m.cpu, "pc", base, debugMemoryPCWindow,
		))
	}
	if sp, err := m.cpu.ReadRegister(cpu.RegisterSP); err == nil && sp != 0 {
		windows = append(windows, guest.ReadDebugMemoryRegion(
			m.cpu, "stack", sp, debugMemoryStackWindow,
		))
	}
	if m.info.TextSize != 0 {
		size := int(m.info.TextSize)
		if size > debugMemoryTextWindow {
			size = debugMemoryTextWindow
		}
		windows = append(windows, guest.ReadDebugMemoryRegion(
			m.cpu, "text", m.info.TextAddress, size,
		))
	}
	return boundDebugMemoryRegions(windows, limit)
}

// boundDebugMemoryRegions drops empty windows and truncates the tail so the
// summed byte count never exceeds limit.
func boundDebugMemoryRegions(
	regions []DebugMemoryRegion,
	limit int,
) []DebugMemoryRegion {
	bounded := make([]DebugMemoryRegion, 0, len(regions))
	total := 0
	for _, region := range regions {
		if len(region.Data) == 0 || total >= limit {
			continue
		}
		if total+len(region.Data) > limit {
			region.Data = region.Data[:limit-total]
		}
		total += len(region.Data)
		bounded = append(bounded, region)
	}
	if len(bounded) == 0 {
		return nil
	}
	return bounded
}

func (m *Machine) DebugSnapshot(maxEntries int) DebugSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit := guest.NormalizeDebugSnapshotLimit(maxEntries)
	snapshot := DebugSnapshot{
		Runtime:   machineRuntimeName(m),
		State:     m.state.String(),
		CPU:       guest.NewDebugCPUSnapshot(m.cpu),
		GuestLog:  guest.NewDebugLogSnapshot(nil, 0, limit),
		HostTrace: guest.NewDebugLogSnapshot(nil, 0, limit),
	}
	if measured, ok := m.cpu.(cpu.ExecutionStatisticsBackend); ok {
		execution := measured.ExecutionStatistics()
		snapshot.Execution = &execution
	}
	m.audioMu.Lock()
	snapshot.Audio = &DebugAudioSnapshot{
		Generation:       m.audioGeneration,
		EpochGuestNS:     m.audioEpochGuestNS,
		QueuedChunks:     len(m.publishedAudio) - m.publishedAudioHead,
		QueuedSamples:    m.publishedAudioSamples,
		PublishedDropped: m.publishedAudioDropped,
	}
	m.audioMu.Unlock()
	if m.ktf != nil && m.ktf.Services != nil && m.ktf.Services.Media != nil {
		snapshot.Audio.MediaDroppedSamples = m.ktf.Services.Media.DroppedSamples()
	} else if m.wipi != nil && m.wipi.Services != nil && m.wipi.Services.Media != nil {
		snapshot.Audio.MediaDroppedSamples = m.wipi.Services.Media.DroppedSamples()
	}
	if m.lastResult.Instructions != 0 ||
		m.lastResult.PC != 0 ||
		m.lastResult.Err != nil ||
		m.state == machinecore.StateFaulted {
		snapshot.LastResult = guest.NewDebugExecutionResult(m.lastResult)
	}
	if m.wipi != nil {
		snapshot.GuestLog = guest.NewDebugLogSnapshot(m.wipi.Logs, 0, limit)
	}
	if m.ktf != nil {
		snapshot.HostTrace = m.ktf.HostTraceSnapshot(limit)
		tasks := make([]DebugKTFTaskSnapshot, 0, len(m.ktf.Tasks))
		var taskInstructions, taskSlices, taskYields uint64
		for index, task := range m.ktf.Tasks {
			if task == nil {
				continue
			}
			taskInstructions += task.Instructions()
			taskSlices += task.Slices()
			taskYields += task.Yields()
			tasks = append(tasks, DebugKTFTaskSnapshot{
				Index:           index,
				Instructions:    task.Instructions(),
				Slices:          task.Slices(),
				Yields:          task.Yields(),
				LastYieldReason: task.LastYieldReason(),
				Done:            task.Done,
			})
		}
		var execution cpu.ExecutionStatistics
		if snapshot.Execution != nil {
			execution = *snapshot.Execution
		}
		snapshot.KTF = &DebugKTFSnapshot{
			PresentCount:          m.ktf.PresentCount,
			TickMS:                m.ktf.TickMS,
			TaskCount:             len(m.ktf.Tasks),
			ActiveInstructions:    m.ktf.ActiveInstructions,
			TaskInstructions:      taskInstructions,
			TaskSlices:            taskSlices,
			TaskYields:            taskYields,
			Tasks:                 tasks,
			Execution:             execution,
			TraceMode:             m.ktf.TraceMode().String(),
			HostCalls:             m.ktf.HostCallCount,
			LastHostCall:          m.ktf.LastHostCall,
			LastJavaMethod:        m.ktf.LastJavaMethod,
			LastUnimplementedJava: m.ktf.LastUnimplementedJava,
			FirstJavaThrow:        m.ktf.FirstJavaThrowName,
			FirstJavaThrowSP:      m.ktf.FirstJavaThrowSP,
			LastJavaThrow:         m.ktf.LastJavaThrowName,
			LastJavaThrowSP:       m.ktf.LastJavaThrowSP,
			JavaExceptionFrames: append(
				[]string(nil),
				m.ktf.JavaExceptionFrames...,
			),
		}
	}
	if m.raptor != nil {
		start := max(0, len(m.raptor.ImportTrace)-limit)
		imports := make(
			[]DebugRaptorImportCall,
			0,
			len(m.raptor.ImportTrace)-start,
		)
		for _, call := range m.raptor.ImportTrace[start:] {
			imports = append(imports, DebugRaptorImportCall{
				Module:  call.Module,
				Ordinal: call.Ordinal,
				Args:    call.Args,
				LR:      call.LR,
			})
		}
		snapshot.Raptor = &DebugRaptorSnapshot{
			ModuleInitialized: m.raptor.ModuleInitialized,
			Started:           m.raptor.Started,
			ImportCalls:       len(m.raptor.ImportTrace),
			ImportsOmitted:    start,
			Imports:           imports,
		}
	}
	return snapshot
}

func machineRuntimeName(machine *Machine) string {
	switch {
	case machine.ktf != nil:
		return "ktf"
	case machine.raptor != nil:
		return "raptor"
	case machine.minigame != nil:
		return "eads"
	case machine.wipi != nil:
		return "wipi-c"
	default:
		return "native"
	}
}

func MachineDiagnostics(
	machine machinecore.Machine,
) func() map[string]any {
	runtime, ok := machine.(*Machine)
	if !ok {
		return nil
	}
	return func() map[string]any {
		info := runtime.ImageInfo()
		diagnostics := map[string]any{
			"image": map[string]any{
				"name":         info.Name,
				"profile_id":   info.ProfileID,
				"source_kind":  string(info.SourceKind),
				"entry_point":  info.EntryPoint,
				"mode":         cpuModeName(info.Mode),
				"text_address": info.TextAddress,
				"text_size":    info.TextSize,
				"bss_address":  info.BSSAddress,
				"bss_size":     info.BSSSize,
			},
		}
		if stats, present := runtime.WIPIFrameStats(); present {
			diagnostics["wipi"] = map[string]any{
				"present_count":           stats.PresentCount,
				"api_calls":               stats.APICalls,
				"implemented_calls":       stats.ImplementedCalls,
				"unimplemented_calls":     stats.UnimplementedCalls,
				"last_api":                stats.LastAPI,
				"last_unimplemented":      stats.LastUnimplemented,
				"unimplemented_selectors": runtime.WIPIUnimplementedAPIs(),
			}
		}
		if coverage, present := runtime.WIPIAPICoverage(); present {
			diagnostics["wipi_coverage"] = map[string]any{
				"cataloged":              coverage.Cataloged,
				"dispatch_wired":         coverage.DispatchWired,
				"semantically_modeled":   coverage.SemanticallyModeled,
				"observed":               coverage.Observed,
				"observed_unimplemented": coverage.ObservedUnimplemented,
			}
		}
		if stats, present := runtime.EADSFrameStats(); present {
			events := make([]any, 0, len(stats.Events))
			for _, event := range stats.Events {
				events = append(events, map[string]any{
					"event":        event.Event,
					"instructions": event.Instructions,
					"api_calls":    event.APICalls,
					"return_value": event.ReturnValue,
				})
			}
			diagnostics["eads"] = map[string]any{
				"events":        events,
				"present_count": stats.PresentCount,
				"tick_ms":       stats.TickMS,
			}
		}
		return diagnostics
	}
}

func cpuModeName(mode cpu.Mode) string {
	switch mode {
	case cpu.ModeARM:
		return "ARM"
	case cpu.ModeThumb:
		return "Thumb"
	default:
		return fmt.Sprintf("unknown(%d)", mode)
	}
}
