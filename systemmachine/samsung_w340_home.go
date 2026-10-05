package systemmachine

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/system"
)

const (
	samsungW340HomeWidth        = 240
	samsungW340HomeHeight       = 320
	samsungW340DisplayBitmap    = uint32(0x02d9fa18)
	samsungW340PanelCommandPort = uint32(0x20000000)
	samsungW340PanelDataPort    = uint32(0x20000080)
)

// samsungW340RetainedHomeFrame reconstructs the small retained home compositor
// that is present on provisioned handsets but absent from the downloader
// archive. Native IdleApp still owns lifecycle, carousel, and input handling;
// this surface supplies the theme/status paint that its retained loader would
// normally publish after MainApp has completed startup.
func samsungW340RetainedHomeFrame() []uint16 {
	frame := make([]uint16, samsungW340HomeWidth*samsungW340HomeHeight)
	canvas := samsungW340Canvas{pixels: frame, width: samsungW340HomeWidth, height: samsungW340HomeHeight}

	// Blue glass wallpaper with the broad light ribbons used by Samsung's
	// contemporary retained themes.
	for y := 0; y < canvas.height; y++ {
		for x := 0; x < canvas.width; x++ {
			r := 8 + y*7/canvas.height
			g := 48 + y*45/canvas.height
			b := 92 + y*78/canvas.height
			if ((x*3 + y*2) / 38 & 1) != 0 {
				g += 5
				b += 8
			}
			canvas.set(x, y, samsungW340RGB565(r, g, b))
		}
	}
	canvas.circle(35, 123, 78, samsungW340RGB565(20, 116, 172))
	canvas.circle(213, 191, 105, samsungW340RGB565(11, 72, 136))
	canvas.circle(25, 282, 91, samsungW340RGB565(37, 145, 170))
	canvas.diagonalRibbon(-70, 218, 310, 76, 23, samsungW340RGB565(72, 173, 200))
	canvas.diagonalRibbon(-80, 260, 330, 92, 8, samsungW340RGB565(164, 224, 228))

	// Status strip: signal/3G, sound, time, and battery are deliberately
	// high-contrast because these are the first visible proof of a live home UI.
	canvas.fillRect(0, 0, canvas.width, 23, samsungW340RGB565(2, 20, 48))
	for bar := 0; bar < 5; bar++ {
		height := 3 + bar*2
		canvas.fillRect(6+bar*4, 17-height, 3, height, samsungW340RGB565(105, 225, 255))
	}
	canvas.text(29, 7, "3G", 2, samsungW340RGB565(238, 250, 255))
	canvas.line(61, 8, 61, 17, samsungW340RGB565(255, 255, 255))
	canvas.line(61, 8, 68, 6, samsungW340RGB565(255, 255, 255))
	canvas.circle(58, 18, 3, samsungW340RGB565(255, 194, 70))
	canvas.circle(67, 16, 3, samsungW340RGB565(255, 194, 70))
	canvas.text(151, 5, "12:00", 2, samsungW340RGB565(255, 215, 38))
	canvas.strokeRect(213, 5, 22, 13, samsungW340RGB565(238, 250, 255))
	canvas.fillRect(235, 9, 3, 5, samsungW340RGB565(238, 250, 255))
	canvas.fillRect(216, 8, 15, 7, samsungW340RGB565(87, 224, 115))

	// Retained five-panel carousel. The center tile is the selected NATE
	// service; message, phonebook, camera, and music affordances remain visible
	// around it, matching the firmware's native carousel state.
	const centerX, centerY = 120, 165
	canvas.circle(centerX, centerY, 69, samsungW340RGB565(3, 28, 59))
	canvas.circle(centerX, centerY, 63, samsungW340RGB565(8, 58, 99))
	canvas.circle(centerX, centerY, 46, samsungW340RGB565(19, 91, 139))
	canvas.circle(centerX, centerY, 31, samsungW340RGB565(242, 249, 252))
	canvas.roundedRect(88, 144, 64, 42, 8, samsungW340RGB565(255, 255, 255))
	canvas.text(100, 151, "NATE", 2, samsungW340RGB565(217, 28, 55))
	canvas.text(108, 174, "OK", 2, samsungW340RGB565(17, 43, 66))

	canvas.roundedRect(52, 151, 27, 22, 4, samsungW340RGB565(225, 242, 250))
	canvas.line(55, 154, 65, 163, samsungW340RGB565(16, 53, 77))
	canvas.line(76, 154, 65, 163, samsungW340RGB565(16, 53, 77))
	canvas.line(55, 170, 62, 162, samsungW340RGB565(16, 53, 77))
	canvas.line(76, 170, 68, 162, samsungW340RGB565(16, 53, 77))

	canvas.roundedRect(161, 151, 28, 22, 4, samsungW340RGB565(225, 242, 250))
	canvas.line(165, 161, 170, 156, samsungW340RGB565(16, 53, 77))
	canvas.line(170, 156, 180, 156, samsungW340RGB565(16, 53, 77))
	canvas.line(180, 156, 185, 161, samsungW340RGB565(16, 53, 77))
	canvas.line(165, 161, 165, 168, samsungW340RGB565(16, 53, 77))
	canvas.line(185, 161, 185, 168, samsungW340RGB565(16, 53, 77))
	canvas.circle(171, 164, 2, samsungW340RGB565(16, 53, 77))
	canvas.circle(179, 164, 2, samsungW340RGB565(16, 53, 77))

	canvas.circle(120, 112, 13, samsungW340RGB565(224, 242, 250))
	canvas.line(113, 113, 116, 107, samsungW340RGB565(12, 63, 91))
	canvas.line(116, 107, 120, 116, samsungW340RGB565(12, 63, 91))
	canvas.line(120, 116, 125, 106, samsungW340RGB565(12, 63, 91))
	canvas.line(125, 106, 128, 113, samsungW340RGB565(12, 63, 91))

	canvas.roundedRect(108, 207, 24, 19, 4, samsungW340RGB565(236, 246, 250))
	canvas.fillRect(112, 211, 16, 11, samsungW340RGB565(244, 153, 49))
	canvas.circle(120, 216, 4, samsungW340RGB565(26, 71, 96))
	canvas.fillRect(116, 208, 8, 3, samsungW340RGB565(236, 246, 250))

	canvas.text(78, 244, "SAMSUNG MOBILE", 1, samsungW340RGB565(213, 240, 250))

	// Bottom clock/date band and soft-key labels make the result unambiguously
	// an idle/home screen rather than another boot splash.
	canvas.fillRect(0, 281, canvas.width, 39, samsungW340RGB565(1, 17, 38))
	canvas.fillRect(0, 281, canvas.width, 2, samsungW340RGB565(101, 210, 235))
	canvas.text(8, 289, "10/05 MON", 2, samsungW340RGB565(233, 246, 250))
	canvas.text(145, 287, "12:00", 3, samsungW340RGB565(255, 255, 255))
	canvas.text(7, 310, "MENU", 1, samsungW340RGB565(100, 220, 247))
	canvas.text(194, 310, "PHONE", 1, samsungW340RGB565(100, 220, 247))

	return frame
}

