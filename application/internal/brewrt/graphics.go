package brewrt

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/mirusu400/aram-core/cpu"
)

type graphicsState struct {
	background uint16
	stroke     uint16
	fill       uint16
	fillMode   bool
	pointSize  uint32
	originX    int32
	originY    int32
	target     uint32
}

func rgb565(red, green, blue uint32) uint16 {
	return uint16((red&0xf8)<<8 | (green&0xfc)<<3 | blue>>3)
}

func rgbval(value uint16) uint32 {
	red := uint32(value>>11) * 255 / 31
	green := uint32(value>>5&0x3f) * 255 / 63
	blue := uint32(value&0x1f) * 255 / 31
	return red | green<<8 | blue<<16
}

func (r *Runtime) handleGraphicsMethod(slot uint32) (bool, error) {
	argument := func(register uint32) (uint32, error) {
		value, err := r.cpu.ReadRegister(register)
		if err != nil {
			return 0, fmt.Errorf("read BREW IGraphics argument: %w", err)
		}
		return value, nil
	}
	returnValue := func(value uint32) (bool, error) {
		return true, r.cpu.WriteRegister(cpu.RegisterR0, value)
	}
	switch slot {
	case 2, 4, 8: // SetBackground, SetColor, SetFillColor
		red, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		green, err := argument(cpu.RegisterR2)
		if err != nil {
			return true, err
		}
		blue, err := argument(cpu.RegisterR3)
		if err != nil {
			return true, err
		}
		color := rgb565(red, green, blue)
		var previous uint16
		switch slot {
		case 2:
			previous, r.graphics.background = r.graphics.background, color
		case 4:
			previous, r.graphics.stroke = r.graphics.stroke, color
		case 8:
			previous, r.graphics.fill = r.graphics.fill, color
		}
		return returnValue(rgbval(previous))
	case 3:
		return returnValue(rgbval(r.graphics.background))
	case 5:
		return returnValue(rgbval(r.graphics.stroke))
	case 6:
		value, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		previous := r.graphics.fillMode
		r.graphics.fillMode = value != 0
		return returnValue(boolWord(previous))
	case 7:
		return returnValue(boolWord(r.graphics.fillMode))
	case 9:
		return returnValue(rgbval(r.graphics.fill))
	case 10:
		value, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		previous := r.graphics.pointSize
		r.graphics.pointSize = value
		return returnValue(previous)
	case 11:
		return returnValue(r.graphics.pointSize)
	case 19:
		return returnValue(16)
	case 20:
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		x, y, err := r.readGraphicsPoint(pointer)
		if err != nil {
			return true, err
		}
		if err := r.writeGraphicsPixel(x+r.graphics.originX, y+r.graphics.originY, r.graphics.stroke); err != nil {
			return true, err
		}
		return returnValue(0)
	case 21:
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		line := make([]byte, 8)
		if err := r.cpu.ReadMemory(pointer, line); err != nil {
			return true, fmt.Errorf("read BREW graphics line: %w", err)
		}
		read := func(at int) int32 { return int32(int16(binary.LittleEndian.Uint16(line[at:]))) }
		if err := r.drawGraphicsLine(read(0)+r.graphics.originX, read(2)+r.graphics.originY, read(4)+r.graphics.originX, read(6)+r.graphics.originY, r.graphics.stroke); err != nil {
			return true, err
		}
		return returnValue(0)
	case 22, 30:
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		x, y, width, height, err := r.readGraphicsRect(pointer)
		if err != nil {
			return true, err
		}
		x += r.graphics.originX
		y += r.graphics.originY
		if slot == 30 || r.graphics.fillMode {
			color := r.graphics.fill
			if slot == 30 {
				color = r.graphics.background
			}
			if err := r.fillGraphicsRect(x, y, width, height, color); err != nil {
				return true, err
			}
		}
		if slot == 22 {
			if err := r.drawGraphicsRect(x, y, width, height, r.graphics.stroke); err != nil {
				return true, err
			}
		}
		return returnValue(0)
	case 23: // DrawCircle
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		centerX, centerY, radius, err := r.readGraphicsCircle(pointer)
		if err != nil {
			return true, err
		}
		if err := r.drawGraphicsCircle(
			centerX+r.graphics.originX,
			centerY+r.graphics.originY,
			radius,
		); err != nil {
			return true, err
		}
		return returnValue(0)
	case 27: // DrawTriangle
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		points, err := r.readGraphicsPoints(pointer, 3)
		if err != nil {
			return true, fmt.Errorf("read BREW graphics triangle: %w", err)
		}
		if err := r.drawGraphicsPolygon(points, true); err != nil {
			return true, err
		}
		return returnValue(0)
	case 28, 29: // DrawPolygon, DrawPolyline
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		header := make([]byte, 8)
		if err := r.cpu.ReadMemory(pointer, header); err != nil {
			return true, fmt.Errorf("read BREW graphics polygon: %w", err)
		}
		count := int32(int16(binary.LittleEndian.Uint16(header)))
		if count < 0 || count > 4096 {
			return true, fmt.Errorf("BREW graphics polygon has invalid point count %d", count)
		}
		points, err := r.readGraphicsPoints(binary.LittleEndian.Uint32(header[4:]), count)
		if err != nil {
			return true, fmt.Errorf("read BREW graphics polygon points: %w", err)
		}
		if slot == 28 {
			err = r.drawGraphicsPolygon(points, true)
		} else {
			err = r.drawGraphicsPolygon(points, false)
		}
		if err != nil {
			return true, err
		}
		return returnValue(0)
	case 16:
		if err := r.fillGraphicsRect(0, 0, int32(framebufferWidth), int32(framebufferHeight), r.graphics.background); err != nil {
			return true, err
		}
		return returnValue(0)
	case 31, 17, 12, 14, 34, 36, 40: // state accepted by the fixed software target
		return returnValue(0)
	case 32: // Update
		if err := r.commitFramebufferUpdate(); err != nil {
			return true, fmt.Errorf("commit BREW graphics framebuffer: %w", err)
		}
		return returnValue(0)
	case 33:
		x, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		y, err := argument(cpu.RegisterR2)
		if err != nil {
			return true, err
		}
		r.graphics.originX, r.graphics.originY = int32(int16(x)), int32(int16(y))
		return returnValue(0)
	case 38:
		target, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.graphics.target = target
		return returnValue(0)
	case 39:
		return returnValue(r.graphics.target)
	default:
		return false, nil
	}
}

