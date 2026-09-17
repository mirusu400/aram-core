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

func (r *Runtime) handleMenuControl(slot uint32) (bool, error) {
	switch slot {
	case 2: // HandleEvent
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 3: // Redraw
		return true, nil
	case 4: // SetActive
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.menuControl.active = value != 0
		return true, nil
	case 5: // IsActive
		return true, r.cpu.WriteRegister(cpu.RegisterR0, boolWord(r.menuControl.active))
	case 6: // SetRect
		pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if pointer != 0 {
			if err := r.cpu.ReadMemory(pointer, r.menuControl.rect[:]); err != nil {
				return true, fmt.Errorf("read BREW menu rectangle: %w", err)
			}
		}
		return true, nil
	case 7: // GetRect
		pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if pointer != 0 {
			if err := r.cpu.WriteMemory(pointer, r.menuControl.rect[:]); err != nil {
				return true, fmt.Errorf("write BREW menu rectangle: %w", err)
			}
		}
		return true, nil
	case 8: // SetProperties
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.menuControl.properties = value
		return true, nil
	case 9: // GetProperties
		return true, r.cpu.WriteRegister(cpu.RegisterR0, r.menuControl.properties)
	case 10: // Reset
		r.menuControl.items = nil
		r.menuControl.selection = 0
		r.menuControl.enumIndex = 0
		return true, nil
	case 11: // SetTitle
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
	case 12: // AddItem
		id, err := r.cpu.ReadRegister(cpu.RegisterR3)
		if err != nil {
			return true, err
		}
		sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
		if err != nil {
			return true, err
		}
		var encoded [8]byte
		if err := r.cpu.ReadMemory(sp, encoded[:]); err != nil {
			return true, fmt.Errorf("read BREW menu item arguments: %w", err)
		}
		r.setMenuItem(uint16(id), binary.LittleEndian.Uint32(encoded[4:8]))
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
	case 13: // AddItemEx
		pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if pointer == 0 {
			return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		var encoded [28]byte
		if err := r.cpu.ReadMemory(pointer, encoded[:]); err != nil {
			return true, fmt.Errorf("read BREW extended menu item: %w", err)
		}
		r.setMenuItem(binary.LittleEndian.Uint16(encoded[22:24]), binary.LittleEndian.Uint32(encoded[24:28]))
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
	case 14: // GetItemData
		id, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		output, err := r.cpu.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return true, err
		}
		for _, item := range r.menuControl.items {
			if item.id == uint16(id) {
				if output != 0 {
					var encoded [4]byte
					binary.LittleEndian.PutUint32(encoded[:], item.data)
					if err := r.cpu.WriteMemory(output, encoded[:]); err != nil {
						return true, fmt.Errorf("write BREW menu item data: %w", err)
					}
				}
				return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
			}
		}
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 15: // DeleteItem
		id, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		for index, item := range r.menuControl.items {
			if item.id == uint16(id) {
				r.menuControl.items = append(r.menuControl.items[:index], r.menuControl.items[index+1:]...)
				return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
			}
		}
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 16: // DeleteAll
		r.menuControl.items = nil
		r.menuControl.selection = 0
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
	case 17: // SetSel
		value, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		r.menuControl.selection = uint16(value)
		return true, nil
	case 18: // GetSel
		return true, r.cpu.WriteRegister(cpu.RegisterR0, uint32(r.menuControl.selection))
	case 19, 20, 21, 23, 24, 30, 31, 34, 35, 36, 37:
		// State-independent setters and drawing customization are accepted without
		// inventing host UI behavior.
		return true, nil
	case 22: // GetItemTime
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 1)
	case 25: // MoveItem
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 26: // GetItemCount
		return true, r.cpu.WriteRegister(cpu.RegisterR0, uint32(len(r.menuControl.items)))
	case 27: // GetItemID
		index, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if index >= uint32(len(r.menuControl.items)) {
			return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		return true, r.cpu.WriteRegister(cpu.RegisterR0, uint32(r.menuControl.items[index].id))
	case 28, 29: // GetItem, SetItem
		return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 32: // EnumSelInit
		r.menuControl.enumIndex = 0
		return true, r.cpu.WriteRegister(cpu.RegisterR0, boolWord(len(r.menuControl.items) != 0))
	case 33: // EnumNextSel
		if r.menuControl.enumIndex >= len(r.menuControl.items) {
			return true, r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		id := r.menuControl.items[r.menuControl.enumIndex].id
		r.menuControl.enumIndex++
		return true, r.cpu.WriteRegister(cpu.RegisterR0, uint32(id))
	default:
		return false, nil
	}
}

func (r *Runtime) setMenuItem(id uint16, data uint32) {
	for index := range r.menuControl.items {
		if r.menuControl.items[index].id == id {
			r.menuControl.items[index].data = data
			return
		}
	}
	r.menuControl.items = append(r.menuControl.items, brewMenuItem{id: id, data: data})
	if len(r.menuControl.items) == 1 {
		r.menuControl.selection = id
	}
}
