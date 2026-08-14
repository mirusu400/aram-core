package skvmhost

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (m *Machine) DebugSnapshot(maxEntries int) guest.DebugSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := guest.DebugSnapshot{
		Runtime:   "skvm",
		State:     m.state.String(),
		GuestLog:  guest.NewDebugLogSnapshot(nil, 0, guest.NormalizeDebugSnapshotLimit(maxEntries)),
		HostTrace: guest.NewDebugLogSnapshot(nil, 0, guest.NormalizeDebugSnapshotLimit(maxEntries)),
		SKVM: &guest.DebugSKVMSnapshot{
			MainClass:   m.mainClass,
			Started:     m.started,
			MIDlet:      m.midlet,
			QueuedInput: len(m.input),
		},
	}
	if m.vm != nil {
		snapshot.SKVM.CurrentDisplay = m.vm.CurrentDisplay()
		snapshot.SKVM.Instructions = m.vm.Instructions
	}
	if m.vm != nil && m.services != nil {
		frame := m.services.Graphics.LastFrame()
		if frame.Sequence != 0 {
			actualHash := sha256.Sum256(frame.RGBA)
			framebuffer := &guest.DebugFramebufferSnapshot{
				Surface:        frame.SurfaceID.String(),
				Sequence:       frame.Sequence,
				Width:          frame.Width,
				Height:         frame.Height,
				RGBABytes:      len(frame.RGBA),
				RGBASHA256:     fmt.Sprintf("%x", actualHash),
				SnapshotHashOK: actualHash == frame.Hash,
			}
			if descriptor, err := m.services.Graphics.Descriptor(
				m.owner,
				m.vm.ScreenSurface(),
			); err == nil {
				framebuffer.Stride = descriptor.Stride
				framebuffer.Format = uint8(descriptor.Format)
				framebuffer.DescriptorValid =
					descriptor.Width == frame.Width &&
						descriptor.Height == frame.Height &&
						descriptor.Stride == frame.Width*4 &&
						descriptor.Format == shared.PixelRGBA8888 &&
						uint64(len(frame.RGBA)) ==
							uint64(frame.Width)*uint64(frame.Height)*4
			}
			snapshot.SKVM.Framebuffer = framebuffer
		}
	}
	return snapshot
}

// FrameQuantum reports how much guest time one StepFrame advances for a SKVM
// machine, which paces itself from the shared service configuration.
func (m *Machine) FrameQuantum() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frameQuantum > 0 {
		return m.frameQuantum
	}
	if m.services == nil || m.services.Config.FrameDuration <= 0 {
		return guest.WIPIFrameDuration
	}
	return m.services.Config.FrameDuration
}
