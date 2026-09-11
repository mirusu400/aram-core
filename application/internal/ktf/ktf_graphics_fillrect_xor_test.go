package ktf

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// Generated pixels exercise the raster contract without title assets or fonts.
func TestKTFGraphicsFillRectXORAndCopy(t *testing.T) {
	for _, xor := range []bool{false, true} {
		t.Run(fmt.Sprintf("xor=%t", xor), func(t *testing.T) {
			runtime := newTestRuntime(t)
			runtime.JvmContext = allocWords(t, runtime, 3+128)
			runtime.frame = image.NewRGBA(image.Rect(0, 0, 16, 16))
			graphics, err := runtime.EnsureScreenGraphics()
			check(t, err)
			state := runtime.Graphics[graphics]
			background := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 255}
			paint := color.RGBA{R: 0xf0, G: 0x5a, B: 0xa5, A: 255}
			draw.Draw(runtime.frame, runtime.frame.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
			state.translate = image.Pt(3, 4)
			state.clip = image.Rect(2, 2, 5, 6)
			state.color = paint

			stack := guest.DefaultStackBase + 0x100
			check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, graphics))
			mode := uint32(0)
			if xor {
				mode = 1
			}
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, mode))
			_, err = runtime.handleGraphicsMethod("setXORMode", "(Z)V")
			check(t, err)

			fill := func() {
				// Guest (-2,-1,6,5) translates to (1,3)-(7,8), then clips.
				check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, ^uint32(1)))
				check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, ^uint32(0)))
				check(t, runtime.writeWords(stack, []uint32{6, 5}))
				_, err := runtime.handleGraphicsMethod("fillRect", "(IIII)V")
				check(t, err)
			}
			area := image.Rect(2, 3, 5, 6)
			for pass := 1; pass <= 2; pass++ {
				fill()
				for y := 0; y < 16; y++ {
					for x := 0; x < 16; x++ {
						want := background
						if image.Pt(x, y).In(area) {
							want = paint
							if xor {
								want = background
								if pass == 1 {
									want = color.RGBA{
										R: background.R ^ paint.R,
										G: background.G ^ paint.G,
										B: background.B ^ paint.B,
										A: 255,
									}
								}
							}
						}
						if got := runtime.frame.RGBAAt(x, y); got != want {
							t.Fatalf("pass %d pixel (%d,%d) = %v, want %v", pass, x, y, got, want)
						}
					}
				}
			}
		})
	}
}
