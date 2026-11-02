package gvm_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

// push encodes each word with opcode06 (BE16 immediate).
func push(words ...uint16) []byte {
	var code []byte
	for _, word := range words {
		code = append(code, 0x06, byte(word>>8), byte(word))
	}
	return code
}

func runToHalt(t *testing.T, program []byte, space gvm.AddressSpace, config *gvm.ServiceConfig) *gvm.VM {
	t.Helper()
	vm, err := gvm.NewWithAddressSpaceAndServices(append(program, 0xff), 0, space, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1000); err != nil || !vm.Halted() {
		t.Fatalf("Run = %v halted=%v stack=%v", err, vm.Halted(), vm.Stack())
	}
	return vm
}

func mediaRecord(t *testing.T, vm *gvm.VM, index int) []byte {
	t.Helper()
	state, err := vm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return state.Media[index]
}

func words(values ...uint16) []byte {
	data := make([]byte, 2*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint16(data[2*i:], value)
	}
	return data
}

func TestIndexedAddImmediateAndStoreTop(t *testing.T) {
	vm := symbolCopyFixture(t, []byte{
		0x37, 0, 1, 1, 0xfe, // destination[symbol1[1]=2] += -2
		0x38, 0, 3, 5, // destination[symbol3[0]=3] += 5
		0x05, 9, 0x48, 0, 4, 1, // destination[symbol4[1]=1] = 9, keeping 9 on the stack
		0xff,
	})
	if err := vm.Run(10); err != nil || !vm.Halted() {
		t.Fatal(err)
	}
	destination, _ := vm.Symbol(0)
	if want := words(0, 9, 0xfffe, 5); !reflect.DeepEqual(destination, want) {
		t.Fatalf("destination = %x, want %x", destination, want)
	}
	if !reflect.DeepEqual(vm.Stack(), []uint16{9}) {
		t.Fatalf("stack = %v, want [9]", vm.Stack())
	}
}

func TestDecimalIntoMediaUsesNativeResize(t *testing.T) {
	config := &gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte{}}}}
	// "-123" plus NUL is five bytes; the native allocator rounds growth to six.
	vm := runToHalt(t, append(push(0, 0xff85), 0x84), gvm.AddressSpace{}, config)
	if len(vm.Stack()) != 0 {
		t.Fatalf("stack = %v", vm.Stack())
	}
	record := mediaRecord(t, vm, 0)
	if string(record) != "-123\x00\x00" {
		t.Fatalf("media = %q", record)
	}
}

func TestStringFormatOpcodes(t *testing.T) {
	for _, tc := range []struct {
		name, format, argument, want string
	}{
		{"plain", "name:%s!\x00", "hero\x00", "name:hero!\x00"},
		{"right aligned", "[%6s]\x00", "ab\x00", "[    ab]\x00"},
		{"left aligned", "[%-6s]\x00", "ab\x00", "[ab    ]\x00"},
		{"zero padded", "[%05s]\x00", "ab\x00", "[000ab]\x00"},
		{"precision", "[%.2s]\x00", "abcdef", "[ab]\x00"},
		{"other conversions are literal", "%d%%\x00", "x\x00", "d%\x00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := &gvm.ServiceConfig{Media: []gvm.MediaResource{
				{Data: []byte{}}, {Data: []byte(tc.format)}, {Data: []byte(tc.argument)},
			}}
			vm := runToHalt(t, append(push(0, 1, 2), 0x89), gvm.AddressSpace{}, config)
			record := mediaRecord(t, vm, 0)
			if got := string(record[:len(tc.want)]); got != tc.want || len(vm.Stack()) != 0 {
				t.Fatalf("media = %q, want prefix %q (stack %v)", record, tc.want, vm.Stack())
			}
		})
	}
	config := &gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte{}}, {Data: []byte("%d.%d.%d.%d-%d\x00")}}}
	vm := runToHalt(t, append(push(0, 1, 1, 2, 3, 4, 0xfffb), 0x8e), gvm.AddressSpace{}, config)
	if record := mediaRecord(t, vm, 0); string(record) != "1.2.3.4--5\x00" {
		t.Fatalf("8e media = %q", record)
	}
	config = &gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte{}}, {Data: []byte("%d:%d:%d:%d\x00")}}}
	vm = runToHalt(t, append(push(0, 1, 7, 8, 9, 10), 0x8d), gvm.AddressSpace{}, config)
	if record := mediaRecord(t, vm, 0); string(record[:9]) != "7:8:9:10\x00" || len(vm.Stack()) != 0 {
		t.Fatalf("8d media = %q stack=%v", record, vm.Stack())
	}
}

