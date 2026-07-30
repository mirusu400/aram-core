package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestParseKeyEvents(t *testing.T) {
	events, err := parseKeyEvents("press:53,up:0x32,down:-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 ||
		events[0] != (keyEvent{code: 53, pressed: true}) ||
		events[1] != (keyEvent{code: 0x32, pressed: false}) ||
		events[2] != (keyEvent{code: -1, pressed: true}) {
		t.Fatalf("events = %#v", events)
	}
	if _, err := parseKeyEvents("tap:5"); err == nil {
		t.Fatal("parseKeyEvents unexpectedly accepted tap")
	}
}

func TestWritePNG(t *testing.T) {
	name := filepath.Join(t.TempDir(), "frame.png")
	rgba := []byte{
		255, 0, 0, 255,
		0, 255, 0, 255,
	}
	if err := writePNG(name, 2, 1, rgba); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, err := png.DecodeConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != 2 || config.Height != 1 {
		t.Fatalf("PNG geometry = %dx%d", config.Width, config.Height)
	}
}
