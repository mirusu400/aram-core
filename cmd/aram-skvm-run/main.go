// Command aram-skvm-run boots an SKVM MIDlet in the portable headless
// interpreter and emits a machine-readable execution report.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
	"github.com/mirusu400/aram-core/skvm"
)

type runReport struct {
	Path              string            `json:"path"`
	MainClass         string            `json:"main_class"`
	MIDletReference   uint32            `json:"midlet_reference,omitempty"`
	CurrentDisplay    uint32            `json:"current_display,omitempty"`
	Instructions      uint64            `json:"instructions"`
	FramebufferSHA256 string            `json:"framebuffer_sha256"`
	NonTransparent    int               `json:"non_transparent_pixels"`
	Error             string            `json:"error,omitempty"`
	Trace             []skvm.TraceEvent `json:"trace,omitempty"`
	KeyEvents         int               `json:"key_events,omitempty"`
	Screenshot        string            `json:"screenshot,omitempty"`
}

func main() {
	var instructionLimit uint64
	var traceCount int
	var keyScript string
	var screenshot string
	flag.Uint64Var(
		&instructionLimit,
		"instructions",
		skvm.DefaultInstructionLimit,
		"maximum Java bytecode instructions",
	)
	flag.IntVar(&traceCount, "trace", 64, "number of trailing instructions to report")
	flag.StringVar(
		&keyScript,
		"keys",
		"",
		"comma-separated key events such as press:53,release:53",
	)
	flag.StringVar(&screenshot, "screenshot", "", "write the final framebuffer as PNG")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: aram-skvm-run [options] <package.zip>")
		os.Exit(2)
	}
	keyEvents, err := parseKeyEvents(keyScript)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aram-skvm-run:", err)
		os.Exit(2)
	}
	report, err := boot(
		context.Background(),
		flag.Arg(0),
		instructionLimit,
		traceCount,
		keyEvents,
		screenshot,
	)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(report); encodeErr != nil {
		fmt.Fprintln(os.Stderr, "aram-skvm-run:", encodeErr)
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}

func boot(
	ctx context.Context,
	name string,
	instructionLimit uint64,
	traceCount int,
	keyEvents []keyEvent,
	screenshot string,
) (runReport, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return runReport{Path: name, Error: err.Error()}, err
	}
	pkg, err := skloader.Inspect(data)
	if err != nil {
		return runReport{Path: name, Error: err.Error()}, err
	}
	classData := make(map[string][]byte, len(pkg.Classes))
	for className, class := range pkg.Classes {
		classData[className] = class.Data
	}
	machine, err := skvm.New(classData)
	if err != nil {
		return runReport{
			Path:      name,
			MainClass: pkg.Descriptor.MainClass,
			Error:     err.Error(),
		}, err
	}
	machine.InstructionLimit = instructionLimit
	machine.SetResources(pkg.Resources)
	machine.SetProperties(pkg.Descriptor.Raw)
	if traceCount < 0 {
		traceCount = 0
	}
	trace := make([]skvm.TraceEvent, 0, traceCount)
	machine.SetTraceHook(func(event skvm.TraceEvent) error {
		if traceCount == 0 {
			return nil
		}
		if len(trace) == traceCount {
			copy(trace, trace[1:])
			trace[len(trace)-1] = event
		} else {
			trace = append(trace, event)
		}
		return nil
	})
	reference, bootErr := machine.Start(ctx, pkg.Descriptor.MainClass)
	if bootErr == nil && machine.CurrentDisplay() != 0 {
		bootErr = machine.ShowCurrent(ctx)
	}
	if bootErr == nil && machine.CurrentDisplay() != 0 {
		bootErr = machine.PaintCurrent(ctx)
	}
	if bootErr == nil && machine.CurrentDisplay() != 0 {
		for _, event := range keyEvents {
			if bootErr = machine.KeyEvent(ctx, event.code, event.pressed); bootErr != nil {
				break
			}
			if bootErr = machine.PaintCurrent(ctx); bootErr != nil {
				break
			}
		}
	}
	frame := machine.FrameRGBA()
	if bootErr == nil && screenshot != "" {
		bootErr = writePNG(screenshot, machine.ScreenWidth, machine.ScreenHeight, frame)
	}
	sum := sha256.Sum256(frame)
	nonTransparent := 0
	for offset := 3; offset < len(frame); offset += 4 {
		if frame[offset] != 0 {
			nonTransparent++
		}
	}
	result := runReport{
		Path:              name,
		MainClass:         pkg.Descriptor.MainClass,
		MIDletReference:   reference,
		CurrentDisplay:    machine.CurrentDisplay(),
		Instructions:      machine.Instructions,
		FramebufferSHA256: hex.EncodeToString(sum[:]),
		NonTransparent:    nonTransparent,
		Trace:             trace,
		KeyEvents:         len(keyEvents),
		Screenshot:        screenshot,
	}
	if bootErr != nil {
		result.Error = bootErr.Error()
	}
	return result, bootErr
}

type keyEvent struct {
	code    int32
	pressed bool
}

func parseKeyEvents(script string) ([]keyEvent, error) {
	if strings.TrimSpace(script) == "" {
		return nil, nil
	}
	parts := strings.Split(script, ",")
	events := make([]keyEvent, 0, len(parts))
	for _, part := range parts {
		kind, rawCode, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, fmt.Errorf("invalid key event %q", part)
		}
		code, err := strconv.ParseInt(strings.TrimSpace(rawCode), 0, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid key code %q: %w", rawCode, err)
		}
		var pressed bool
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "press", "down":
			pressed = true
		case "release", "up":
			pressed = false
		default:
			return nil, fmt.Errorf("invalid key action %q", kind)
		}
		events = append(events, keyEvent{code: int32(code), pressed: pressed})
	}
	return events, nil
}

func writePNG(name string, width, height int, rgba []byte) error {
	if len(rgba) != width*height*4 {
		return fmt.Errorf("framebuffer size %d does not match %dx%d RGBA", len(rgba), width, height)
	}
	output, err := os.Create(name)
	if err != nil {
		return err
	}
	pixels := &image.RGBA{
		Pix:    rgba,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}
	encodeErr := png.Encode(output, pixels)
	closeErr := output.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}
