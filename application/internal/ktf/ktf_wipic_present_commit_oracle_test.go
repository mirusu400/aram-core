package ktf

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"reflect"
	goruntime "runtime"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

// This is the frozen old function, not a reference that delegates to the
// optimized presentation/copy logic. Common to baseline and candidate.
func wipicCommitLiteralOld(r *Runtime, handle uint32) error {
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil || r.frame == nil {
		return nil
	}
	if err := r.syncKTFWIPICFramebuffer(handle); err != nil {
		return err
	}
	surface := r.wipicSurfaceServices[handle]
	if surface == 0 {
		return fmt.Errorf("KTF WIPI-C framebuffer 0x%08x has no shared surface", handle)
	}
	presentationSurface := surface
	if !framebuffer.screen {
		screenHandle, err := r.EnsureWIPICScreenFramebuffer()
		if err != nil {
			return err
		}
		screen := r.wipicFramebuffers[screenHandle]
		presentationSurface = r.wipicSurfaceServices[screenHandle]
		if screen == nil || presentationSurface == 0 {
			return errors.New("KTF WIPI-C screen framebuffer is unavailable")
		}
		width := min(framebuffer.width, screen.width)
		height := min(framebuffer.height, screen.height)
		if width > 0 && height > 0 {
			if err := r.Services.Graphics.Blit(
				r.ServiceOwner,
				presentationSurface,
				surface,
				0,
				0,
				shared.Rectangle{
					Width:  int32(width),
					Height: int32(height),
				},
			); err != nil {
				return fmt.Errorf(
					"flush KTF WIPI-C framebuffer 0x%08x to screen: %w",
					handle,
					err,
				)
			}
		}
	}
	if r.Services.Graphics.Screen() != presentationSurface {
		if err := r.Services.Graphics.SetScreen(
			r.ServiceOwner,
			presentationSurface,
		); err != nil {
			return err
		}
	}
	frame, err := r.Services.Graphics.Present(
		r.ServiceOwner,
		presentationSurface,
		shared.Rectangle{},
	)
	if err != nil {
		return err
	}
	bounds := r.frame.Bounds()
	// Keep the existing snapshot/hash contract, but copy whole overlapping
	// rows into the host image. Pix starts at Bounds().Min even for a
	// nonzero-origin subimage, and Stride preserves padding and margins.
	width := min(int(frame.Width), bounds.Dx())
	height := min(int(frame.Height), bounds.Dy())
	for y := 0; y < height; y++ {
		source := y * int(frame.Width) * 4
		destination := y * r.frame.Stride
		copy(r.frame.Pix[destination:destination+width*4], frame.RGBA[source:source+width*4])
	}
	r.WipicScreenPending = false
	if state := r.Graphics[r.ScreenGraphics]; state != nil {
		state.PixelsDirty = true
	}
	r.PresentCount++
	return nil
}

type wipicCommitFixture struct {
	r                *Runtime
	handle, graphics uint32
	backing          *image.RGBA
}

func wipicCommitNewFixture(t *testing.T, parent, target image.Rectangle) wipicCommitFixture {
	t.Helper()
	r := newTestRuntime(t)
	r.frame = image.NewRGBA(image.Rect(0, 0, 3, 2))
	graphics, err := r.EnsureScreenGraphics()
	check(t, err)
	handle, err := r.EnsureWIPICScreenFramebuffer()
	check(t, err)
	backing := image.NewRGBA(parent)
	for i := range backing.Pix {
		backing.Pix[i] = 0x5a
	}
	r.frame = backing.SubImage(target).(*image.RGBA)
	r.Graphics[graphics].PixelsDirty = false
	r.WipicScreenPending = true
	f := wipicCommitFixture{r: r, handle: handle, graphics: graphics, backing: backing}
	wipicCommitSeed(t, f, 0)
	return f
}

