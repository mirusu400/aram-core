package skvmhost

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

// syntheticLayoutClass builds class "Screen" extending superclass, with one
// method whose code is code. Constant #10 is a Methodref to
// owner.getHeight()I.
func syntheticLayoutClass(t *testing.T, superclass, owner string, code []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	u2 := func(value uint16) { _ = binary.Write(&out, binary.BigEndian, value) }
	u4 := func(value uint32) { _ = binary.Write(&out, binary.BigEndian, value) }
	utf := func(value string) {
		out.WriteByte(1)
		u2(uint16(len(value)))
		out.WriteString(value)
	}
	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(13)
	utf("Screen")    // 1
	out.WriteByte(7) // 2: class Screen
	u2(1)
	utf(superclass)  // 3
	out.WriteByte(7) // 4: class superclass
	u2(3)
	utf("getHeight")  // 5
	utf("()I")        // 6
	out.WriteByte(12) // 7: NameAndType getHeight ()I
	u2(5)
	u2(6)
	utf(owner)       // 8
	out.WriteByte(7) // 9: class owner
	u2(8)
	out.WriteByte(10) // 10: Methodref owner.getHeight()I
	u2(9)
	u2(7)
	utf("layout") // 11
	utf("Code")   // 12
	u2(0x0021)
	u2(2)
	u2(4)
	u2(0) // interfaces
	u2(0) // fields
	u2(1) // methods
	u2(0x0001)
	u2(11)
	u2(6)
	u2(1)
	u2(12)
	u4(uint32(12 + len(code)))
	u2(4) // max stack
	u2(2) // max locals
	u4(uint32(len(code)))
	out.Write(code)
	u2(0) // handlers
	u2(0) // code attributes
	u2(0) // class attributes
	return out.Bytes()
}

const syntheticGetHeight = "\x2a\xb6\x00\x0a" // aload_0; invokevirtual #10

