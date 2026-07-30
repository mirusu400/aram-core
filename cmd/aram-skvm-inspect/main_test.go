package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"testing"
)

func TestInspectReport(t *testing.T) {
	jar := testZIP(t, map[string][]byte{
		"Game.class": testClass(t),
		"data.bin":   {1, 2, 3},
	})
	wrapped := make([]byte, 32+len(jar))
	binary.LittleEndian.PutUint32(wrapped, 32)
	copy(wrapped[32:], jar)
	archive := testZIP(t, map[string][]byte{
		"7.msd": []byte(
			"MIDlet-Name: Test\n" +
				"MicroEdition-Profile: SKTP-1.0\n" +
				"MIDlet-1: Test,,Game\n",
		),
		"7.jar": wrapped,
		"7.mod": {1},
		"7.wmr": {2},
	})
	result, err := inspect("test.zip", archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Classes) != 1 || result.Classes[0].Name != "Game" ||
		result.JARHeaderSize != 32 || len(result.Resources) != 1 {
		t.Fatalf("report = %#v", result)
	}
}

func testZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, payload := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testClass(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	u2 := func(value uint16) {
		if err := binary.Write(&data, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	u4 := func(value uint32) {
		if err := binary.Write(&data, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	utf := func(value string) {
		data.WriteByte(1)
		u2(uint16(len(value)))
		data.WriteString(value)
	}
	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(7)
	utf("Game")
	data.WriteByte(7)
	u2(1)
	utf("java/lang/Object")
	data.WriteByte(7)
	u2(3)
	utf("SourceFile")
	utf("Game.java")
	u2(1)
	u2(2)
	u2(4)
	u2(0)
	u2(0)
	u2(0)
	u2(1)
	u2(5)
	u4(2)
	u2(6)
	return data.Bytes()
}
