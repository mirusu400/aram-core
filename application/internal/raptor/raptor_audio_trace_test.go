package raptor

import (
	"context"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorAudioTraceSurvivesGraphicsCalls(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public}
	handle, err := public.RaptorCreateClip("audio/wav", 256, 0)
	check(t, err)
	wave := raptorTestWave()
	data, err := public.Heap.Allocate(uint32(len(wave)), true)
	check(t, err)
	check(t, public.CPU.WriteMemory(data, wave))
	if !public.RaptorPutClipData(handle, data, int32(len(wave))) {
		t.Fatal("put failed")
	}
	call := func(ordinal uint32, args ...uint32) {
		t.Helper()
		for i := 0; i < 4; i++ {
			var v uint32
			if i < len(args) {
				v = args[i]
			}
			check(t, r.CPU.WriteRegister(uint32(i), v))
		}
		check(t, r.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
		check(t, r.dispatchImport(context.Background(), raptorImportKey{Module: 507, Ordinal: ordinal}))
	}
	check(t, public.Services.Advance(public.ServiceOwner, time.Second))
	call(1210, handle, 0)
	check(t, public.Services.Advance(public.ServiceOwner, time.Millisecond))
	call(1213, handle)
	call(1201, handle)
	screen, err := public.EnsureScreenFramebuffer()
	check(t, err)
	for i := 0; i < 2*wipirt.MaxSavedEntries; i++ {
		call(51, screen)
	}
	if r.AudioImportCalls != 3 || len(r.AudioImportTrace) != 3 {
		t.Fatalf("audio history = %d / %d", r.AudioImportCalls, len(r.AudioImportTrace))
	}
	for _, c := range r.ImportTrace {
		if c.Ordinal == 1210 {
			t.Fatal("graphics did not displace ordinary import history")
		}
	}
	play, stop, free := r.AudioImportTrace[0], r.AudioImportTrace[1], r.AudioImportTrace[2]
	if play.Name != "RAPTOR.sndPlay" || play.Result != 0 || play.GuestNS != int64(time.Second) || play.Clip == nil || play.Clip.State != 1 || !play.Clip.Decoded || play.Clip.SourceBytes != uint64(len(wave)) {
		t.Fatalf("play = %+v clip=%+v", play, play.Clip)
	}
	if stop.GuestNS != int64(time.Second+time.Millisecond) || stop.Clip == nil || stop.Clip.State != 0 || stop.OutputRevision != play.OutputRevision {
		t.Fatalf("stop = %+v", stop)
	}
	if free.Name != "RAPTOR.sndFree" || free.Clip != nil {
		t.Fatalf("free = %+v", free)
	}
}

func TestRaptorAudioTraceRecordsRejectedPlayAndBoundsHistory(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public}
	for i := 0; i < 100; i++ {
		check(t, r.CPU.WriteRegister(cpu.RegisterR0, uint32(i+1)))
		check(t, r.CPU.WriteRegister(cpu.RegisterR1, 0))
		check(t, r.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
		check(t, r.dispatchImport(context.Background(), raptorImportKey{Module: 507, Ordinal: 1210}))
	}
	if r.AudioImportCalls != 100 || len(r.AudioImportTrace) != 64 {
		t.Fatalf("bounded history = %d / %d", r.AudioImportCalls, len(r.AudioImportTrace))
	}
	first, last := r.AudioImportTrace[0], r.AudioImportTrace[63]
	if first.Args[0] != 37 || last.Args[0] != 100 || last.Result != ^uint32(0) || last.Clip != nil || last.Fault {
		t.Fatalf("rejected call = %+v", last)
	}
}

func TestRaptorAudioTraceRecordsFaultWithoutStaleAPIName(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public}
	public.Stats.LastAPI = "previous graphics call"
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, 0xf0000000))
	if err := r.dispatchImport(context.Background(), raptorImportKey{Module: 507, Ordinal: 1200}); err == nil {
		t.Fatal("unmapped media type did not fault")
	}
	if len(r.AudioImportTrace) != 1 || !r.AudioImportTrace[0].Fault || r.AudioImportTrace[0].Name != "RAPTOR.sndCreate" {
		t.Fatalf("fault trace=%+v", r.AudioImportTrace)
	}
}

func TestRaptorAudioTraceClearsOnStateRestore(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public, AudioImportCalls: 1, AudioImportTrace: []guest.DebugRaptorAudioCall{{Name: "old timeline"}}}
	check(t, RestoreState(r, r.CPU, &SavedState{}))
	if r.AudioImportCalls != 0 || len(r.AudioImportTrace) != 0 {
		t.Fatal("old audio timeline survived restore")
	}
}
