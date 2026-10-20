package application

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32VirusSoundResourceCorpus(t *testing.T) {
	machine := readyVirusProductMachine(t)
	product, ok := machine.(*gnex32Machine)
	if !ok {
		t.Fatalf("Virus machine type = %T", machine)
	}

	count := 0
	for index, media := range product.image.Media {
		if media.Flags != 0x200 {
			continue
		}
		count++
		t.Run(soundCorpusName(index), func(t *testing.T) {
			image := product.image
			image.Code = gnex32PlaySoundProgram(index)
			image.Entry = uint32(image.Header.BodyOffset)
			image.Callbacks = gnex.GNEX32Callbacks{}
			runtime, err := gnex32.NewRuntime(image, product.profile)
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			if err := runtime.RunMain(3); err != nil {
				t.Fatalf("decode/play media %d: %v", index, err)
			}

			var samples int
			var peak int16
			for frame := 0; frame < 600 && peak == 0; frame++ {
				if err := runtime.StepFrame(1); err != nil {
					t.Fatalf("media %d frame %d: %v", index, frame, err)
				}
				audio, _, _ := runtime.DrainTimedAudio()
				if len(audio.PCM16) != 0 && (audio.SampleRate != 44_100 || audio.Channels < 1 || audio.Channels > 2) {
					t.Fatalf("media %d format = %d Hz/%d channels", index, audio.SampleRate, audio.Channels)
				}
				samples += len(audio.PCM16)
				for _, sample := range audio.PCM16 {
					absolute := sample
					if absolute < 0 {
						absolute = -absolute
					}
					if absolute > peak {
						peak = absolute
					}
				}
			}
			if samples == 0 || peak == 0 {
				t.Fatalf("media %d decoded to no audible PCM in 10 guest seconds: samples=%d peak=%d",
					index, samples, peak)
			}
		})
	}
	if count == 0 {
		t.Fatal("Virus image contains no flag-0x200 sound resources")
	}
}

func gnex32PlaySoundProgram(index int) []byte {
	code := make([]byte, 10)
	binary.LittleEndian.PutUint16(code[0:], 0x06)
	binary.LittleEndian.PutUint16(code[2:], uint16(index))
	binary.LittleEndian.PutUint16(code[4:], 0x92)
	binary.LittleEndian.PutUint16(code[6:], 0x82)
	binary.LittleEndian.PutUint16(code[8:], 0x86)
	return code
}

func soundCorpusName(index int) string {
	return fmt.Sprintf("media-%04d", index)
}