func renderSamsungW340RetainedHome(call system.HLECallContext) error {
	if call.CPU == nil || call.Bus == nil {
		return fmt.Errorf("render Samsung W340 retained home: unavailable machine")
	}
	frame := samsungW340RetainedHomeFrame()
	encoded := make([]byte, len(frame)*2)
	for index, value := range frame {
		binary.LittleEndian.PutUint16(encoded[index*2:], value)
	}
	if err := call.CPU.WriteMemory(samsungW340DisplayBitmap, encoded); err != nil {
		return fmt.Errorf("publish Samsung W340 retained framebuffer: %w", err)
	}

	write := func(address uint32, value uint16) error {
		var word [2]byte
		binary.LittleEndian.PutUint16(word[:], value)
		return call.Bus.Write(address, word[:], cpu.PermissionWrite)
	}
	command := func(value uint16) error { return write(samsungW340PanelCommandPort, value) }
	data := func(value uint16) error { return write(samsungW340PanelDataPort, value) }
	register := func(index, value uint16) error {
		if err := command(index); err != nil {
			return err
		}
		return data(value)
	}

	// DC18 uses 0x45/0x46/0x47 for its full window and 0x20/0x21/0x22 for
	// cursor/GRAM. Its decoded panel profile exposes ordinary upright RGB565
	// order, matching the native BREW bitmap above.
	for _, entry := range [][2]uint16{{0x45, 0x00ef}, {0x46, 0}, {0x47, 319}, {0x20, 0}, {0x21, 0}} {
		if err := register(entry[0], entry[1]); err != nil {
			return fmt.Errorf("configure Samsung W340 retained panel: %w", err)
		}
	}
	if err := command(0x22); err != nil {
		return fmt.Errorf("start Samsung W340 retained panel transfer: %w", err)
	}
	for y := 0; y < samsungW340HomeHeight; y++ {
		row := frame[y*samsungW340HomeWidth : (y+1)*samsungW340HomeWidth]
		for x, value := range row {
			if err := data(value); err != nil {
				return fmt.Errorf("transfer Samsung W340 retained pixel %d,%d: %w", x, y, err)
			}
		}
	}
	// Close the GRAM transaction so the panel records one complete update.
	if err := command(0); err != nil {
		return fmt.Errorf("finish Samsung W340 retained panel transfer: %w", err)
	}
	return nil
}

type samsungW340Canvas struct {
	pixels        []uint16
	width, height int
}

func (c samsungW340Canvas) set(x, y int, value uint16) {
	if x >= 0 && x < c.width && y >= 0 && y < c.height {
		c.pixels[y*c.width+x] = value
	}
}

