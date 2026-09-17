package application

import (
	"bytes"
	"context"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	"testing"

	"github.com/mirusu400/aram-core/cheat"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/raptor"
)

func TestApplicationCheatsUseTitleIdentityAndReapplyAfterReset(t *testing.T) {
	machine := newSyntheticMachine(t)
	wrapped, err := machine.WithCheats(cheat.Options{})
	check(t, err)
	defer wrapped.Close()

	engine := wrapped.Cheats()
	if engine.TargetSHA256() == "" ||
		engine.TargetSHA256() != machine.source.SHA256 {
		t.Fatalf(
			"cheat target SHA-256 = %q, machine = %q",
			engine.TargetSHA256(),
			machine.source.SHA256,
		)
	}
	regions := engine.Regions()
	regionNames := make(map[string]bool, len(regions))
	for _, region := range regions {
		regionNames[region.Name] = true
	}
	for _, name := range []string{
		"image.text",
		"image.data",
		"wipi.heap",
		"application.stack",
	} {
		if !regionNames[name] {
			t.Fatalf("default cheat region %q missing from %+v", name, regions)
		}
	}

	value, err := cheat.U32(999).Encode(cheat.EndianLittle)
	check(t, err)
	if _, err := engine.AddCode(cheat.Code{
		ID:      "score",
		Address: machine.info.BSSAddress,
		Value:   value,
	}); err != nil {
		t.Fatal(err)
	}
	check(t, engine.EnableCode("score"))
	check(t, wrapped.Reset(context.Background()))
	got, err := engine.ReadBytes(machine.info.BSSAddress, len(value))
	check(t, err)
	if !bytes.Equal(got, value) {
		t.Fatalf("cheat after application reset = %x, want %x", got, value)
	}
}

// ELF section flags as raptor images carry them.
const (
	sectionWriteFlag = 1
	sectionAllocFlag = 2
	sectionExecFlag  = 4
)

// Raptor code sections carry no write flag, yet raptorrt.MapRaptorImage maps them
// read-write because the handset patches import veneers in place. Hash-keyed
// code patches depend on the cheat region agreeing with that mapping.
func TestRaptorCheatRegionsAllowCodePatchesWithoutScanningThem(t *testing.T) {
	machine := &Machine{raptor: &raptorrt.Runtime{Pkg: raptor.Package{
		Image: raptor.Image{Sections: []raptor.Section{
			{
				Index:   1,
				Name:    "ER_RO",
				Flags:   sectionAllocFlag | sectionExecFlag,
				Address: 0x00100000,
				Size:    0x1000,
			},
			{
				Index:   2,
				Name:    "ER_RW",
				Flags:   sectionAllocFlag | sectionWriteFlag,
				Address: 0x00101000,
				Size:    0x1000,
			},
		}},
	}}}

	regions := make(map[string]cheat.Region)
	for _, region := range machine.defaultCheatRegionsLocked() {
		regions[region.Name] = region
	}

	code, ok := regions["image.raptor.1.ER_RO"]
	if !ok {
		t.Fatalf("raptor code region missing from %+v", regions)
	}
	if !code.Writable || code.Scannable {
		t.Fatalf(
			"raptor code region writable = %t, scannable = %t; want true, false",
			code.Writable,
			code.Scannable,
		)
	}
	data, ok := regions["image.raptor.2.ER_RW"]
	if !ok {
		t.Fatalf("raptor data region missing from %+v", regions)
	}
	if !data.Writable || !data.Scannable {
		t.Fatalf(
			"raptor data region writable = %t, scannable = %t; want true, true",
			data.Writable,
			data.Scannable,
		)
	}
}

func TestApplicationCheatsPatchSKVMClassBytecode(t *testing.T) {
	data := syntheticSKVMPackage(t)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "game.zip",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	check(t, err)
	wrapper, err := AttachCheats(created, cheat.Options{})
	check(t, err)
	t.Cleanup(func() { _ = wrapper.Close() })

	engine := wrapper.Cheats()
	if engine.ImageSHA256() == "" || engine.TargetSHA256() == "" {
		t.Fatalf(
			"SKVM cheat identities: image=%q target=%q",
			engine.ImageSHA256(),
			engine.TargetSHA256(),
		)
	}
	var game cheat.Region
	for _, region := range engine.Regions() {
		if region.Name == "skvm.class.Game" {
			game = region
			break
		}
	}
	if game.Size == 0 || !game.Writable || game.Scannable {
		t.Fatalf("SKVM Game class region = %+v", game)
	}
	class := syntheticSKVMLifecycleClass(t)
	returnOffset := bytes.IndexByte(class, 0xb1)
	if returnOffset < 0 {
		t.Fatal("synthetic class has no return instruction")
	}
	if _, err := engine.AddCode(cheat.Code{
		ID:               "break-constructor",
		Address:          game.Start + uint32(returnOffset),
		Value:            []byte{0x00},
		Expected:         []byte{0xb1},
		RestoreOnDisable: true,
	}); err != nil {
		t.Fatal(err)
	}
	check(t, engine.EnableCode("break-constructor"))
	if err := wrapper.Start(context.Background()); err == nil {
		t.Fatal("patched one-byte constructor unexpectedly completed")
	}
	check(t, engine.DisableCode("break-constructor"))
	check(t, wrapper.Reset(context.Background()))
	check(t, wrapper.Start(context.Background()))
}
