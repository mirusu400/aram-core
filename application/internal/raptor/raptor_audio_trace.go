package raptor

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

const maxAudioImportTrace = 64

func (r *Runtime) clearAudioImportTrace() {
	r.AudioImportTrace = nil
	r.AudioImportCalls = 0
}

func (r *Runtime) recordAudioImport(call raptorImportCall, guestNS int64, dispatchErr error) {
	result, err := r.CPU.ReadRegister(cpu.RegisterR0)
	entry := guest.DebugRaptorAudioCall{
		DebugRaptorImportCall: guest.DebugRaptorImportCall{Module: call.Module, Ordinal: call.Ordinal, Args: call.Args, LR: call.LR},
		GuestNS:               guestNS, Name: r.audioImportName(call), Result: result,
		Fault:          dispatchErr != nil || err != nil,
		OutputRevision: r.Public.Services.Media.OutputRevision(),
	}
	handle := call.Args[0]
	if call.Ordinal == 1200 {
		handle = result
	}
	if id := r.Public.MediaServices[handle]; id != 0 {
		if info, err := r.Public.Services.Media.Info(r.Public.ServiceOwner, id); err == nil {
			sourceBytes, _ := r.Public.Services.Media.AvailableBytes(r.Public.ServiceOwner, id)
			entry.Clip = &guest.DebugMediaClip{
				Handle: handle, State: uint8(info.State), PositionNS: int64(info.Position), DurationNS: int64(info.Duration),
				Volume: info.Volume, Muted: info.Muted, RemainingPlays: info.RemainingPlays,
				Decoded: info.Decoded, WaitingForData: info.WaitingForData, SourceBytes: sourceBytes,
			}
		}
	}
	r.AudioImportCalls++
	if len(r.AudioImportTrace) == maxAudioImportTrace {
		copy(r.AudioImportTrace, r.AudioImportTrace[1:])
		r.AudioImportTrace[len(r.AudioImportTrace)-1] = entry
	} else {
		r.AudioImportTrace = append(r.AudioImportTrace, entry)
	}
}

func (r *Runtime) audioImportName(call raptorImportCall) string {
	if call.Module == 507 {
		switch call.Ordinal {
		case 1200:
			return "RAPTOR.sndCreate"
		case 1201:
			return "RAPTOR.sndFree"
		case 1203:
			return "RAPTOR.sndPutData"
		case 1206:
			return "RAPTOR.sndClearData"
		case 1209:
			return "RAPTOR.sndSetVolume"
		case 1210:
			return "RAPTOR.sndPlay"
		case 1213:
			return "RAPTOR.sndStop"
		case 1221:
			return "RAPTOR.sndRewind"
		}
	}
	if name, ok := raptorWIPIImportName(call.Ordinal); ok {
		return name
	}
	return r.unimplementedImportName(raptorImportKey{Module: call.Module, Ordinal: call.Ordinal})
}
