package application

import (
	"context"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The seeded fuzz captures predate host-generated held-key repeats (aa65b0b).
// Their frames and input counts describe recorded transitions, including the
// legacy EVT_KEY at release. Do not inject new repeats while replaying them:
// they can choose an applet's Exit item before the reported bug is reached.
// Live held-key delivery is covered separately by brew_held_key_test.go and
// brew_issues526528_reference_test.go.
func stepBREWCapturedFrame(ctx context.Context, machine machinecore.Machine) error {
	brew := machine.(*brewMachine)
	for key, held := range brew.heldKeys {
		held.nextRepeat = time.Duration(1<<63 - 1)
		brew.heldKeys[key] = held
	}
	return machine.StepFrame(ctx)
}
