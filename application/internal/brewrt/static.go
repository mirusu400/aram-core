package brewrt

import (
	"encoding/binary"
	"fmt"
	"image"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
	"golang.org/x/image/font/basicfont"
)

// IStatic (AEECLSID_STATIC) property bits. The control-specific ST_ flags sit
// above the shared IControl bits: 스케이드보드 asks for 0x00230000, a centered
// title over centered text in the middle of the rectangle.
const (
	staticCenterText  = uint32(0x00010000)
	staticCenterTitle = uint32(0x00020000)
	staticMiddleText  = uint32(0x00200000)
	staticASCII       = uint32(0x01000000)
)

// brewStaticControl is one IStatic instance. SetText copies its strings: a
// title may free them straight after the call and Redraw only afterwards
// (스케이드보드 does exactly that).
type brewStaticControl struct {
	refs       uint32
	active     bool
	rect       [8]byte
	properties uint32
	title      string
	text       string
}

func (r *Runtime) createStaticControl() (uint32, error) {
	object, err := r.allocateGuest(4)
	if err != nil {
		return 0, err
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], staticCtlVTable)
	if err := r.cpu.WriteMemory(object, encoded[:]); err != nil {
		r.releaseGuest(object)
		return 0, fmt.Errorf("write BREW static control object: %w", err)
	}
	r.staticControls[object] = &brewStaticControl{refs: 1}
	return object, nil
}

// handleStaticControl implements the IControl prefix and IStatic::SetText. A
// slot past SetText reports false so the caller raises a precise boundary
// rather than guessing at a method whose order the corpus has not shown.
func (r *Runtime) handleStaticControl(slot uint32) (bool, error) {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return true, fmt.Errorf("read BREW static control object: %w", err)
	}
	control, ok := r.staticControls[object]
	if !ok {
		return true, fmt.Errorf("BREW static control object 0x%08x is not active", object)
	}
	argument := func(register uint32) (uint32, error) {
		value, err := r.cpu.ReadRegister(register)
		if err != nil {
			return 0, fmt.Errorf("read BREW static control argument: %w", err)
		}
		return value, nil
	}
	returnValue := func(value uint32) (bool, error) {
		return true, r.cpu.WriteRegister(cpu.RegisterR0, value)
	}
	switch slot {
	case 0: // AddRef
		control.refs++
		return returnValue(control.refs)
	case 1: // Release
		if control.refs > 0 {
			control.refs--
		}
		if control.refs == 0 {
			delete(r.staticControls, object)
			r.releaseGuest(object)
		}
		return returnValue(control.refs)
	case 2: // HandleEvent: a static control consumes no event.
		return returnValue(0)
	case 3: // Redraw
		if err := r.redrawStaticControl(control); err != nil {
			return true, err
		}
		return returnValue(1)
	case 4: // SetActive
		value, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		control.active = value != 0
		return true, nil
	case 5: // IsActive
		return returnValue(boolWord(control.active))
	case 6: // SetRect
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if pointer != 0 {
			if err := r.cpu.ReadMemory(pointer, control.rect[:]); err != nil {
				return true, fmt.Errorf("read BREW static control rectangle: %w", err)
			}
		}
		return true, nil
	case 7: // GetRect
		pointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if pointer != 0 {
			if err := r.cpu.WriteMemory(pointer, control.rect[:]); err != nil {
				return true, fmt.Errorf("write BREW static control rectangle: %w", err)
			}
		}
		return true, nil
	case 8: // SetProperties
		value, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		control.properties = value
		return true, nil
	case 9: // GetProperties
		return returnValue(control.properties)
	case 10: // Reset
		control.title, control.text = "", ""
		return true, nil
	case 11: // SetText(IStatic *, AECHAR *title, AECHAR *text, AEEFont, AEEFont)
		titlePointer, err := argument(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		textPointer, err := argument(cpu.RegisterR2)
		if err != nil {
			return true, err
		}
		ascii := control.properties&staticASCII != 0
		title, err := r.displayText(titlePointer, ^uint32(0), ascii)
		if err != nil {
			return true, fmt.Errorf("read BREW static control title: %w", err)
		}
		text, err := r.displayText(textPointer, ^uint32(0), ascii)
		if err != nil {
			return true, fmt.Errorf("read BREW static control text: %w", err)
		}
		control.title, control.text = title, text
		return returnValue(1)
	default:
		return false, nil
	}
}

// redrawStaticControl draws the title on the first line and wraps the text
// to the control's width, honoring the centering properties. It leaves the
// rectangle's background alone: the titles seen so far frame and fill it
// themselves before calling Redraw.
func (r *Runtime) redrawStaticControl(control *brewStaticControl) error {
	x := int(int16(binary.LittleEndian.Uint16(control.rect[0:2])))
	y := int(int16(binary.LittleEndian.Uint16(control.rect[2:4])))
	width := int(int16(binary.LittleEndian.Uint16(control.rect[4:6])))
	height := int(int16(binary.LittleEndian.Uint16(control.rect[6:8])))
	if width <= 0 || height <= 0 {
		return nil
	}
	clip := image.Rect(x, y, x+width, y+height).
		Intersect(image.Rect(0, 0, int(r.screenWidth), int(r.screenHeight)))
	if clip.Empty() {
		return nil
	}
	lineHeight := basicfont.Face7x13.Height
	type line struct {
		text     string
		centered bool
	}
	var lines []line
	if control.title != "" {
		lines = append(lines, line{control.title, control.properties&staticCenterTitle != 0})
	}
	wrapped, err := r.wrapStaticText(control.text, int64(clip.Dx()))
	if err != nil {
		return err
	}
	for _, text := range wrapped {
		lines = append(lines, line{text, control.properties&staticCenterText != 0})
	}
	top := clip.Min.Y
	if control.properties&staticMiddleText != 0 {
		if block := len(lines) * lineHeight; block < clip.Dy() {
			top += (clip.Dy() - block) / 2
		}
	}
	for index, current := range lines {
		lineTop := top + index*lineHeight
		if lineTop >= clip.Max.Y {
			break
		}
		flags := displayAlignLeft
		if current.centered {
			flags = displayAlignCenter
		}
		if err := r.renderDisplayText(current.text, int32(clip.Min.X), int32(lineTop), clip, flags); err != nil {
			return err
		}
	}
	return nil
}

// wrapStaticText breaks text at explicit newlines and wherever the next
// character would cross width, preferring the last space on the line. Korean
// text carries few spaces, so a long run still breaks between characters.
func (r *Runtime) wrapStaticText(text string, width int64) ([]string, error) {
	var lines []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		var current []rune
		var currentWidth int64
		lastSpace := -1
		for _, character := range paragraph {
			advance, err := r.displayCharacterWidth(character)
			if err != nil {
				return nil, err
			}
			if currentWidth+advance > width && len(current) != 0 {
				if lastSpace > 0 {
					lines = append(lines, string(current[:lastSpace]))
					current = append([]rune(nil), current[lastSpace+1:]...)
				} else {
					lines = append(lines, string(current))
					current = current[:0]
				}
				currentWidth, err = r.displayTextWidth(string(current))
				if err != nil {
					return nil, err
				}
				lastSpace = -1
				for index, kept := range current {
					if kept == ' ' {
						lastSpace = index
					}
				}
			}
			if character == ' ' {
				lastSpace = len(current)
			}
			current = append(current, character)
			currentWidth += advance
		}
		if len(current) != 0 || paragraph == "" {
			lines = append(lines, string(current))
		}
	}
	for len(lines) != 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}