func wipicCommitSeed(t *testing.T, f wipicCommitFixture, turn int) {
	t.Helper()
	fb := f.r.wipicFramebuffers[f.handle]
	words := [...]uint16{0xf800, 0x07e0, 0x001f, 0xffff, 0, 0xffe0}
	pixels := make([]byte, fb.stride*fb.height)
	for y := 0; y < fb.height; y++ {
		for x := 0; x < fb.width; x++ {
			binary.LittleEndian.PutUint16(pixels[y*fb.stride+x*2:], words[(y*fb.width+x+turn)%len(words)])
		}
	}
	check(t, f.r.CPU.WriteMemory(fb.pixels, pixels))
}

type wipicCommitState struct {
	Graphics                              shared.GraphicsState
	Backing, Pixels, GuestPixels, Context []byte
	Rectangle                             image.Rectangle
	Stride                                int
	Pending, PixelsDirty                  bool
	PresentCount                          any
}

func wipicCommitSnapshot(t *testing.T, f wipicCommitFixture) wipicCommitState {
	t.Helper()
	state := wipicCommitState{
		Graphics:     f.r.Services.Graphics.Snapshot(),
		Backing:      append([]byte(nil), f.backing.Pix...),
		Pending:      f.r.WipicScreenPending,
		PixelsDirty:  f.r.Graphics[f.graphics].PixelsDirty,
		PresentCount: f.r.PresentCount,
	}
	if f.r.frame != nil {
		state.Pixels = append([]byte(nil), f.r.frame.Pix...)
		state.Rectangle, state.Stride = f.r.frame.Rect, f.r.frame.Stride
	}
	if fb := f.r.wipicFramebuffers[f.handle]; fb != nil {
		state.GuestPixels = make([]byte, fb.stride*fb.height)
		check(t, f.r.CPU.ReadMemory(fb.pixels, state.GuestPixels))
	}
	var err error
	state.Context, err = f.r.CPU.SaveContext()
	check(t, err)
	return state
}

func wipicCommitInvoke(call func() error) (err error, panicked any) {
	defer func() { panicked = recover() }()
	err = call()
	return
}

func wipicCommitCompare(t *testing.T, got, want wipicCommitFixture) (error, any) {
	t.Helper()
	gotErr, gotPanic := wipicCommitInvoke(func() error { return got.r.presentWIPICFramebuffer(got.handle) })
	wantErr, wantPanic := wipicCommitInvoke(func() error { return wipicCommitLiteralOld(want.r, want.handle) })
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) ||
		fmt.Sprint(gotPanic) != fmt.Sprint(wantPanic) || reflect.TypeOf(gotPanic) != reflect.TypeOf(wantPanic) {
		t.Fatalf("error/panic differs: got %T/%T want %T/%T", gotErr, gotPanic, wantErr, wantPanic)
	}
	for _, cause := range []error{cpu.ErrInvalidAddress, shared.ErrWrongOwner, shared.ErrLimitExceeded, wipicCommitReadFault} {
		if errors.Is(gotErr, cause) != errors.Is(wantErr, cause) {
			t.Fatal("error identity differs")
		}
	}
	// A failed read is armed only for the production operation, not its observer.
	for _, f := range []wipicCommitFixture{got, want} {
		if spy, ok := f.r.CPU.(*wipicCommitFaultBackend); ok {
			spy.armed = false
		}
	}
	if !reflect.DeepEqual(wipicCommitSnapshot(t, got), wipicCommitSnapshot(t, want)) {
		t.Fatal("full graphics/public snapshot, host/guest pixels, CPU context or presentation fields differ")
	}
	return gotErr, gotPanic
}

