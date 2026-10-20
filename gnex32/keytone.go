package gnex32

import (
	"encoding/binary"
	"math"
	"sync"
)

const (
	gnex32KeyToneSampleRate = 8_000
	gnex32KeyToneMillis     = 60
	gnex32KeyToneFrames     = gnex32KeyToneSampleRate * gnex32KeyToneMillis / 1_000
)

var gnex32KeyToneCache struct {
	sync.Once
	waves map[uint16][]byte
}

func gnex32KeyToneWave(code uint16) []byte {
	gnex32KeyToneCache.Do(func() {
		gnex32KeyToneCache.waves = make(map[uint16][]byte, 13)
		for key := uint16(1); key <= 12; key++ {
			low, high := gnex32KeyToneFrequencies(key)
			gnex32KeyToneCache.waves[key] = gnex32SineWave(low, high)
		}
		low, high := gnex32KeyToneFrequencies(0)
		gnex32KeyToneCache.waves[0] = gnex32SineWave(low, high)
	})
	if wave := gnex32KeyToneCache.waves[code]; wave != nil {
		return wave
	}
	return gnex32KeyToneCache.waves[0]
}

// gnex32KeyToneFrequencies maps the SKT keypad codes to standard DTMF. Other
// handset controls use one deterministic compatibility beep because the SDK
// leaves their device-default tone unspecified.
func gnex32KeyToneFrequencies(code uint16) (low, high float64) {
	rows := [...]float64{697, 770, 852, 941}
	columns := [...]float64{1209, 1336, 1477}
	var row, column int
	switch code {
	case 1, 2, 3:
		row, column = 0, int(code-1)
	case 4, 5, 6:
		row, column = 1, int(code-4)
	case 7, 8, 9:
		row, column = 2, int(code-7)
	case 10: // 0
		row, column = 3, 1
	case 11: // *
		row, column = 3, 0
	case 12: // #
		row, column = 3, 2
	default:
		return 1_000, 0
	}
	return rows[row], columns[column]
}

func gnex32SineWave(low, high float64) []byte {
	data := make([]byte, 44+gnex32KeyToneFrames*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], gnex32KeyToneSampleRate)
	binary.LittleEndian.PutUint32(data[28:32], gnex32KeyToneSampleRate*2)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], gnex32KeyToneFrames*2)

	const fadeFrames = gnex32KeyToneSampleRate / 200 // 5 ms
	for frame := 0; frame < gnex32KeyToneFrames; frame++ {
		seconds := float64(frame) / gnex32KeyToneSampleRate
		value := math.Sin(2*math.Pi*low*seconds) * 6_000
		if high != 0 {
			value = (value + math.Sin(2*math.Pi*high*seconds)*6_000) / 2
		}
		envelope := 1.0
		if frame < fadeFrames {
			envelope = float64(frame) / fadeFrames
		} else if remaining := gnex32KeyToneFrames - 1 - frame; remaining < fadeFrames {
			envelope = float64(remaining) / fadeFrames
		}
		binary.LittleEndian.PutUint16(data[44+frame*2:], uint16(int16(math.Round(value*envelope))))
	}
	return data
}
