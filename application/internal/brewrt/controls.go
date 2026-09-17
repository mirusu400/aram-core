package brewrt

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const maxTextControlBytes = uint32(4096)

func (r *Runtime) handleTextControl(slot uint32) (bool, error) {
	switch slot {
	case 2, 3: // HandleEvent, Redraw
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 4: // SetActive
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.textControl.active = value != 0
		return true, nil
	case 5: // IsActive
		return true, r.cpu.WriteRegister(cpu.RegisterR0, boolWord(r.textControl.active))
	case 6: // SetRect
		pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if pointer != 0 {
			if err := r.cpu.ReadMemory(pointer, r.textControl.rect[:]); err != nil {
				return true, fmt.Errorf("read BREW text-control rectangle: %w", err)
			}
		}
		return true, nil
	case 7: // GetRect
		pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if pointer != 0 {
			if err := r.cpu.WriteMemory(pointer, r.textControl.rect[:]); err != nil {
				return true, fmt.Errorf("write BREW text-control rectangle: %w", err)
			}
		}
		return true, nil
	case 8: // SetProperties
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.textControl.properties = value
		return true, nil
	case 9: // GetProperties
		return true, r.cpu.WriteRegister(cpu.RegisterR0, r.textControl.properties)
	case 10: // Reset
		r.textControl.text = nil
		r.textControl.cursor = 0
		return true, nil
	case 11: // SetTitle: accepted but not rendered by the portable control.
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
	case 12: // SetText
		return true, r.setTextControlText()
	case 13: // GetText
		return true, r.getTextControlText()
	case 14: // GetTextPtr
		return true, r.returnTextControlPointer()
	case 15: // EnableCommand
		return true, nil
	case 16: // SetMaxSize
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.textControl.maxSize = min(value, maxTextControlBytes/2-1)
		return true, nil
	case 17: // SetSoftKeyMenu
		return true, nil
	case 18: // SetInputMode
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.textControl.inputMode = value
		return true, nil
	case 19: // GetCursorPos
		return true, r.cpu.WriteRegister(cpu.RegisterR0, r.textControl.cursor)
	case 20: // SetCursorPos
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.textControl.cursor = min(value, uint32(len(r.textControl.text)/2))
		return true, nil
	case 21: // GetInputMode
		return true, r.cpu.WriteRegister(cpu.RegisterR0, r.textControl.inputMode)
	case 22, 23: // EnumModeInit, EnumNextMode
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
	default:
		return false, nil
	}
}

func (r *Runtime) setTextControlText() error {
	pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	if pointer == 0 {
		r.textControl.text = nil
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	}
	limit := maxTextControlBytes
	if r.textControl.maxSize != 0 {
		limit = min(limit, (r.textControl.maxSize+1)*2)
	}
	data := make([]byte, 0, limit)
	for offset := uint32(0); offset+2 <= limit; offset += 2 {
		var unit [2]byte
		if err := r.cpu.ReadMemory(pointer+offset, unit[:]); err != nil {
			return fmt.Errorf("read BREW text-control text: %w", err)
		}
		if binary.LittleEndian.Uint16(unit[:]) == 0 {
			break
		}
		data = append(data, unit[:]...)
	}
	r.textControl.text = data
	r.textControl.cursor = uint32(len(data) / 2)
	return r.cpu.WriteRegister(cpu.RegisterR0, 1)
}

func (r *Runtime) getTextControlText() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	characters, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return err
	}
	if destination == 0 || characters == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	count := min(uint32(len(r.textControl.text)), (characters-1)*2)
	encoded := make([]byte, count+2)
	copy(encoded, r.textControl.text[:count])
	if err := r.cpu.WriteMemory(destination, encoded); err != nil {
		return fmt.Errorf("write BREW text-control text: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 1)
}

func (r *Runtime) returnTextControlPointer() error {
	need := uint32(len(r.textControl.text) + 2)
	if r.textControl.textPtr == 0 {
		pointer, err := r.allocateGuest(maxTextControlBytes)
		if err != nil {
			return err
		}
		r.textControl.textPtr = pointer
	}
	encoded := make([]byte, need)
	copy(encoded, r.textControl.text)
	if err := r.cpu.WriteMemory(r.textControl.textPtr, encoded); err != nil {
		return fmt.Errorf("write BREW text-control stable text: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, r.textControl.textPtr)
}
