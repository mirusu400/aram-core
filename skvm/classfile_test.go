package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
)

func TestParseClassMethodsAndReferences(t *testing.T) {
	data := syntheticClass(t)
	class, err := ParseClass("Game.class", data)
	if err != nil {
		t.Fatal(err)
	}
	if class.Name != "Game" || class.SuperName != "java/lang/Object" {
		t.Fatalf("class identity = %q extends %q", class.Name, class.SuperName)
	}
	method, ok := class.Method("answer", "()I")
	if !ok || method.MaxStack != 1 || method.MaxLocals != 0 ||
		!bytes.Equal(method.Code, []byte{0x10, 42, 0xac}) {
		t.Fatalf("answer method = %#v, %v", method, ok)
	}
	references, err := class.References()
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 || references[0] != (Reference{
		Kind:       ReferenceMethod,
		Class:      "java/lang/Object",
		Name:       "<init>",
		Descriptor: "()V",
	}) {
		t.Fatalf("references = %#v", references)
	}
}

func TestParseClassRejectsTruncation(t *testing.T) {
	data := syntheticClass(t)
	for _, size := range []int{0, 7, len(data) - 1} {
		if _, err := ParseClass("bad.class", data[:size]); err == nil {
			t.Fatalf("ParseClass accepted %d bytes", size)
		}
	}
}

func TestDecodeModifiedUTF8(t *testing.T) {
	got, err := decodeModifiedUTF8([]byte{'A', 0xc0, 0x80, 0xed, 0x95, 0x9c})
	if err != nil {
		t.Fatal(err)
	}
	if got != "A\x00한" {
		t.Fatalf("decoded string = %q", got)
	}
}

func TestVMExecutesStaticBytecode(t *testing.T) {
	machine, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	value, returned, err := machine.InvokeStatic(
		context.Background(),
		"Game",
		"answer",
		"()I",
	)
	if err != nil {
		t.Fatal(err)
	}
	integer, err := value.Int()
	if err != nil {
		t.Fatal(err)
	}
	if !returned || integer != 42 {
		t.Fatalf("answer = %v, %v; want 42, true", value, returned)
	}
	if machine.Instructions != 2 {
		t.Fatalf("instructions = %d, want 2", machine.Instructions)
	}
}

func syntheticClass(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	writeU4 := func(value uint32) {
		t.Helper()
		if err := binary.Write(&data, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	writeU2 := func(value uint16) {
		t.Helper()
		if err := binary.Write(&data, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	writeUTF := func(value string) {
		t.Helper()
		data.WriteByte(constantUTF8)
		writeU2(uint16(len(value)))
		data.WriteString(value)
	}

	writeU4(0xcafebabe)
	writeU2(3)
	writeU2(45)
	writeU2(13)
	writeUTF("Game")              // 1
	data.WriteByte(constantClass) // 2
	writeU2(1)
	writeUTF("java/lang/Object")  // 3
	data.WriteByte(constantClass) // 4
	writeU2(3)
	writeUTF("answer") // 5
	writeUTF("()I")    // 6
	writeUTF("Code")   // 7
	writeUTF("<init>") // 8
	writeUTF("()V")    // 9
	data.WriteByte(constantNameAndType)
	writeU2(8)
	writeU2(9) // 10
	data.WriteByte(constantMethodref)
	writeU2(4)
	writeU2(10)            // 11
	writeUTF("SourceFile") // 12

	writeU2(AccessPublic)
	writeU2(2)
	writeU2(4)
	writeU2(0) // interfaces
	writeU2(0) // fields
	writeU2(1) // methods
	writeU2(AccessPublic | AccessStatic)
	writeU2(5)
	writeU2(6)
	writeU2(1) // attributes
	writeU2(7)
	code := []byte{
		0, 1, // max stack
		0, 0, // max locals
		0, 0, 0, 3,
		0x10, 42, 0xac,
		0, 0, // handlers
		0, 0, // attributes
	}
	writeU4(uint32(len(code)))
	data.Write(code)
	writeU2(0) // class attributes
	return data.Bytes()
}
