package application

import (
	"encoding/json"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
)

func TestDebugSnapshotRaptorAudioTraceIsBoundedAndOwned(t *testing.T) {
	m := newSyntheticMachine(t)
	m.raptor = &raptorrt.Runtime{CPU: m.cpu, Public: m.wipi, AudioImportCalls: 100}
	for i := 0; i < 64; i++ {
		m.raptor.AudioImportTrace = append(m.raptor.AudioImportTrace, guest.DebugRaptorAudioCall{
			DebugRaptorImportCall: guest.DebugRaptorImportCall{Module: 507, Ordinal: 1210},
			Name:                  "RAPTOR.sndPlay", Clip: &guest.DebugMediaClip{Handle: uint32(i + 37), State: 1, Decoded: true},
		})
	}
	snapshot := m.DebugSnapshot(2)
	if snapshot.Raptor.AudioImportCalls != 100 || snapshot.Raptor.AudioImportsOmitted != 98 || len(snapshot.Raptor.AudioImports) != 2 || snapshot.Raptor.AudioImports[0].Clip.Handle != 99 {
		t.Fatalf("snapshot=%+v", snapshot.Raptor)
	}
	snapshot.Raptor.AudioImports[0].Clip.Handle = 0
	snapshot.Raptor.AudioImports[0].Name = "changed"
	if m.raptor.AudioImportTrace[62].Clip.Handle != 99 || m.raptor.AudioImportTrace[62].Name != "RAPTOR.sndPlay" {
		t.Fatal("snapshot mutated live history")
	}
	data, err := json.Marshal(m.DebugSnapshot(2))
	check(t, err)
	var decoded struct {
		Raptor struct {
			AudioImportCalls    uint64 `json:"audio_import_calls"`
			AudioImportsOmitted uint64 `json:"audio_imports_omitted"`
			AudioImports        []struct {
				Name string `json:"name"`
				Clip struct {
					Handle uint32 `json:"handle"`
				} `json:"clip"`
			} `json:"audio_imports"`
		} `json:"raptor"`
	}
	check(t, json.Unmarshal(data, &decoded))
	if decoded.Raptor.AudioImportCalls != 100 || decoded.Raptor.AudioImportsOmitted != 98 || len(decoded.Raptor.AudioImports) != 2 || decoded.Raptor.AudioImports[1].Clip.Handle != 100 {
		t.Fatalf("encoded audio history=%s", data)
	}
}