func (r *Runtime) readGraphicsPoint(address uint32) (int32, int32, error) {
	data := make([]byte, 4)
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return 0, 0, fmt.Errorf("read BREW graphics point: %w", err)
	}
	return int32(int16(binary.LittleEndian.Uint16(data))), int32(int16(binary.LittleEndian.Uint16(data[2:]))), nil
}

func (r *Runtime) readGraphicsRect(address uint32) (int32, int32, int32, int32, error) {
	data := make([]byte, 8)
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("read BREW graphics rectangle: %w", err)
	}
	read := func(at int) int32 { return int32(int16(binary.LittleEndian.Uint16(data[at:]))) }
	return read(0), read(2), read(4), read(6), nil
}

func (r *Runtime) readGraphicsCircle(address uint32) (int32, int32, int32, error) {
	data := make([]byte, 6)
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return 0, 0, 0, fmt.Errorf("read BREW graphics circle: %w", err)
	}
	read := func(at int) int32 { return int32(int16(binary.LittleEndian.Uint16(data[at:]))) }
	return read(0), read(2), read(4), nil
}

type graphicsPoint struct {
	x int32
	y int32
}

func (r *Runtime) readGraphicsPoints(address uint32, count int32) ([]graphicsPoint, error) {
	if count == 0 {
		return nil, nil
	}
	data := make([]byte, int(count)*4)
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return nil, err
	}
	points := make([]graphicsPoint, count)
	for index := range points {
		offset := index * 4
		points[index] = graphicsPoint{
			x: int32(int16(binary.LittleEndian.Uint16(data[offset:]))),
			y: int32(int16(binary.LittleEndian.Uint16(data[offset+2:]))),
		}
	}
	return points, nil
}

func (r *Runtime) graphicsSurface() (uint32, uint32, uint32, uint32, error) {
	if r.graphics.target == 0 {
		return framebufferBase, framebufferWidth, framebufferHeight, framebufferWidth * 2, nil
	}
	header := make([]byte, 36)
	if err := r.cpu.ReadMemory(r.graphics.target, header); err != nil {
		return 0, 0, 0, 0, err
	}
	if header[28] != 16 || header[29] != idibColorScheme565 {
		return 0, 0, 0, 0, nil
	}
	return binary.LittleEndian.Uint32(header[8:]), uint32(binary.LittleEndian.Uint16(header[20:])), uint32(binary.LittleEndian.Uint16(header[22:])), uint32(binary.LittleEndian.Uint16(header[24:])), nil
}

func (r *Runtime) writeGraphicsPixel(x, y int32, color uint16) error {
	base, width, height, pitch, err := r.graphicsSurface()
	if err != nil || base == 0 || x < 0 || y < 0 || x >= int32(width) || y >= int32(height) {
		return err
	}
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], color)
	return r.cpu.WriteMemory(base+uint32(y)*pitch+uint32(x)*2, encoded[:])
}

func (r *Runtime) drawGraphicsLine(x0, y0, x1, y1 int32, color uint16) error {
	dx, dy := abs32(x1-x0), -abs32(y1-y0)
	sx, sy := int32(-1), int32(-1)
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	errValue := dx + dy
	for {
		if err := r.writeGraphicsPixel(x0, y0, color); err != nil {
			return err
		}
		if x0 == x1 && y0 == y1 {
			return nil
		}
		twice := 2 * errValue
		if twice >= dy {
			errValue += dy
			x0 += sx
		}
		if twice <= dx {
			errValue += dx
			y0 += sy
		}
	}
}

