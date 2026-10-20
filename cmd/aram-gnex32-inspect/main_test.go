package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestInspectImageReportsRuntimeDifference(t *testing.T) {
	code := make([]byte, 10)
	binary.LittleEndian.PutUint16(code[0:], 0x92)
	binary.LittleEndian.PutUint16(code[2:], 0x5f)
	binary.LittleEndian.PutUint16(code[4:], 0x92)
	binary.LittleEndian.PutUint16(code[6:], 0xbeef)
	binary.LittleEndian.PutUint16(code[8:], 0xff)
	result, err := inspectImage("sample.sgs", gnex.GNEX32Image{
		Header: gnex.Header{FormatVersion: 4, Title: "sample", BodyOffset: 160},
		Entry:  160, Code: code,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "sample.sgs" || result.ReachableInstructions != 3 ||
		len(result.ServiceCalls) != 2 || len(result.UnsupportedServices) != 1 ||
		result.UnsupportedServices[0].ID != 0xbeef || len(result.RuntimeServices) == 0 {
		t.Fatalf("report = %+v", result)
	}
}

func TestRunRequiresOneInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if status := run(nil, &stdout, &stderr); status != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("run status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}
