package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// These exact LGT titles compare MIDP key codes directly (Snowboard) or call
// Canvas.getGameAction on them (Golf). A paired, same-frame run distinguishes
// a key response from animation that would have happened without input.
func TestJ2MELGTInputChangesReferenceFrames(t *testing.T) {
	tests := []struct {
		name, digest  string
		end, actionAt int
		prefix        []int
		control       string
	}{
		{
			name:   "issue343_snowboard_notice_select",
			digest: "735b4f82ffc9a7b3066a5d764a3f114e0c4c29bc1bd95dc96c873e96811c5c13",
			end:    300, actionAt: 180, control: "select",
		},
		{
			name:   "issue344_golf_submenu_right",
			digest: "1ceb6dd33788e27fdad9df8929a12b8a721196b9b86125b60aa445259baea4e8",
			end:    1020, actionAt: 990, prefix: []int{180, 720, 900}, control: "right",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, data := findAuthorizedPackage(t, test.digest)
			run := func(action bool) [32]byte {
				t.Helper()
				ctx := context.Background()
				machine, err := NewFactory().Create(ctx, machinecore.Source{
					Name: filepath.Base(path), Path: path,
					ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
				})
				if err != nil {
					t.Fatal(err)
				}
				defer machine.Close()
				if err := machine.Start(ctx); err != nil {
					t.Fatal(err)
				}
				press := func(control string) {
					for _, event := range []machinecore.InputEvent{
						{Control: control, Pressed: true},
						{Control: control, Pressed: false, At: time.Millisecond},
					} {
						if err := machine.QueueInput(event); err != nil {
							t.Fatal(err)
						}
					}
				}
				for frame := 0; frame < test.end; frame++ {
					for _, at := range test.prefix {
						if frame == at {
							press("select")
						}
					}
					if action && frame == test.actionAt {
						press(test.control)
					}
					if err := machine.StepFrame(ctx); err != nil {
						t.Fatalf("frame %d: %v", frame, err)
					}
				}
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, machine.Framebuffer()); err != nil {
					t.Fatal(err)
				}
				return sha256.Sum256(encoded.Bytes())
			}
			if baseline, pressed := run(false), run(true); baseline == pressed {
				t.Fatalf("%s did not change the rendered frame", test.control)
			}
		})
	}
}