func TestSKVMTitleRestoresSystemStripReadsTheCanvasHeightIdiom(t *testing.T) {
	const canvas = "javax/microedition/lcdui/Canvas"
	// lookupswitch at pc 1 with one pair whose key and offset bytes spell
	// the idiom; a byte scan would match it, an instruction walk must not.
	hiddenInOperands := []byte{0x03, 0xab, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x14, // default
		0x00, 0x00, 0x00, 0x01, // npairs
		0x2a, 0xb6, 0x00, 0x0a, // match: aload_0 invokevirtual #10
		0x10, 0x10, 0x60, 0xac, // offset: bipush 16 iadd ireturn
		0x03, 0xac,
	}
	// tableswitch at pc 1 (two padding bytes), then the idiom.
	afterTableSwitch := []byte{0x03, 0xaa, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x13, // default
		0x00, 0x00, 0x00, 0x00, // low
		0x00, 0x00, 0x00, 0x00, // high
		0x00, 0x00, 0x00, 0x13, // offset 0
		0x2a, 0xb6, 0x00, 0x0a, 0x10, 0x10, 0x60, 0xac,
	}
	for _, test := range []struct {
		name, owner string
		code        []byte
		want        bool
	}{
		{"own canvas subclass", "Screen", []byte(syntheticGetHeight + "\x10\x10\x60\xac"), true},
		{"midp canvas", canvas, []byte(syntheticGetHeight + "\x10\x10\x60\xac"), true},
		{"sipush", "Screen", []byte(syntheticGetHeight + "\x11\x00\x10\x60\xac"), true},
		{"after tableswitch", "Screen", afterTableSwitch, true},
		{"image height", "javax/microedition/lcdui/Image", []byte(syntheticGetHeight + "\x10\x10\x60\xac"), false},
		{"other strip", "Screen", []byte(syntheticGetHeight + "\x10\x08\x60\xac"), false},
		{"subtracted", "Screen", []byte(syntheticGetHeight + "\x10\x10\x64\xac"), false},
		{"height alone", "Screen", []byte(syntheticGetHeight + "\xac"), false},
		{"switch operands", "Screen", hiddenInOperands, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			class := syntheticLayoutClass(t, canvas, test.owner, test.code)
			if _, err := skengine.ParseClass("Screen.class", class); err != nil {
				t.Fatalf("synthetic class: %v", err)
			}
			got := skvmTitleRestoresSystemStrip(map[string][]byte{"Screen": class})
			if got != test.want {
				t.Fatalf("skvmTitleRestoresSystemStrip() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSKVMSystemStripIdiomEnablesTheCanvasInset(t *testing.T) {
	idiom := syntheticLayoutClass(t, "javax/microedition/lcdui/Canvas", "Screen",
		[]byte(syntheticGetHeight+"\x10\x10\x60\xac"))
	plain := syntheticLayoutClass(t, "javax/microedition/lcdui/Canvas", "Screen",
		[]byte(syntheticGetHeight+"\xac"))
	inset := shared.DeviceQuirk{Name: skengine.CanvasHeightInset16Quirk, Enabled: true}
	for _, test := range []struct {
		name   string
		digest string
		class  []byte
		key    skloader.Descriptor
		want   []shared.DeviceQuirk
	}{
		{"idiom", "", idiom, skloader.Descriptor{MainClass: "Screen"}, []shared.DeviceQuirk{inset}},
		{"no idiom", "", plain, skloader.Descriptor{MainClass: "Screen"}, nil},
		// An exact-title entry that already asks for the inset adds it once.
		{"entry and idiom", dragonKnightEXSKTSHA256, idiom,
			skloader.Descriptor{MainClass: "PNJDKEx", ProgramName: "0053597505"}, []shared.DeviceQuirk{inset}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg := skloader.Package{
				Descriptor: test.key,
				Classes:    map[string]skloader.Class{"Screen": {Name: "Screen", Data: test.class}},
			}
			config := shared.DefaultConfig()
			applySKVMTitleCompatibility(&config, machinecore.Source{SHA256: test.digest}, pkg, image.Pt(120, 160))
			if len(config.Device.Quirks) != len(test.want) {
				t.Fatalf("quirks = %+v, want %+v", config.Device.Quirks, test.want)
			}
			for index := range test.want {
				if config.Device.Quirks[index] != test.want[index] {
					t.Fatalf("quirks = %+v, want %+v", config.Device.Quirks, test.want)
				}
			}
			if err := config.Device.Validate(); err != nil {
				t.Fatalf("device config: %v", err)
			}
		})
	}
}

// columnFrame paints a 240x320 frame black and fills [left, left+width) with
// a two-colour pattern, so those columns are not solid.
func columnFrame(left, width int) *image.NRGBA {
	frame := image.NewNRGBA(image.Rect(0, 0, 240, 320))
	for y := range 320 {
		for x := range 240 {
			value := color.NRGBA{A: 0xff}
			if x >= left && x < left+width {
				value = color.NRGBA{R: uint8(y), G: 0x80, A: 0xff}
			}
			frame.SetNRGBA(x, y, value)
		}
	}
	return frame
}

func TestSKVMColumnEvidenceNeedsAnExactlyFilledCentredColumn(t *testing.T) {
	fallback := image.Pt(240, 320)
	blank := image.NewNRGBA(image.Rect(0, 0, 240, 320))
	for _, test := range []struct {
		name   string
		frames []*image.NRGBA
		want   image.Point
	}{
		{"176 column", []*image.NRGBA{columnFrame(32, 176), columnFrame(60, 120)}, image.Pt(176, 220)},
		{"128 column", []*image.NRGBA{columnFrame(56, 128)}, image.Pt(128, 160)},
		{"120 column", []*image.NRGBA{columnFrame(60, 120)}, image.Pt(120, 160)},
		// A centred dialog of another width is no evidence of a handset.
		{"centred dialog", []*image.NRGBA{columnFrame(45, 150)}, fallback},
		// Content off-centre inside the column fills no handset exactly.
		{"off centre", []*image.NRGBA{columnFrame(40, 150)}, fallback},
		{"blank", []*image.NRGBA{blank}, fallback},
	} {
		t.Run(test.name, func(t *testing.T) {
			var evidence skvmColumnEvidence
			for _, frame := range test.frames {
				if !evidence.observe(frame) {
					t.Fatal("frame rejected")
				}
			}
			if got := evidence.design(fallback); got != test.want {
				t.Fatalf("design = %v, want %v", got, test.want)
			}
		})
	}

	// Anything drawn in the outer bands ends the probe on the large canvas.
	var evidence skvmColumnEvidence
	if !evidence.observe(columnFrame(32, 176)) || evidence.observe(columnFrame(20, 200)) {
		t.Fatal("content outside the widest handset column was accepted")
	}
}

func TestSKVMCanvasProbeOnlySecondGuessesTheFallback(t *testing.T) {
	pkg := skloader.Package{Descriptor: skloader.Descriptor{MainClass: "Screen", ProgramName: "1"}}
	digest := "00000000000000000000000000000000000000000000000000000000000000aa"
	fallback := image.Pt(240, 320)
	for _, test := range []struct {
		name             string
		source           machinecore.Source
		pkg              skloader.Package
		inferred, canvas image.Point
		want             bool
	}{
		{"fallback", machinecore.Source{SHA256: digest}, pkg, fallback, fallback, true},
		{"no digest", machinecore.Source{}, pkg, fallback, fallback, false},
		{"inferred handset", machinecore.Source{SHA256: digest}, pkg, image.Pt(176, 208), image.Pt(176, 208), false},
		{"replaced canvas", machinecore.Source{SHA256: digest}, pkg, fallback, image.Pt(176, 220), false},
		// An exact-title entry was settled by hand; the probe leaves it.
		{"pinned title", machinecore.Source{SHA256: "9a2cf5ffc9d30641bb482f952b03cb7e9daf46d7012a69a2ad83db62599a53ac"},
			skloader.Package{Descriptor: skloader.Descriptor{MainClass: "Game", ProgramName: "0048926642"}},
			fallback, fallback, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := skvmCanvasNeedsProbe(test.source, test.pkg, test.inferred, test.canvas); got != test.want {
				t.Fatalf("skvmCanvasNeedsProbe() = %v, want %v", got, test.want)
			}
		})
	}
}
