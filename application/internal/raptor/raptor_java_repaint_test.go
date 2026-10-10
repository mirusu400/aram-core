package raptor

import (
	"context"
	"image"
	"testing"

	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorJavaCardRepaintRegion(t *testing.T) {
	for _, test := range []struct {
		name     string
		requests [][]uint32
		want     image.Rectangle
	}{
		{"partial", [][]uint32{{3, 2, 5, 4}}, image.Rect(3, 2, 8, 6)},
		{"union", [][]uint32{{3, 2, 5, 4}, {7, 5, 4, 3}}, image.Rect(3, 2, 11, 8)},
		{"full overrides partial", [][]uint32{{3, 2, 5, 4}, nil}, image.Rect(0, 0, 16, 12)},
		{"full stays full", [][]uint32{nil, {3, 2, 5, 4}}, image.Rect(0, 0, 16, 12)},
		{"clip to card", [][]uint32{{^uint32(0), ^uint32(0), 5, 5}}, image.Rect(0, 0, 4, 4)},
		{"overflow is outside", [][]uint32{{0x7fffffff, 0, 0x7fffffff, 4}, {3, 2, 5, 4}}, image.Rect(3, 2, 8, 6)},
		{"empty ignored", [][]uint32{{3, 2, 5, 4}, {0, 0, 0, 1}}, image.Rect(3, 2, 8, 6)},
	} {
		t.Run(test.name, func(t *testing.T) {
			public := newPublicRuntime(t)
			runtime := &Runtime{CPU: public.CPU, Public: public,
				resolvedImports: make(map[raptorImportKey]uint64), importSlotByKey: make(map[raptorImportKey]uint32)}
			java, err := runtime.ensureJavaRuntime()
			check(t, err)
			holder, err := public.Heap.Allocate(80, true)
			check(t, err)
			class := &raptorJavaClass{Name: "app/Card", Holder: holder, parentName: "org/kwis/msp/lcdui/Card",
				methods: []raptorJavaDeclaredMethod{{Name: "paint", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;)V", Body: 0x20000}}}
			java.classes[holder], java.ClassByName[class.Name] = class, class
			card, err := runtime.NewRaptorJavaObject(holder)
			check(t, err)
			java.currentCard = card
			for _, request := range test.requests {
				check(t, public.CPU.WriteRegister(cpu.RegisterR0, card))
				descriptor := "()V"
				if len(request) == 4 {
					descriptor = "(IIII)V"
					for i := 0; i < 3; i++ {
						check(t, public.CPU.WriteRegister(uint32(i+1), request[i]))
					}
					sp, err := public.CPU.ReadRegister(cpu.RegisterSP)
					check(t, err)
					check(t, public.WriteU32(sp, request[3]))
				}
				_, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
					className: "org/kwis/msp/lcdui/Card", Name: "repaint", descriptor: descriptor})
				check(t, err)
			}
			// Pending damage must survive a save before the next paint.
			state, err := captureJavaState(java)
			check(t, err)
			clear(java.dirtyCardRegions)
			check(t, restoreJavaState(java, state))
			paints := 0
			public.InvokeSync = func(ctx context.Context, callback wipirt.GuestCallback) (uint32, error) {
				paints++
				var clip [4]int
				for i, name := range []string{"getClipX", "getClipY", "getClipWidth", "getClipHeight"} {
					check(t, public.CPU.WriteRegister(cpu.RegisterR0, callback.Args[1]))
					result, err := runtime.callJavaHostMethod(ctx, raptorJavaMethod{className: "org/kwis/msp/lcdui/Graphics", Name: name, descriptor: "()I"})
					check(t, err)
					clip[i] = int(int32(result.Low))
				}
				if got := image.Rect(clip[0], clip[1], clip[0]+clip[2], clip[1]+clip[3]); got != test.want {
					t.Fatalf("paint clip = %v, want %v", got, test.want)
				}
				return 0, nil
			}
			painted, err := runtime.RepaintDirtyJavaCard(context.Background())
			check(t, err)
			if !painted || paints != 1 {
				t.Fatalf("painted=%t callbacks=%d", painted, paints)
			}
			if len(java.dirtyCardRegions) != 0 {
				t.Fatal("paint left pending damage")
			}
			painted, err = runtime.RepaintDirtyJavaCard(context.Background())
			check(t, err)
			if painted {
				t.Fatal("clean card repainted")
			}
		})
	}
}
