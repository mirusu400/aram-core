package minigame

import (
	"encoding/binary"
	"image"
	"time"
)

const (
	LegacyProfileID = "wipi-1.0/skt/samsung/sch-w210/bomb-link"
	legacyServiceID = uint32(0xffff)
)

// AdvanceClock uses the shared machine clock for the legacy periodic callback.
func (r *Runtime) AdvanceClock(now time.Duration) {
	milliseconds := uint32(now.Milliseconds())
	r.clockNS = now
	r.clockMS = milliseconds
	if r.legacy {
		r.tickMS = milliseconds
		r.Stats.TickMS = milliseconds
	}
}

type eventTimer struct {
	enabled  bool
	deadline uint32
}

func signedPair(value uint32) (int, int) {
	return int(int16(value)), int(int16(value >> 16))
}

func (r *Runtime) dispatchLegacy(slot uint32) (uint32, error) {
	args, err := r.args(5)
	if err != nil {
		return 0, err
	}
	var value uint32
	switch slot {
	case 0x18:
		if args[0]&0xffff == 0 {
			r.stopMusic()
		} else {
			err = r.playMusic(args[0]&0xffff, args[1])
		}
	case 0x1b0:
		for _, handle := range r.audioHandles {
			if r.public.OEMClipPlaying(handle) {
				value = 1
				break
			}
		}
	case 0x3c:
		r.rngState = args[0] & 0xffff
	case 0x40:
		lower, upper := args[0]&0xffff, args[0]>>16
		if lower > upper {
			lower, upper = upper, lower
		}
		r.rngState = (1103515245*r.rngState + 12345) & 0x7fffffff
		value = lower + r.rngState%(upper-lower+1)
	case 0x5c:
		var info [32]byte
		if err := r.cpu.ReadMemory(args[0], info[:]); err != nil {
			return 0, err
		}
		text, err := r.readCString(binary.LittleEndian.Uint32(info[12:]), 1<<20)
		if err != nil {
			return 0, err
		}
		x, y := signedPair(binary.LittleEndian.Uint32(info[8:]))
		color := rgb332(info[24])
		clip := image.Rect(r.clipLeft, r.clipTop, r.clipRight+1, r.clipBottom+1)
		width, err := r.public.OEMText(0, r.width, r.height, 0, 0, color, text, 12, clip)
		if err != nil {
			return 0, err
		}
		if binary.LittleEndian.Uint16(info[28:]) == 2 {
			x += (int(binary.LittleEndian.Uint16(info[4:])) - width) / 2
		}
		_, err = r.public.OEMText(r.screenPixels, r.width, r.height, x, y, color, text, 12, clip)
		if err != nil {
			return 0, err
		}
	case 0x24:
		r.timerPeriod = args[1] & 0xffff
		r.nextTick = r.tickMS + r.timerPeriod
	case 0x7c:
		r.drawColor = rgb332(byte(args[0]))
	case 0x8c:
		x, y := signedPair(args[0])
		endX, endY := signedPair(args[1])
		dx, dy := abs(endX-x), -abs(endY-y)
		stepX, stepY := 1, 1
		if x > endX {
			stepX = -1
		}
		if y > endY {
			stepY = -1
		}
		distance := dx + dy
		for {
			if x >= r.clipLeft && x <= r.clipRight && y >= r.clipTop && y <= r.clipBottom {
				if err := r.putPixel(x, y, r.drawColor); err != nil {
					return 0, err
				}
			}
			if x == endX && y == endY {
				break
			}
			twice := 2 * distance
			if twice >= dy {
				distance += dy
				x += stepX
			}
			if twice <= dx {
				distance += dx
				y += stepY
			}
		}
	case 0x94:
		x, y := signedPair(args[0])
		err = r.putPixel(x, y, rgb332(byte(args[1])))
	case 0x98:
		x, y := signedPair(args[0])
		width, height := signedPair(args[1])
		err = r.drawRectangle([]uint32{0, uint32(x), uint32(y), uint32(width), uint32(height), args[2] & 0xffff})
	case 0xa0:
		r.presentCount++
	case 0x1cc:
		// The guest binds the screen obtained from 0x1e0.
	case 0x1d0:
		var info [16]byte
		binary.LittleEndian.PutUint16(info[:], uint16(r.width))
		binary.LittleEndian.PutUint16(info[2:], uint16(r.height))
		binary.LittleEndian.PutUint32(info[4:], r.screenPixels)
		return 0, r.cpu.WriteMemory(args[4], info[:])
	case 0x1e0:
		return 0, r.writeU32(args[0], r.screenPixels)
	case 0x1ec:
		width, height := uint32(byte(args[1])), uint32(byte(args[1]>>8))
		value, err = r.decodeImage([]uint32{0, args[0], width, height, args[2], args[3] & 0xff})
	case 0x1f0:
		x, y := signedPair(args[0])
		if decoded, ok := r.images[args[1]]; ok {
			_, err = r.blitIndices(x, y, decoded)
		}
	default:
		// Opaque device calls remain success-returning during ABI recovery.
	}
	if err != nil {
		return 0, err
	}
	if args[4] != 0 {
		err = r.writeU32(args[4], value)
	}
	return 0, err
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
