package application

import raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"

// Time & Tales publishes no method names for its j class. Its game-start
// Runnable has six own virtual methods, with run() in the last slot. The
// metadata-free Thread.start fallback assumes the second own slot is run(),
// which instead handles key input and returns immediately. Limit the correction
// to this executable and verify both vtable entries before scheduling it.
const timeAndTalesImageSHA256 = "5df265e6c2783a994e500c111b5a61df9d37a512930348d5d37ddb51c5b9cb9d"

func (m *Machine) applyRaptorThreadRunPatch(task *raptorrt.JavaTask) {
	if m.info.ImageSHA256 != timeAndTalesImageSHA256 || task.Done ||
		task.HasContext() || task.Procedure != 0x0001e79c {
		return
	}
	holder, err := m.wipi.ReadU32(task.Target + 4)
	if err != nil || holder != 0x01400f14 {
		return
	}
	table, err := m.wipi.ReadU32(task.Target)
	if err != nil || table == 0 {
		return
	}
	oldBody, oldErr := m.wipi.ReadU32(table + 0x30)
	runBody, runErr := m.wipi.ReadU32(table + 0x40)
	if oldErr != nil || runErr != nil || oldBody != 0x0001e79c || runBody != 0x0005ba2c {
		return
	}
	task.Procedure = runBody
}
