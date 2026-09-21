// Command aram-j2me-run runs a J2ME archive through the product machine.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mirusu400/aram-core/application"
	"github.com/mirusu400/aram-core/core"
)

type report struct {
	Path       string `json:"path"`
	Frames     int    `json:"frames"`
	State      string `json:"state,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Pixels     int    `json:"non_black_pixels,omitempty"`
	FrameHash  string `json:"framebuffer_sha256,omitempty"`
	Screenshot string `json:"screenshot,omitempty"`
	Error      string `json:"error,omitempty"`
}

type keyAction struct {
	frame   int
	control string
}

func main() {
	frames := flag.Int("frames", 5000, "maximum frames to step")
	keys := flag.String("keys", "", "comma-separated frame:control presses")
	screenshot := flag.String("screenshot", "", "write the final framebuffer as PNG")
	flag.Parse()
	if flag.NArg() != 1 || *frames < 0 {
		fmt.Fprintln(os.Stderr, "usage: aram-j2me-run [-frames N] [-keys 600:select] [-screenshot path] package.zip")
		os.Exit(2)
	}
	actions, err := parseActions(*keys)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	r, err := run(context.Background(), flag.Arg(0), *frames, actions, *screenshot)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(r); encodeErr != nil {
		fmt.Fprintln(os.Stderr, encodeErr)
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}

func parseActions(script string) ([]keyAction, error) {
	if script == "" {
		return nil, nil
	}
	var actions []keyAction
	last := -1
	for _, item := range strings.Split(script, ",") {
		at, control, ok := strings.Cut(item, ":")
		if !ok || strings.TrimSpace(control) == "" {
			return nil, fmt.Errorf("invalid key action %q", item)
		}
		frame, err := strconv.Atoi(strings.TrimSpace(at))
		if err != nil || frame < 0 || frame < last {
			return nil, fmt.Errorf("invalid or unordered key frame %q", at)
		}
		actions = append(actions, keyAction{frame, strings.TrimSpace(control)})
		last = frame
	}
	return actions, nil
}

func run(ctx context.Context, path string, frames int, actions []keyAction, screenshot string) (report, error) {
	r := report{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		r.Error = err.Error()
		return r, err
	}
	machine, err := application.NewFactory().Create(ctx, core.Source{
		Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		r.Error = err.Error()
		return r, err
	}
	defer machine.Close()
	if err = machine.Start(ctx); err == nil {
		for frame := 0; frame < frames; frame++ {
			for len(actions) > 0 && actions[0].frame == frame {
				control := actions[0].control
				actions = actions[1:]
				if err = machine.QueueInput(core.InputEvent{Control: control, Pressed: true}); err != nil {
					break
				}
				if err = machine.QueueInput(core.InputEvent{Control: control, Pressed: false}); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
			if err = machine.StepFrame(ctx); err != nil {
				break
			}
			r.Frames++
		}
	}
	r.State = machine.State().String()
	frame := machine.Framebuffer()
	bounds := frame.Bounds()
	r.Width, r.Height = bounds.Dx(), bounds.Dy()
	pixels := make([]byte, 0, r.Width*r.Height*4)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, alpha := frame.At(x, y).RGBA()
			if red != 0 || green != 0 || blue != 0 {
				r.Pixels++
			}
			pixels = append(pixels, byte(red>>8), byte(green>>8), byte(blue>>8), byte(alpha>>8))
		}
	}
	hash := sha256.Sum256(pixels)
	r.FrameHash = hex.EncodeToString(hash[:])
	if screenshot != "" {
		captureErr := writePNG(screenshot, frame)
		if captureErr == nil {
			r.Screenshot = screenshot
		} else if err == nil {
			err = captureErr
		}
	}
	if err != nil {
		r.Error = err.Error()
	}
	return r, err
}

func writePNG(path string, frame image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encodeErr := png.Encode(file, frame)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}
