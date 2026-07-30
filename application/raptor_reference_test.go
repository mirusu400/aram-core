package application

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/raptor"
)

func TestReferenceRaptorClet(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var (
		packagePath string
		data        []byte
	)
	stop := errors.New("Raptor package selected")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := raptor.Inspect(payload); err != nil {
			return nil
		}
		packagePath, data = path, payload
		return stop
	})
	if err != nil && !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if packagePath == "" {
		t.Fatal("ARAM_TEST_DATA contained no valid Raptor package")
	}
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(packagePath),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	err = machine.Start(context.Background())
	t.Logf(
		"%s: Clet=%+v initialized=%t started=%t result=%+v error=%v",
		packagePath,
		machine.raptor.clet,
		machine.raptor.moduleInitialized,
		machine.raptor.started,
		machine.LastResult(),
		err,
	)
	if err != nil {
		t.Fatal(err)
	}
	var previousState uint32 = ^uint32(0)
	for frame := 0; frame < 512; frame++ {
		if frame == 20 || frame == 280 {
			for _, pressed := range []bool{true, false} {
				if inputErr := machine.QueueInput(machinecore.InputEvent{
					Control: "ok",
					Pressed: pressed,
				}); inputErr != nil {
					t.Fatal(inputErr)
				}
			}
		}
		frameErr := machine.StepFrame(context.Background())
		nonBlack := 0
		colors := make(map[uint32]struct{})
		for offset := 0; offset+3 < len(machine.frame.Pix); offset += 4 {
			r := machine.frame.Pix[offset]
			g := machine.frame.Pix[offset+1]
			b := machine.frame.Pix[offset+2]
			if r != 0 || g != 0 || b != 0 {
				nonBlack++
			}
			colors[uint32(r)<<16|uint32(g)<<8|uint32(b)] = struct{}{}
		}
		gameState, _ := machine.wipi.readU32(0x0152eea0)
		if frame < 8 || frame%32 == 31 ||
			gameState != previousState || frameErr != nil {
			t.Logf(
				"frame[%d]: state=%d result=%+v presents=%d non_black=%d colors=%d error=%v",
				frame,
				gameState,
				machine.LastResult(),
				machine.wipi.stats.PresentCount,
				nonBlack,
				len(colors),
				frameErr,
			)
		}
		previousState = gameState
		if frameErr != nil {
			t.Fatal(frameErr)
		}
	}
	if screenshot := os.Getenv("ARAM_RAPTOR_FRAME_PNG"); screenshot != "" {
		output, createErr := os.Create(screenshot)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if encodeErr := png.Encode(output, machine.Framebuffer()); encodeErr != nil {
			_ = output.Close()
			t.Fatal(encodeErr)
		}
		if closeErr := output.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Logf("frame PNG: %s", screenshot)
	}
	counts := make(map[uint32]int)
	for _, call := range machine.raptor.importTrace {
		counts[call.Ordinal]++
	}
	ordinals := make([]uint32, 0, len(counts))
	for ordinal := range counts {
		ordinals = append(ordinals, ordinal)
	}
	sort.Slice(ordinals, func(i, j int) bool { return ordinals[i] < ordinals[j] })
	t.Logf("import counts:")
	for _, ordinal := range ordinals {
		t.Logf("  %d=%d", ordinal, counts[ordinal])
	}
	traceStart := max(0, len(machine.raptor.importTrace)-128)
	for index, call := range machine.raptor.importTrace[traceStart:] {
		t.Logf(
			"import[%d]: ordinal=%d args=%08x lr=0x%08x",
			traceStart+index,
			call.Ordinal,
			call.Args,
			call.LR,
		)
	}
}
