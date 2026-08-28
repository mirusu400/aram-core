package guest

import (
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	DefaultDebugSnapshotEntries = 4096
	MaxDebugSnapshotEntries     = 16384
)

// DebugSnapshot is a bounded, serialization-friendly view of an application
// machine. It contains execution metadata and logs, but never source bytes,
// guest memory, framebuffer pixels, persistence, or media payloads.
type DebugSnapshot struct {
	Runtime    string                   `json:"runtime"`
	State      string                   `json:"state"`
	CPU        *DebugCPUSnapshot        `json:"cpu,omitempty"`
	Execution  *cpu.ExecutionStatistics `json:"execution,omitempty"`
	Audio      *DebugAudioSnapshot      `json:"audio,omitempty"`
	LastResult *DebugExecutionResult    `json:"last_result,omitempty"`
	GuestLog   DebugLogSnapshot         `json:"guest_log"`
	HostTrace  DebugLogSnapshot         `json:"host_trace"`
	KTF        *DebugKTFSnapshot        `json:"ktf,omitempty"`
	Raptor     *DebugRaptorSnapshot     `json:"raptor,omitempty"`
	SKVM       *DebugSKVMSnapshot       `json:"skvm,omitempty"`
}

type DebugAudioSnapshot struct {
	Generation          uint64 `json:"generation"`
	EpochGuestNS        int64  `json:"epoch_guest_ns"`
	QueuedChunks        int    `json:"queued_chunks"`
	QueuedSamples       int    `json:"queued_samples"`
	PublishedDropped    uint64 `json:"published_dropped_samples"`
	MediaDroppedSamples uint64 `json:"media_dropped_samples"`
}

type DebugCPUSnapshot struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Architecture string          `json:"architecture"`
	Mode         string          `json:"mode"`
	Registers    []DebugRegister `json:"registers,omitempty"`
	ReadErrors   []string        `json:"read_errors,omitempty"`
}

type DebugRegister struct {
	Name  string `json:"name"`
	Value uint32 `json:"value"`
	Hex   string `json:"hex"`
}

type DebugExecutionResult struct {
	Reason       string `json:"reason"`
	Instructions uint64 `json:"instructions"`
	PC           uint32 `json:"pc"`
	PCHex        string `json:"pc_hex"`
	Error        string `json:"error,omitempty"`
}

type DebugLogSnapshot struct {
	Total   int      `json:"total"`
	Omitted int      `json:"omitted"`
	Entries []string `json:"entries,omitempty"`
}

// DebugFramebufferSnapshot identifies a presented frame without including its
// pixels. The canonical RGBA digest can be compared with a separately captured
// screenshot to distinguish core rendering from downstream frame corruption.
type DebugFramebufferSnapshot struct {
	Surface         string `json:"surface"`
	Sequence        uint64 `json:"sequence"`
	Width           int32  `json:"width"`
	Height          int32  `json:"height"`
	Stride          int32  `json:"stride"`
	Format          uint8  `json:"format"`
	RGBABytes       int    `json:"rgba_bytes"`
	RGBASHA256      string `json:"rgba_sha256"`
	SnapshotHashOK  bool   `json:"snapshot_hash_ok"`
	DescriptorValid bool   `json:"descriptor_valid"`
}

func NormalizeDebugSnapshotLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultDebugSnapshotEntries
	case limit > MaxDebugSnapshotEntries:
		return MaxDebugSnapshotEntries
	default:
		return limit
	}
}

func NewDebugCPUSnapshot(backend cpu.Backend) *DebugCPUSnapshot {
	if backend == nil {
		return nil
	}
	identity := backend.Identity()
	snapshot := &DebugCPUSnapshot{
		Name:         identity.Name,
		Version:      identity.Version,
		Architecture: string(identity.Architecture),
		Mode:         "unknown",
		Registers:    make([]DebugRegister, 0, len(DebugRegisterNames)),
	}
	for id, name := range DebugRegisterNames {
		value, err := backend.ReadRegister(uint32(id))
		if err != nil {
			snapshot.ReadErrors = append(
				snapshot.ReadErrors,
				fmt.Sprintf("%s: %v", name, err),
			)
			continue
		}
		snapshot.Registers = append(snapshot.Registers, DebugRegister{
			Name:  name,
			Value: value,
			Hex:   fmt.Sprintf("0x%08x", value),
		})
		if id == int(cpu.RegisterCPSR) {
			snapshot.Mode = "arm"
			if value&cpu.StatusThumb != 0 {
				snapshot.Mode = "thumb"
			}
		}
	}
	return snapshot
}

func NewDebugExecutionResult(result cpu.Result) *DebugExecutionResult {
	snapshot := &DebugExecutionResult{
		Reason:       DebugStopReasonLabel(result.Reason),
		Instructions: result.Instructions,
		PC:           result.PC,
		PCHex:        fmt.Sprintf("0x%08x", result.PC),
	}
	if result.Err != nil {
		snapshot.Error = result.Err.Error()
	}
	return snapshot
}

func DebugStopReasonLabel(reason cpu.StopReason) string {
	switch reason {
	case cpu.StopRequested:
		return "requested"
	case cpu.StopBreakpoint:
		return "breakpoint"
	case cpu.StopFault:
		return "fault"
	case cpu.StopBudget:
		return "budget"
	case cpu.StopExited:
		return "exited"
	case cpu.StopExecutionTrap:
		return "execution-trap"
	default:
		return "unknown"
	}
}

