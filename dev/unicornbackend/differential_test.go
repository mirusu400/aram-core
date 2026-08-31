package unicornbackend

import (
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/conformance"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func installedBackendFactory(t *testing.T) func() cpu.Backend {
	t.Helper()
	probe := openInstalledBackend(t)
	options := Options{LibraryPath: probe.api.path}
	return func() cpu.Backend {
		backend, err := NewWithOptions(options)
		if err != nil {
			panic(fmt.Sprintf("reopen Unicorn comparison backend: %v", err))
		}
		return backend
	}
}

func TestARMCorpusMatchesInterpreterDeterministically(t *testing.T) {
	newUnicorn := installedBackendFactory(t)
	newInterpreter := func() cpu.Backend { return interpreter.New() }
	compared := 0
	for _, program := range conformance.Corpus {
		if program.Mode != cpu.ModeARM {
			continue
		}
		t.Run(program.Name, func(t *testing.T) {
			oracle, err := conformance.Execute(newInterpreter, program)
			if err != nil {
				t.Fatalf("interpreter: %v", err)
			}
			first, err := conformance.Execute(newUnicorn, program)
			if err != nil {
				t.Fatalf("Unicorn first run: %v", err)
			}
			second, err := conformance.Execute(newUnicorn, program)
			if err != nil {
				t.Fatalf("Unicorn second run: %v", err)
			}
			if diff := conformance.Diff(first, second); diff != "" {
				t.Fatalf("Unicorn was nondeterministic: %s", diff)
			}
			if diff := conformance.Diff(oracle, first); diff != "" {
				t.Fatalf("Unicorn diverged from interpreter: %s", diff)
			}
		})
		compared++
	}
	if compared == 0 {
		t.Fatal("conformance corpus contained no ARM programs")
	}
}

func TestARMBudgetCutoffsMatchInterpreter(t *testing.T) {
	newUnicorn := installedBackendFactory(t)
	newInterpreter := func() cpu.Backend { return interpreter.New() }
	code := make([]byte, 16)
	words := []uint32{
		0xe3a00003, // MOV r0, #3
		0xe2500001, // SUBS r0, r0, #1
		0x1afffffd, // BNE 0x1004
		0xe1200070, // BKPT
	}
	for index, word := range words {
		code[index*4+0] = byte(word)
		code[index*4+1] = byte(word >> 8)
		code[index*4+2] = byte(word >> 16)
		code[index*4+3] = byte(word >> 24)
	}
	for budget := uint64(1); budget <= 8; budget++ {
		t.Run(fmt.Sprintf("budget_%d", budget), func(t *testing.T) {
			program := conformance.Program{
				Name: "arm/budget-cutoff", Mode: cpu.ModeARM, Code: code, Budget: budget,
			}
			oracle, err := conformance.Execute(newInterpreter, program)
			if err != nil {
				t.Fatal(err)
			}
			subject, err := conformance.Execute(newUnicorn, program)
			if err != nil {
				t.Fatal(err)
			}
			if diff := conformance.Diff(oracle, subject); diff != "" {
				t.Fatalf("Unicorn diverged from interpreter: %s", diff)
			}
		})
	}
}