func TestMediaAddressCopies(t *testing.T) {
	space := gvm.AddressSpace{RAM: []byte("....ABCDEF.."), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 12}}}
	config := &gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte("xy")}, {Data: []byte("1234")}}}
	// 87: media0 = RAM word 2 (byte 4) + 1, three bytes: "BCD".
	vm := runToHalt(t, append(push(2, 1, 0, 3), 0x87), space, config)
	if record := mediaRecord(t, vm, 0); string(record) != "BCD\x00" || len(vm.Stack()) != 0 {
		t.Fatalf("87 media = %q stack=%v", record, vm.Stack())
	}
	// 88: RAM word 1 (byte 2) + 3 = byte 5 receives three bytes of media1.
	vm = runToHalt(t, append(push(1, 3, 1, 3), 0x88), space, config)
	if ram, _ := vm.Symbol(0); string(ram) != "....A123EF.." {
		t.Fatalf("88 RAM = %q", ram)
	}
	for _, tc := range []struct {
		name  string
		stack []uint16
		op    byte
		want  error
	}{
		{"source past region", []uint16{2, 6, 0, 3}, 0x87, gvm.ErrInvalidAddress},
		{"negative byte offset", []uint16{2, 0xffff, 0, 1}, 0x87, gvm.ErrInvalidAddress},
		{"count past record", []uint16{1, 0, 0, 3}, 0x88, gvm.ErrInvalidElement},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, err := gvm.NewWithAddressSpaceAndServices(append(push(tc.stack...), tc.op), 0, space, config)
			if err != nil {
				t.Fatal(err)
			}
			if err := vm.Run(10); !errors.Is(err, tc.want) {
				t.Fatalf("Run = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTrigonometryTable(t *testing.T) {
	for _, tc := range []struct {
		op    byte
		angle int16
		want  int16
	}{
		{0xa5, 0, 0}, {0xa5, 1, 2}, {0xa5, 2, 3}, {0xa5, 30, 50}, {0xa5, 89, 100},
		{0xa5, 150, 50}, {0xa5, 210, -50}, {0xa5, 330, -50}, {0xa5, -30, -50}, {0xa5, 390, 50},
		{0xa6, 0, 100}, {0xa6, 60, 50}, {0xa6, 90, 0}, {0xa6, 120, -50}, {0xa6, 270, 0},
		{0xa6, 300, 50}, {0xa6, -60, 50}, {0xa6, 179, -100},
	} {
		vm := runToHalt(t, append(push(uint16(tc.angle)), tc.op), gvm.AddressSpace{}, nil)
		if got := int16(vm.Stack()[0]); len(vm.Stack()) != 1 || got != tc.want {
			t.Fatalf("%02x(%d) = %d, want %d", tc.op, tc.angle, got, tc.want)
		}
	}
}

func TestSelectionArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  []byte
		space gvm.AddressSpace
		want  []uint16
	}{
		{"max of three", append(push(0xfffb, 7, 3), 0xae), gvm.AddressSpace{}, []uint16{7}},
		{"min of three", append(push(0xfffb, 7, 3), 0xb0), gvm.AddressSpace{}, []uint16{0xfffb}},
		{"strictly inside", append(push(10, 13, 4), 0xb7), gvm.AddressSpace{}, []uint16{1}},
		{"edge is outside", append(push(10, 14, 4), 0xb7), gvm.AddressSpace{}, []uint16{0}},
		{"no 16-bit wrap", append(push(0x7fff, 0x8000, 2), 0xb7), gvm.AddressSpace{}, []uint16{0}},
		{"first largest", append(push(0, 4), 0xb1), gvm.AddressSpace{RAM: words(3, 9, 9, 0xffff), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 8}}}, []uint16{1}},
		{"first smallest", append(push(0, 4), 0xb2), gvm.AddressSpace{RAM: words(3, 9, 9, 0xffff), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 8}}}, []uint16{3}},
		{"single element", append(push(0, 0), 0xb1), gvm.AddressSpace{RAM: words(3), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 2}}}, []uint16{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := runToHalt(t, tc.code, tc.space, &gvm.ServiceConfig{})
			if !reflect.DeepEqual(vm.Stack(), tc.want) {
				t.Fatalf("stack = %v, want %v", vm.Stack(), tc.want)
			}
		})
	}
}