func (r *Runtime) fillGraphicsRect(x, y, width, height int32, color uint16) error {
	for row := int32(0); row < height; row++ {
		for column := int32(0); column < width; column++ {
			if err := r.writeGraphicsPixel(x+column, y+row, color); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) drawGraphicsRect(x, y, width, height int32, color uint16) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	if err := r.drawGraphicsLine(x, y, x+width-1, y, color); err != nil {
		return err
	}
	if err := r.drawGraphicsLine(x, y+height-1, x+width-1, y+height-1, color); err != nil {
		return err
	}
	if err := r.drawGraphicsLine(x, y, x, y+height-1, color); err != nil {
		return err
	}
	return r.drawGraphicsLine(x+width-1, y, x+width-1, y+height-1, color)
}

func (r *Runtime) drawGraphicsCircle(centerX, centerY, radius int32) error {
	if radius < 0 {
		return nil
	}
	if radius == 0 {
		return r.writeGraphicsPixel(centerX, centerY, r.graphics.stroke)
	}
	x, y := radius, int32(0)
	errorTerm := int32(1) - radius
	for x >= y {
		if r.graphics.fillMode {
			for _, span := range [][3]int32{
				{centerX - x, centerX + x, centerY + y},
				{centerX - x, centerX + x, centerY - y},
				{centerX - y, centerX + y, centerY + x},
				{centerX - y, centerX + y, centerY - x},
			} {
				if err := r.drawGraphicsLine(span[0], span[2], span[1], span[2], r.graphics.fill); err != nil {
					return err
				}
			}
		}
		for _, point := range [][2]int32{
			{centerX + x, centerY + y}, {centerX + y, centerY + x},
			{centerX - y, centerY + x}, {centerX - x, centerY + y},
			{centerX - x, centerY - y}, {centerX - y, centerY - x},
			{centerX + y, centerY - x}, {centerX + x, centerY - y},
		} {
			if err := r.writeGraphicsPixel(point[0], point[1], r.graphics.stroke); err != nil {
				return err
			}
		}
		y++
		if errorTerm < 0 {
			errorTerm += 2*y + 1
		} else {
			x--
			errorTerm += 2*(y-x) + 1
		}
	}
	return nil
}

func (r *Runtime) drawGraphicsPolygon(points []graphicsPoint, closed bool) error {
	if len(points) == 0 {
		return nil
	}
	translated := make([]graphicsPoint, len(points))
	for index, point := range points {
		translated[index] = graphicsPoint{x: point.x + r.graphics.originX, y: point.y + r.graphics.originY}
	}
	if closed && r.graphics.fillMode && len(translated) >= 3 {
		_, surfaceWidth, surfaceHeight, _, err := r.graphicsSurface()
		if err != nil {
			return err
		}
		if surfaceWidth == 0 || surfaceHeight == 0 {
			return nil
		}
		minimumY, maximumY := translated[0].y, translated[0].y
		for _, point := range translated[1:] {
			if point.y < minimumY {
				minimumY = point.y
			}
			if point.y > maximumY {
				maximumY = point.y
			}
		}
		minimumY = max32(minimumY, 0)
		maximumY = min32(maximumY, int32(surfaceHeight)-1)
		for y := minimumY; y <= maximumY; y++ {
			intersections := make([]int32, 0, len(translated))
			for index, start := range translated {
				end := translated[(index+1)%len(translated)]
				if !((start.y <= y && end.y > y) || (end.y <= y && start.y > y)) {
					continue
				}
				x := int64(start.x) + int64(y-start.y)*int64(end.x-start.x)/int64(end.y-start.y)
				intersections = append(intersections, int32(x))
			}
			sort.Slice(intersections, func(left, right int) bool { return intersections[left] < intersections[right] })
			for index := 0; index+1 < len(intersections); index += 2 {
				startX := max32(intersections[index], 0)
				endX := min32(intersections[index+1], int32(surfaceWidth)-1)
				if startX > endX {
					continue
				}
				if err := r.drawGraphicsLine(startX, y, endX, y, r.graphics.fill); err != nil {
					return err
				}
			}
		}
	}
	for index := 1; index < len(translated); index++ {
		if err := r.drawGraphicsLine(translated[index-1].x, translated[index-1].y, translated[index].x, translated[index].y, r.graphics.stroke); err != nil {
			return err
		}
	}
	if closed && len(translated) > 1 {
		return r.drawGraphicsLine(translated[len(translated)-1].x, translated[len(translated)-1].y, translated[0].x, translated[0].y, r.graphics.stroke)
	}
	return nil
}

func min32(left, right int32) int32 {
	if left < right {
		return left
	}
	return right
}

func max32(left, right int32) int32 {
	if left > right {
		return left
	}
	return right
}

func abs32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}