func TestKTFWIPICPresentCommitLiteralLayouts(t *testing.T) {
	for _, tc := range []struct {
		name           string
		parent, target image.Rectangle
		mutation       string
		panics         bool
	}{
		{"equal", image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2), "", false},
		{"preserve_margins", image.Rect(0, 0, 5, 4), image.Rect(0, 0, 5, 4), "", false},
		{"crop_oversized", image.Rect(0, 0, 2, 1), image.Rect(0, 0, 2, 1), "", false},
		{"nonzero_origin", image.Rect(7, 11, 10, 13), image.Rect(7, 11, 10, 13), "", false},
		{"padded_subimage", image.Rect(4, 6, 11, 12), image.Rect(6, 7, 9, 9), "", false},
		{"negative_origin", image.Rect(-4, -3, -1, -1), image.Rect(-4, -3, -1, -1), "", false},
		{"empty_target", image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2), "empty", false},
		{"zero_stride_literal_behavior", image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2), "zero_stride", false},
		{"negative_stride_partial_copy_panic", image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2), "negative_stride", true},
		{"short_pixels_len5_cap24_literal_no_panic", image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2), "short", false},
		{"short_pixels_len5_cap5_literal_panic", image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2), "short_cap", true},
		{"nil_pixels_literal_panic", image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2), "nil", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, want := wipicCommitNewFixture(t, tc.parent, tc.target), wipicCommitNewFixture(t, tc.parent, tc.target)
			for _, f := range []wipicCommitFixture{got, want} {
				switch tc.mutation {
				case "empty":
					f.r.frame.Rect = image.Rectangle{}
				case "zero_stride":
					f.r.frame.Stride = 0
				case "negative_stride":
					f.r.frame.Stride = -4
				case "short":
					f.r.frame.Pix = f.r.frame.Pix[:5]
					if len(f.r.frame.Pix) != 5 || cap(f.r.frame.Pix) != 24 {
						t.Fatal("fixture must retain len=5/cap=24")
					}
				case "short_cap":
					f.r.frame.Pix = f.r.frame.Pix[:5:5]
					if len(f.r.frame.Pix) != 5 || cap(f.r.frame.Pix) != 5 {
						t.Fatal("fixture must limit len=5/cap=5")
					}
				case "nil":
					f.r.frame.Pix = nil
				}
			}
			err, panicked := wipicCommitCompare(t, got, want)
			if err != nil || (panicked != nil) != tc.panics {
				t.Fatal("unexpected literal outcome")
			}
			frame := got.r.Services.Graphics.LastFrame()
			if frame.Sequence != 1 || frame.Width != 3 || frame.Height != 2 || !bytes.Equal(frame.RGBA[:4], []byte{255, 0, 0, 255}) {
				t.Fatal("fixture did not commit its literal RGB565 red pixel exactly once")
			}
			if !tc.panics && got.r.PresentCount != 1 {
				t.Fatal("success retirement/presentation count differs")
			}
		})
	}
}

var wipicCommitReadFault = errors.New("synthetic WIPI-C framebuffer read fault")

type wipicCommitFaultBackend struct {
	cpu.Backend
	address  uint32
	armed    bool
	attempts int
}

func (b *wipicCommitFaultBackend) ReadMemory(address uint32, data []byte) error {
	if b.armed && address == b.address {
		b.attempts++
		return wipicCommitReadFault
	}
	return b.Backend.ReadMemory(address, data)
}

func TestKTFWIPICPresentCommitLiteralErrors(t *testing.T) {
	for _, name := range []string{"missing_handle", "nil_host_frame", "read_fault", "wrong_owner", "sequence_exhausted"} {
		t.Run(name, func(t *testing.T) {
			got, want := wipicCommitNewFixture(t, image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2)), wipicCommitNewFixture(t, image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2))
			for _, f := range []wipicCommitFixture{got, want} {
				switch name {
				case "missing_handle":
					delete(f.r.wipicFramebuffers, f.handle)
				case "nil_host_frame":
					f.r.frame = nil
				case "read_fault":
					f.r.CPU = &wipicCommitFaultBackend{Backend: f.r.CPU, address: f.r.wipicFramebuffers[f.handle].pixels, armed: true}
				case "wrong_owner":
					f.r.ServiceOwner++
				case "sequence_exhausted":
					check(t, wipicCommitLiteralOld(f.r, f.handle))
					state := f.r.Services.Graphics.Snapshot()
					state.PresentSequence = math.MaxUint64
					state.LastFrame.Sequence = math.MaxUint64
					check(t, f.r.Services.Graphics.Restore(state))
				}
			}
			err, panicked := wipicCommitCompare(t, got, want)
			if panicked != nil {
				t.Fatal("unexpected panic")
			}
			switch name {
			case "missing_handle", "nil_host_frame":
				if err != nil {
					t.Fatal("literal no-op failed")
				}
			case "read_fault":
				if !errors.Is(err, wipicCommitReadFault) || got.r.CPU.(*wipicCommitFaultBackend).attempts != 1 || want.r.CPU.(*wipicCommitFaultBackend).attempts != 1 {
					t.Fatal("fault probe/identity changed")
				}
			case "wrong_owner":
				if !errors.Is(err, shared.ErrWrongOwner) {
					t.Fatal("owner error changed")
				}
			case "sequence_exhausted":
				if !errors.Is(err, shared.ErrLimitExceeded) {
					t.Fatal("sequence overflow error changed")
				}
			}
		})
	}
}

