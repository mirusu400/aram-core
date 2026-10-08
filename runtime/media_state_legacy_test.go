package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"testing"
	"time"
)

func TestServicesLegacyMediaStateLoads(t *testing.T) {
	for _, voice := range []bool{false, true} {
		name := "without_voice"
		if voice {
			name = "with_voice"
		}
		t.Run(name, func(t *testing.T) {
			config := DefaultConfig()
			config.Limits.Media.OutputSampleRate = 8_000
			config.Limits.Media.OutputChannels = 1
			services, err := NewServices(config)
			check(t, err)
			owner, err := services.Coordinator.Register("legacy-media", 0)
			check(t, err)
			clip, err := services.Media.CreateClip(owner, "audio/wav", 0)
			check(t, err)
			wave := pcmWave(8_000, 1, []int16{10, 20, 30, 40})
			_, err = services.Media.Append(owner, clip, wave)
			check(t, err)
			old := mediaStateV3{
				Limits: config.Limits.Media, GlobalVolume: 100,
				Clips: services.Media.Snapshot().Clips,
			}
			const step = 125 * time.Microsecond
			if voice {
				old.BGMVoice = &bgmVoiceStateV3{
					MediaType: "audio/wav", Source: wave, PositionNS: int64(step), Volume: 100,
				}
			}
			oldBytes, err := encodeStateValue(old)
			check(t, err)
			encoded, err := services.MarshalBinary()
			check(t, err)
			legacy := replaceServiceComponentForTest(t, encoded, "media", oldBytes, 3)
			restored, err := NewServices(config)
			check(t, err)
			check(t, restored.UnmarshalBinary(legacy))
			if restored.Media.MusicVoiceActive() != voice {
				t.Fatalf("legacy music voice active = %v, want %v", restored.Media.MusicVoiceActive(), voice)
			}
			check(t, restored.Advance(owner, step))
			var want []int16
			if voice {
				want = []int16{20}
			}
			if got := restored.Media.Drain().PCM16; !reflect.DeepEqual(got, want) {
				t.Fatalf("legacy BGM = %v, want %v", got, want)
			}
			// A migrated state must also be writable in the current format.
			_, err = restored.MarshalBinary()
			check(t, err)
		})
	}
}

func replaceServiceComponentForTest(t *testing.T, encoded []byte, component string, payload []byte, version uint32) []byte {
	t.Helper()
	d := binaryStateDecoder{reader: bytes.NewReader(encoded[:len(encoded)-sha256.Size])}
	headerSize := len(servicesStateMagic) + 12
	d.bytes(headerSize)
	var output bytes.Buffer
	output.Write(encoded[:headerSize])
	for range requiredServiceComponents {
		start := d.offset
		name := string(d.bytes(int(d.u16())))
		d.u32()
		d.bytes(int(d.u64()))
		d.bytes(sha256.Size)
		if name != component {
			output.Write(encoded[start:d.offset])
			continue
		}
		check(t, binary.Write(&output, binary.LittleEndian, uint16(len(name))))
		output.WriteString(name)
		check(t, binary.Write(&output, binary.LittleEndian, version))
		check(t, binary.Write(&output, binary.LittleEndian, uint64(len(payload))))
		output.Write(payload)
		digest := sha256.Sum256(payload)
		output.Write(digest[:])
	}
	check(t, d.err)
	digest := sha256.Sum256(output.Bytes())
	output.Write(digest[:])
	return output.Bytes()
}
