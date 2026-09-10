package raptor

import (
	"encoding/binary"
	"testing"
	"time"
)

// The embedded KTF Java host does not run through the KTF machine loop. Its
// clips therefore have to use the public Raptor mixer, which is advanced and
// published once per machine frame (issue #256).
func TestRaptorJavaAudioUsesPublicMediaMixer(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	if java.Host.Services.Media != public.Services.Media {
		t.Fatal("Raptor Java host has a mixer the public frame loop cannot advance")
	}

	clip, err := java.Host.Services.Media.CreateClip(
		java.Host.ServiceOwner,
		"audio/wav",
		0,
	)
	check(t, err)
	if _, err := java.Host.Services.Media.Append(
		java.Host.ServiceOwner,
		clip,
		raptorJavaTestWave(),
	); err != nil {
		t.Fatal(err)
	}
	check(t, java.Host.Services.Media.Play(java.Host.ServiceOwner, clip, 1))
	check(t, public.Services.Advance(public.ServiceOwner, 20*time.Millisecond))

	output := public.Services.Media.Drain()
	if len(output.PCM16) == 0 {
		t.Fatal("public frame advance produced no Raptor Java audio")
	}
	for _, sample := range output.PCM16 {
		if sample != 0 {
			return
		}
	}
	t.Fatal("public frame advance produced only silence for a non-silent Java clip")
}

func raptorJavaTestWave() []byte {
	const sampleCount = 800
	data := make([]byte, 44+sampleCount*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8_000)
	binary.LittleEndian.PutUint32(data[28:32], 16_000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], sampleCount*2)
	for index := 0; index < sampleCount; index++ {
		binary.LittleEndian.PutUint16(data[44+index*2:], 400)
	}
	return data
}
