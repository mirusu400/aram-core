package application

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
)

func TestResumeRaptorInvalidDrawRequiresExactFault(t *testing.T) {
	type faultState struct {
		digest, aid, mainClass string
		lr, method, object, pc uint32
		hasError               bool
	}
	zenonia := faultState{
		digest:    zenonia2RaptorSHA256,
		aid:       "0002C004",
		mainClass: "Clet",
		lr:        0x00070eef,
		object:    105,
		hasError:  true,
	}
	hybrid := faultState{
		digest:    "e68b1c8aef85c584dc6e5e225f0c226641b23f6c523bf0f59eb662151f9cf954",
		aid:       "0002996E",
		mainClass: "Clet",
		lr:        0x00031c1b,
		object:    0,
		hasError:  true,
	}
	mutations := []struct {
		name   string
		mutate func(*faultState)
	}{
		{name: "wrong digest", mutate: func(s *faultState) { s.digest = "0" + s.digest[1:] }},
		{name: "wrong AID", mutate: func(s *faultState) { s.aid = "00000000" }},
		{name: "wrong main class", mutate: func(s *faultState) { s.mainClass = "Other" }},
		{name: "wrong return", mutate: func(s *faultState) { s.lr += 2 }},
		{name: "nonzero method", mutate: func(s *faultState) { s.method = 1 }},
		{name: "wrong receiver", mutate: func(s *faultState) { s.object++ }},
		{name: "nonzero PC", mutate: func(s *faultState) { s.pc = 2 }},
		{name: "no fault", mutate: func(s *faultState) { s.hasError = false }},
	}
	run := func(t *testing.T, state faultState, wantResume bool) {
		t.Helper()
		backend := interpreter.New()
		t.Cleanup(func() { _ = backend.Close() })
		check(t, backend.Map(
			state.lr&^0xfff,
			0x1000,
			cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
		))
		check(t, backend.WriteRegister(cpu.RegisterLR, state.lr))
		check(t, backend.WriteRegister(cpu.RegisterR4, state.method))
		check(t, backend.WriteRegister(cpu.RegisterR5, state.object))
		check(t, backend.WriteRegister(cpu.RegisterR0, 0xffffffff))
		check(t, backend.WriteRegister(cpu.RegisterCPSR, 0x10))
		machine := &Machine{
			cpu:    backend,
			source: machinecore.Source{SHA256: state.digest},
			raptor: &raptorrt.Runtime{Pkg: raptorloader.Package{
				Descriptor: raptorloader.Descriptor{
					AID: state.aid, MainClass: state.mainClass,
				},
			}},
		}
		fault := errors.New("execute at zero")
		result := cpu.Result{Reason: cpu.StopFault, PC: state.pc}
		if state.hasError {
			result.Err = fault
		}
		if wantResume {
			lr, err := backend.ReadRegister(cpu.RegisterLR)
			check(t, err)
			object, err := backend.ReadRegister(cpu.RegisterR5)
			check(t, err)
			if !quirkdb.LookupRaptorInvalidDrawCall(
				state.digest, state.aid, state.mainClass, lr, object,
			) {
				t.Fatalf("exact invalid draw call did not match: lr=%08x object=%d", lr, object)
			}
		}
		machine.resumeRaptorInvalidDraw(&result)
		if !wantResume {
			if state.hasError && result.Err != fault || !state.hasError && result.Err != nil ||
				result.Reason != cpu.StopFault || result.PC != state.pc {
				t.Fatalf("unexpected recovery: %+v", result)
			}
			return
		}
		if result.Err != nil || result.Reason != cpu.StopBudget || result.PC != state.lr&^1 {
			t.Fatalf("recovered result = %+v", result)
		}
		if r0, err := backend.ReadRegister(cpu.RegisterR0); err != nil || r0 != 0 {
			t.Fatalf("r0 = %08x, err=%v", r0, err)
		}
		if pc, err := backend.ReadRegister(cpu.RegisterPC); err != nil || pc != state.lr&^1 {
			t.Fatalf("pc = %08x, err=%v", pc, err)
		}
		if status, err := backend.ReadRegister(cpu.RegisterCPSR); err != nil || status&cpu.StatusThumb == 0 {
			t.Fatalf("cpsr = %08x, err=%v", status, err)
		}
	}

	t.Run("Zenonia 2", func(t *testing.T) { run(t, zenonia, true) })
	t.Run("Hybrid", func(t *testing.T) { run(t, hybrid, true) })
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			state := zenonia
			test.mutate(&state)
			run(t, state, false)
		})
	}
}