type hostRequests struct {
	browser []string
	exits   int
}

func (h *hostRequests) RequestGVMBrowser(url string) error {
	h.browser = append(h.browser, url)
	return nil
}

func (h *hostRequests) RequestGVMExit() error {
	h.exits++
	return nil
}

func TestHostRequestsEndTheDispatch(t *testing.T) {
	host := &hostRequests{}
	config := &gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte("http://example.invalid/down\x00\x00")}}, HostRequest: host}
	vm, err := gvm.NewWithAddressSpaceAndServices(append(push(7, 0), 0xc4, 0x05, 1), 0, gvm.AddressSpace{}, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(10); err != nil || !vm.Halted() || vm.PC() != 7 {
		t.Fatalf("c4: err=%v halted=%v pc=%d", err, vm.Halted(), vm.PC())
	}
	if !reflect.DeepEqual(host.browser, []string{"http://example.invalid/down"}) || !reflect.DeepEqual(vm.Stack(), []uint16{7}) {
		t.Fatalf("browser=%q stack=%v", host.browser, vm.Stack())
	}
	vm, err = gvm.NewWithAddressSpaceAndServices([]byte{0xc5, 0x05, 1}, 0, gvm.AddressSpace{}, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(10); err != nil || !vm.Halted() || host.exits != 1 || len(vm.Stack()) != 0 {
		t.Fatalf("c5: err=%v halted=%v exits=%d", err, vm.Halted(), host.exits)
	}
	vm, err = gvm.NewWithAddressSpaceAndServices([]byte{0xc5}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); !errors.Is(err, gvm.ErrHostRequestUnavailable) {
		t.Fatalf("missing sink: %v", err)
	}
}

type ellipseCalls struct{ fill, outline [][4]int16 }

func (e *ellipseCalls) DrawGVMEllipse(x, y, rx, ry int16) error {
	e.fill = append(e.fill, [4]int16{x, y, rx, ry})
	return nil
}

func (e *ellipseCalls) DrawGVMEllipseOutline(x, y, rx, ry int16) error {
	e.outline = append(e.outline, [4]int16{x, y, rx, ry})
	return nil
}

func TestEllipseOpcodesSelectFillAndOutline(t *testing.T) {
	calls := &ellipseCalls{}
	config := &gvm.ServiceConfig{EllipseDraw: calls, EllipseOutline: calls}
	vm := runToHalt(t, append(append(push(1, 2, 3, 4), 0x64), append(push(5, 6, 7, 8), 0x65)...), gvm.AddressSpace{}, config)
	if !reflect.DeepEqual(calls.outline, [][4]int16{{1, 2, 3, 4}}) || !reflect.DeepEqual(calls.fill, [][4]int16{{5, 6, 7, 8}}) || len(vm.Stack()) != 0 {
		t.Fatalf("outline=%v fill=%v stack=%v", calls.outline, calls.fill, vm.Stack())
	}
}

type textCalls struct{ styles []gvm.TextDrawStyle }

func (c *textCalls) DrawGVMText(_ []byte, _, _ int16, style gvm.TextDrawStyle) error {
	c.styles = append(c.styles, style)
	return nil
}

func TestTextOpcode6dIsTheBackgroundFormOf6c(t *testing.T) {
	calls := &textCalls{}
	config := &gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte("A\x00")}}, TextDraw: calls}
	// 66 sets mode 1, primary 3, secondary 4, alignment 0.
	program := append(push(1, 3, 4, 0), 0x66)
	program = append(append(program, push(0, 0, 0)...), 0x6c)
	program = append(append(program, push(0, 0, 0)...), 0x6d)
	runToHalt(t, program, gvm.AddressSpace{}, config)
	want := []gvm.TextDrawStyle{
		{Mode: 2, Primary: 3},
		{Mode: 2, Primary: 3, Secondary: 4, Background: true},
	}
	if !reflect.DeepEqual(calls.styles, want) {
		t.Fatalf("styles = %+v, want %+v", calls.styles, want)
	}
}