func TestKTFWIPICPresentCommitLiteralObservers(t *testing.T) {
	for _, name := range []string{"repeated", "unqueued_guest_write", "offscreen", "restore_and_retained_snapshot"} {
		t.Run(name, func(t *testing.T) {
			got, want := wipicCommitNewFixture(t, image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2)), wipicCommitNewFixture(t, image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2))
			if name == "offscreen" {
				for _, f := range []*wipicCommitFixture{&got, &want} {
					var err error
					f.handle, err = f.r.createWIPICFramebuffer(2, 1, false)
					check(t, err)
					wipicCommitSeed(t, *f, 0)
				}
			}
			if err, p := wipicCommitCompare(t, got, want); err != nil || p != nil {
				t.Fatal("first commit failed")
			}
			old := got.r.Services.Graphics.LastFrame()
			oldHash := sha256.Sum256(old.RGBA)
			if old.Sequence != 1 || old.Hash != oldHash {
				t.Fatal("literal committed identity is missing")
			}
			for _, f := range []wipicCommitFixture{got, want} {
				// Host writes and unpresented guest writes cannot mutate retained frames.
				f.r.frame.Pix[0] ^= 0xff
				if name != "repeated" {
					wipicCommitSeed(t, f, 1)
				}
				f.r.WipicScreenPending = false
				if name == "restore_and_retained_snapshot" {
					check(t, f.r.Services.Graphics.Restore(f.r.Services.Graphics.Snapshot()))
				}
			}
			ready, stop, done := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
			go func() {
				ok := sha256.Sum256(old.RGBA) == oldHash
				close(ready)
				for {
					select {
					case <-stop:
						done <- ok
						return
					default:
						if sha256.Sum256(old.RGBA) != oldHash {
							ok = false
						}
						goruntime.Gosched() // Portable cooperative observer on JS/WASM.
					}
				}
			}()
			<-ready
			err, panicked := func() (error, any) {
				defer close(stop) // Also release observer if a comparison calls Fatal.
				return wipicCommitCompare(t, got, want)
			}()
			stable := <-done
			if err != nil || panicked != nil {
				t.Fatal("second commit failed")
			}
			if !stable {
				t.Fatal("a previously returned observer snapshot changed during producer work")
			}
			sequence, hash := got.r.Services.Graphics.LastFramePresentation()
			wantSequence, wantHash := want.r.Services.Graphics.LastFramePresentation()
			if sequence != 2 || sequence != wantSequence || hash != wantHash || got.r.PresentCount != 2 {
				t.Fatal("observer sequence/hash/count changed")
			}
			if name == "repeated" && hash != oldHash {
				t.Fatal("same content changed hash")
			}
			if name != "repeated" && hash == oldHash {
				t.Fatal("changed source was not freshly imported")
			}
			if !bytes.Equal(old.RGBA[:4], []byte{255, 0, 0, 255}) || old.Sequence != 1 {
				t.Fatal("retained snapshot aliases live storage")
			}
		})
	}
}
