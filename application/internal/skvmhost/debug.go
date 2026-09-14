package skvmhost

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

// This cache contains only derived diagnostic data, never machine state. The
// first observation of each changed frame still checks the actual copied pixels.
type debugFramebufferCache struct {
	frame   guest.DebugFramebufferSnapshot
	hash    [sha256.Size]byte
	surface shared.ServiceID
}

func (m *Machine) reuseDebugFramebufferLocked(frame shared.FramePresentation) {
	cached := m.debugFramebuffer
	if cached != nil && frame.Dirty.Empty() &&
		cached.frame.Sequence == frame.Sequence-1 &&
		cached.surface == frame.SurfaceID &&
		cached.frame.Width == frame.Width && cached.frame.Height == frame.Height {
		// PresentCommit retained exactly the same service-owned pixels. Preserve
		// their independent validation, but never suppress a presentation count.
		cached.frame.Sequence = frame.Sequence
		return
	}
	m.debugFramebuffer = nil
}

func (m *Machine) DebugSnapshot(maxEntries int) guest.DebugSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := guest.DebugSnapshot{
		Runtime:   m.runtimeID,
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
		sequence, hash := m.services.Graphics.LastFramePresentation()
		if cached := m.debugFramebuffer; cached != nil &&
			cached.frame.Sequence == sequence && cached.hash == hash {
			framebuffer := cached.frame
			m.validateDebugFramebufferLocked(&framebuffer)
			snapshot.SKVM.Framebuffer = &framebuffer
			return snapshot
		}
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
			m.validateDebugFramebufferLocked(framebuffer)
			m.debugFramebuffer = &debugFramebufferCache{frame: *framebuffer, hash: frame.Hash, surface: frame.SurfaceID}
			snapshot.SKVM.Framebuffer = framebuffer
		}
	}
	return snapshot
}

func (m *Machine) validateDebugFramebufferLocked(frame *guest.DebugFramebufferSnapshot) {
	frame.Stride, frame.Format, frame.DescriptorValid = 0, 0, false
	if descriptor, err := m.services.Graphics.Descriptor(m.owner, m.vm.ScreenSurface()); err == nil {
		frame.Stride = descriptor.Stride
		frame.Format = uint8(descriptor.Format)
		frame.DescriptorValid = descriptor.Width == frame.Width &&
			descriptor.Height == frame.Height && descriptor.Stride == frame.Width*4 &&
			descriptor.Format == shared.PixelRGBA8888 &&
			uint64(frame.RGBABytes) == uint64(frame.Width)*uint64(frame.Height)*4
	}
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
