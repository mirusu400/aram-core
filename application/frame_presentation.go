package application

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/color"
	"image/draw"

	machinecore "github.com/mirusu400/aram-core/core"
)

// framePresentationCache holds the last frame handed to a driver so an
// unchanged screen can be republished instead of copied again.
type framePresentationCache struct {
	image    *image.RGBA
	sequence uint64
	hash     [sha256.Size]byte
	// hashed marks the cached frame as one identified by the graphics
	// service's presentation hash rather than by comparing pixels.
	hashed bool
	// blank marks the placeholder shown before a runtime has presented
	// anything, so it is not rebuilt on every tick either.
	blank bool
}

// FramePresentation returns the current guest frame together with a sequence
// that changes only when the pixels change.
//
// A driver polls the framebuffer once per host tick, whether or not the guest
// drew anything, so Framebuffer's unconditional allocate-and-copy was charged
// to every tick of every title. This reports the same pixels while letting the
// caller skip the work when the sequence is unchanged.
//
// The returned image is immutable and is shared between calls: a caller that
// needs to own or mutate the pixels must use Framebuffer, which always copies.
func (m *Machine) FramePresentation() (image.Image, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.framePresentationLocked()
}

// VideoPresentation anchors the immutable frame to the same deterministic
// guest timeline and discontinuity generation as published audio.
func (m *Machine) VideoPresentation() machinecore.VideoPresentation {
	m.mu.Lock()
	defer m.mu.Unlock()
	frame, sequence := m.framePresentationLocked()
	return machinecore.VideoPresentation{
		Image:      frame,
		Sequence:   sequence,
		GuestNS:    int64(m.guestTimeLocked()),
		Generation: m.audioGenerationValue(),
	}
}

// framePresentationLocked is the shared legacy/timestamped presentation path.
// Callers hold m.mu.
func (m *Machine) framePresentationLocked() (image.Image, uint64) {
	if m.ktf != nil && m.ktf.Services != nil {
		sequence, hash := m.ktf.Services.Graphics.LastFramePresentation()
		if sequence == 0 {
			return m.publishBlankFrame()
		}
		if m.presentation.image != nil &&
			m.presentation.hashed &&
			m.presentation.hash == hash {
			return m.presentation.image, m.presentation.sequence
		}
		// KTF Java paint callbacks draw cooperatively across several host
		// quanta, so only the graphics service's committed frame may be shown.
		presented := m.ktf.Services.Graphics.LastFrameImage()
		if presented == nil {
			return m.publishBlankFrame()
		}
		return m.publishFrame(presented, hash, true, false)
	}
	if m.presentation.image != nil &&
		!m.presentation.hashed &&
		!m.presentation.blank &&
		m.presentation.image.Bounds() == m.frame.Bounds() &&
		bytes.Equal(m.presentation.image.Pix, m.frame.Pix) {
		return m.presentation.image, m.presentation.sequence
	}
	snapshot := image.NewRGBA(m.frame.Bounds())
	copy(snapshot.Pix, m.frame.Pix)
	return m.publishFrame(snapshot, [sha256.Size]byte{}, false, false)
}

// publishBlankFrame shows the placeholder used before a runtime commits its
// first frame. Callers hold m.mu.
func (m *Machine) publishBlankFrame() (image.Image, uint64) {
	if m.presentation.image != nil &&
		m.presentation.blank &&
		m.presentation.image.Bounds() == m.frame.Bounds() {
		return m.presentation.image, m.presentation.sequence
	}
	blank := image.NewRGBA(m.frame.Bounds())
	draw.Draw(
		blank,
		blank.Bounds(),
		image.NewUniform(color.Black),
		image.Point{},
		draw.Src,
	)
	return m.publishFrame(blank, [sha256.Size]byte{}, false, true)
}

// publishFrame adopts a newly materialized frame and advances the sequence.
// Callers hold m.mu.
func (m *Machine) publishFrame(
	frame *image.RGBA,
	hash [sha256.Size]byte,
	hashed bool,
	blank bool,
) (image.Image, uint64) {
	m.presentation.image = frame
	m.presentation.hash = hash
	m.presentation.hashed = hashed
	m.presentation.blank = blank
	m.presentation.sequence++
	return m.presentation.image, m.presentation.sequence
}