// NewDebugLogSnapshot returns the newest entries within limit. dropped counts
// entries a bounded log already discarded before this call, so Total and
// Omitted stay truthful for logs that roll over during a long session.
func NewDebugLogSnapshot(entries []string, dropped, limit int) DebugLogSnapshot {
	start := max(0, len(entries)-limit)
	return DebugLogSnapshot{
		Total:   dropped + len(entries),
		Omitted: dropped + start,
		Entries: append([]string(nil), entries[start:]...),
	}
}

var DebugRegisterNames = [...]string{
	"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"r8", "r9", "r10", "r11", "r12", "sp", "lr", "pc", "cpsr",
}

// DebugMemoryRegion is a bounded window of guest memory captured for crash
// triage. Data holds the bytes that were readable from Base upward; a short
// Data means the window ran into unmapped memory. These are guest runtime
// bytes, so a region is only ever populated for a faulted machine and only
// travels in a user-submitted crash report.
type DebugMemoryRegion struct {
	Label string `json:"label"`
	Base  uint32 `json:"base"`
	Data  []byte `json:"-"`
}

// debugMemoryRegionChunk is the granularity of a bounded region read. A window
// stops at the first chunk that fails so a partly-mapped window still yields its
// readable prefix instead of nothing.
const debugMemoryRegionChunk = 16

// ReadDebugMemoryRegion reads up to size bytes from base, stopping at the first
// unreadable chunk. An unmapped tail therefore never discards the mapped
// prefix, and a fully unmapped window yields an empty region.
func ReadDebugMemoryRegion(
	backend cpu.Backend,
	label string,
	base uint32,
	size int,
) DebugMemoryRegion {
	region := DebugMemoryRegion{Label: label, Base: base}
	if backend == nil || size <= 0 {
		return region
	}
	buffer := make([]byte, 0, size)
	chunk := make([]byte, debugMemoryRegionChunk)
	for offset := 0; offset < size; offset += debugMemoryRegionChunk {
		want := debugMemoryRegionChunk
		if remaining := size - offset; remaining < want {
			want = remaining
		}
		address := uint64(base) + uint64(offset)
		if address > 0xffffffff {
			break
		}
		if err := backend.ReadMemory(uint32(address), chunk[:want]); err != nil {
			break
		}
		buffer = append(buffer, chunk[:want]...)
	}
	region.Data = buffer
	return region
}

type DebugKTFSnapshot struct {
	PresentCount          uint32                  `json:"present_count"`
	TickMS                uint64                  `json:"tick_ms"`
	TaskCount             int                     `json:"task_count"`
	ActiveInstructions    uint64                  `json:"active_instructions"`
	TaskInstructions      uint64                  `json:"task_instructions"`
	TaskSlices            uint64                  `json:"task_slices"`
	TaskYields            uint64                  `json:"task_yields"`
	Tasks                 []DebugKTFTaskSnapshot  `json:"tasks,omitempty"`
	Execution             cpu.ExecutionStatistics `json:"execution"`
	TraceMode             string                  `json:"trace_mode"`
	HostCalls             uint64                  `json:"host_calls"`
	LastHostCall          string                  `json:"last_host_call,omitempty"`
	LastJavaMethod        string                  `json:"last_java_method,omitempty"`
	LastUnimplementedJava string                  `json:"last_unimplemented_java,omitempty"`
	FirstJavaThrow        string                  `json:"first_java_throw,omitempty"`
	FirstJavaThrowSP      uint32                  `json:"first_java_throw_sp,omitempty"`
	LastJavaThrow         string                  `json:"last_java_throw,omitempty"`
	LastJavaThrowSP       uint32                  `json:"last_java_throw_sp,omitempty"`
	JavaExceptionFrames   []string                `json:"java_exception_frames,omitempty"`
}

type DebugKTFTaskSnapshot struct {
	Index           int    `json:"index"`
	Instructions    uint64 `json:"instructions"`
	Slices          uint64 `json:"slices"`
	Yields          uint64 `json:"yields"`
	LastYieldReason string `json:"last_yield_reason,omitempty"`
	Done            bool   `json:"done"`
}

type DebugRaptorSnapshot struct {
	ModuleInitialized bool                    `json:"module_initialized"`
	Started           bool                    `json:"started"`
	ImportCalls       int                     `json:"import_calls"`
	ImportsOmitted    int                     `json:"imports_omitted"`
	Imports           []DebugRaptorImportCall `json:"imports,omitempty"`
}

type DebugRaptorImportCall struct {
	Module  uint32    `json:"module"`
	Ordinal uint32    `json:"ordinal"`
	Args    [4]uint32 `json:"args"`
	LR      uint32    `json:"lr"`
}

type DebugSKVMSnapshot struct {
	MainClass      string                    `json:"main_class"`
	Started        bool                      `json:"started"`
	MIDlet         uint32                    `json:"midlet"`
	CurrentDisplay uint32                    `json:"current_display"`
	Instructions   uint64                    `json:"instructions"`
	QueuedInput    int                       `json:"queued_input"`
	Framebuffer    *DebugFramebufferSnapshot `json:"framebuffer,omitempty"`
}
