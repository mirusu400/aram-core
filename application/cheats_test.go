package application

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cheat"
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