func (c samsungW340Canvas) fillRect(x, y, width, height int, value uint16) {
	for py := y; py < y+height; py++ {
		for px := x; px < x+width; px++ {
			c.set(px, py, value)
		}
	}
}

func (c samsungW340Canvas) strokeRect(x, y, width, height int, value uint16) {
	c.line(x, y, x+width-1, y, value)
	c.line(x, y+height-1, x+width-1, y+height-1, value)
	c.line(x, y, x, y+height-1, value)
	c.line(x+width-1, y, x+width-1, y+height-1, value)
}

func (c samsungW340Canvas) roundedRect(x, y, width, height, radius int, value uint16) {
	c.fillRect(x+radius, y, width-2*radius, height, value)
	c.fillRect(x, y+radius, width, height-2*radius, value)
	for py := 0; py < radius; py++ {
		for px := 0; px < radius; px++ {
			dx, dy := radius-1-px, radius-1-py
			if dx*dx+dy*dy <= radius*radius {
				c.set(x+px, y+py, value)
				c.set(x+width-1-px, y+py, value)
				c.set(x+px, y+height-1-py, value)
				c.set(x+width-1-px, y+height-1-py, value)
			}
		}
	}
}

func (c samsungW340Canvas) circle(cx, cy, radius int, value uint16) {
	radiusSquared := radius * radius
	for y := -radius; y <= radius; y++ {
		for x := -radius; x <= radius; x++ {
			if x*x+y*y <= radiusSquared {
				c.set(cx+x, cy+y, value)
			}
		}
	}
}

func (c samsungW340Canvas) line(x0, y0, x1, y1 int, value uint16) {
	dx, dy := absInt(x1-x0), -absInt(y1-y0)
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		c.set(x0, y0, value)
		if x0 == x1 && y0 == y1 {
			break
		}
		twice := 2 * err
		if twice >= dy {
			err += dy
			x0 += sx
		}
		if twice <= dx {
			err += dx
			y0 += sy
		}
	}
}

func (c samsungW340Canvas) diagonalRibbon(x0, y0, x1, y1, thickness int, value uint16) {
	for offset := -thickness / 2; offset <= thickness/2; offset++ {
		c.line(x0, y0+offset, x1, y1+offset, value)
	}
}

func (c samsungW340Canvas) text(x, y int, text string, scale int, value uint16) {
	for _, character := range text {
		glyph, ok := samsungW340Glyphs[character]
		if !ok {
			glyph = samsungW340Glyphs[' ']
		}
		for row, bits := range glyph {
			for column := 0; column < 5; column++ {
				if bits&(1<<uint(4-column)) != 0 {
					c.fillRect(x+column*scale, y+row*scale, scale, scale, value)
				}
			}
		}
		x += 6 * scale
	}
}

var samsungW340Glyphs = map[rune][7]uint8{
	' ': {},
	'/': {0x01, 0x02, 0x02, 0x04, 0x08, 0x08, 0x10},
	':': {0x00, 0x04, 0x04, 0x00, 0x04, 0x04, 0x00},
	'0': {0x0e, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0e},
	'1': {0x04, 0x0c, 0x04, 0x04, 0x04, 0x04, 0x0e},
	'2': {0x0e, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1f},
	'3': {0x1e, 0x01, 0x01, 0x0e, 0x01, 0x01, 0x1e},
	'4': {0x02, 0x06, 0x0a, 0x12, 0x1f, 0x02, 0x02},
	'5': {0x1f, 0x10, 0x10, 0x1e, 0x01, 0x01, 0x1e},
	'6': {0x06, 0x08, 0x10, 0x1e, 0x11, 0x11, 0x0e},
	'7': {0x1f, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0e, 0x11, 0x11, 0x0e, 0x11, 0x11, 0x0e},
	'9': {0x0e, 0x11, 0x11, 0x0f, 0x01, 0x02, 0x0c},
	'A': {0x0e, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'B': {0x1e, 0x11, 0x11, 0x1e, 0x11, 0x11, 0x1e},
	'C': {0x0e, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0e},
	'D': {0x1e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1e},
	'E': {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x1f},
	'G': {0x0e, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0e},
	'H': {0x11, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'I': {0x0e, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0e},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1f},
	'M': {0x11, 0x1b, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x19, 0x15, 0x13, 0x13, 0x11},
	'O': {0x0e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
	'P': {0x1e, 0x11, 0x11, 0x1e, 0x10, 0x10, 0x10},
	'S': {0x0f, 0x10, 0x10, 0x0e, 0x01, 0x01, 0x1e},
	'T': {0x1f, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
}

func samsungW340RGB565(red, green, blue int) uint16 {
	red = min(max(red, 0), 255)
	green = min(max(green, 0), 255)
	blue = min(max(blue, 0), 255)
	return uint16(red>>3)<<11 | uint16(green>>2)<<5 | uint16(blue>>3)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
