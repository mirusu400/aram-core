package application

import (
	"crypto/sha256"
	"image"
	"image/draw"

	machinecore "github.com/mirusu400/aram-core/core"
)

var _ machinecore.VideoPresentationMachine = (*gnex32Machine)(nil)

// FramePresentation returns an immutable GNEX32 frame and a content sequence.
// Repeated guest Flush calls with identical pixels retain the same sequence.
func (m *gnex32Machine) FramePresentation() (image.Image, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.framePresentationLocked()
}

// VideoPresentation anchors the current immutable frame to the same guest clock
// and discontinuity generation used by DrainAudio.
func (m *gnex32Machine) VideoPresentation() machinecore.VideoPresentation {
	m.mu.Lock()
	defer m.mu.Unlock()
	frame, sequence := m.framePresentationLocked()
	guestNS := int64(0)
	if m.runtime != nil {
		guestNS = int64(m.runtime.AudioTime())
	}
	return machinecore.VideoPresentation{
		Image:      frame,
		Sequence:   sequence,
		GuestNS:    guestNS,
		Generation: m.audioGeneration,
	}
}

// framePresentationLocked materializes a frame only after a new guest Flush.
// The snapshot is retained so later partial flushes cannot mutate pixels which
// a frontend still associates with the previous sequence.
func (m *gnex32Machine) framePresentationLocked() (image.Image, uint64) {
	if m.runtime == nil {
		return nil, 0
	}
	presentCount := m.runtime.PresentCount()
	frame := m.runtime.Frame()
	if frame == nil {
		return nil, 0
	}
	if m.presentation.image != nil && m.presentationCount == presentCount {
		return m.presentation.image, m.presentation.sequence
	}
	bounds := frame.Bounds()
	snapshot := image.NewRGBA(bounds)
	draw.Draw(snapshot, bounds, frame, bounds.Min, draw.Src)
	hash := sha256.Sum256(snapshot.Pix)
	m.presentationCount = presentCount
	if m.presentation.image != nil && m.presentation.image.Bounds() == bounds &&
		m.presentation.hash == hash {
		return m.presentation.image, m.presentation.sequence
	}
	sequence := m.presentation.sequence + 1
	m.presentation = framePresentationCache{
		image: snapshot, sequence: sequence, hash: hash, hashed: true,
	}
	return m.presentation.image, m.presentation.sequence
}
