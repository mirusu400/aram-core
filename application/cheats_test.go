package application

import (
	"bytes"
	"context"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	"testing"

	"github.com/mirusu400/aram-core/cheat"
	"github.com/mirusu400/aram-core/loader/raptor"
)

func TestApplicationCheatsUseTitleIdentityAndReapplyAfterReset(t *testing.T) {
	machine := newSyntheticMachine(t)
	wrapped, err := machine.WithCheats(cheat.Options{})
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddCode(cheat.Code{
		ID:      "score",
		Address: machine.info.BSSAddress,
		Value:   value,
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.EnableCode("score"); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := engine.ReadBytes(machine.info.BSSAddress, len(value))
	if err != nil {
		t.Fatal(err)
	}
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
