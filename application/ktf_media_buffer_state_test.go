package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"testing"
)

func TestKTF262PublicLoadRejectsAliasBeforeMutation(t *testing.T) {
	jar := testZIP(t, map[string][]byte{"client.bin4096": syntheticKTFClient()})
	archive := testZIP(t, map[string][]byte{"01020304.jar": jar, "__adf__": []byte("PID:PD000001\nAID:01020304\nMClass:GameMain\n")})
	created, e := NewFactory().Create(context.Background(), machinecore.Source{Name: "buffer.zip", ReaderAt: bytes.NewReader(archive), Size: int64(len(archive))})
	check(t, e)
	m := created.(*Machine)
	t.Cleanup(func() { _ = m.Close() })
	r := m.ktf
	r.JvmContext, e = r.AllocateWords(131)
	check(t, e)
	c, e := r.NewHostJavaObject("org/kwis/msp/media/Clip")
	check(t, e)
	typ, e := r.NewJavaString("audio/wav")
	check(t, e)
	a, e := r.NewJavaArray("[B", 4, 1)
	check(t, e)
	fields, e := r.ReadU32(a)
	check(t, e)
	check(t, r.CPU.WriteMemory(fields+8, []byte{1, 2, 3, 4}))
	check(t, r.QueueJavaVirtual(c, "<init>", "(Ljava/lang/String;[B)V", typ, a))
	check(t, r.ActivatePendingJavaCalls())
	for i := 0; i < 10 && r.HasRunnableTask(); i++ {
		result := r.RunTaskSlice(context.Background(), 10000)
		check(t, result.Err)
	}
	if r.HasRunnableTask() {
		t.Fatal("constructor task did not finish")
	}
	var good bytes.Buffer
	check(t, m.SaveState(&good))
	payload := good.Bytes()[:good.Len()-stateChecksumSize]
	if got := binary.LittleEndian.Uint32(payload[len(payload)-16:]); got != 1 {
		t.Fatalf("retained alias count=%d", got)
	}
	check(t, m.LoadState(bytes.NewReader(good.Bytes())))
	for _, kind := range []string{"front", "array-overflow", "missing-trailer", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			raw := append([]byte(nil), payload...)
			switch kind {
			case "front":
				binary.LittleEndian.PutUint32(raw[len(raw)-4:], 4)
			case "array-overflow":
				binary.LittleEndian.PutUint32(raw[len(raw)-8:], 0xfffffffc)
			case "missing-trailer":
				raw = raw[:len(raw)-4]
			case "duplicate":
				entry := append([]byte(nil), raw[len(raw)-12:]...)
				binary.LittleEndian.PutUint32(raw[len(raw)-16:], 2)
				raw = append(raw, entry...)
			}
			sum := sha256.Sum256(raw)
			raw = append(raw, sum[:]...)
			check(t, m.cpu.WriteRegister(cpu.RegisterR0, 0x12345678))
			r.TickMS = 9876
			check(t, m.cpu.WriteMemory(fields+8, []byte{9, 8, 7, 6}))
			services := r.Services
			before, e := m.cpu.SaveContext()
			check(t, e)
			if e := m.LoadState(bytes.NewReader(raw)); e == nil {
				t.Fatal("malformed alias load accepted")
			}
			after, e := m.cpu.SaveContext()
			check(t, e)
			var data [4]byte
			check(t, m.cpu.ReadMemory(fields+8, data[:]))
			if !bytes.Equal(before, after) || r.TickMS != 9876 || r.Services != services || data != ([4]byte{9, 8, 7, 6}) {
				t.Fatal("failed public LoadState mutated CPU/runtime")
			}
		})
	}
	check(t, m.Reset(context.Background()))
	if m.ktf == r || len(m.ktf.Services.Media.Snapshot().Clips) != 0 {
		t.Fatal("reset retained old media registry")
	}
	var reset bytes.Buffer
	check(t, m.SaveState(&reset))
	resetPayload := reset.Bytes()[:reset.Len()-stateChecksumSize]
	if n := binary.LittleEndian.Uint32(resetPayload[len(resetPayload)-4:]); n != 0 {
		t.Fatalf("reset retained alias records=%d", n)
	}
}
