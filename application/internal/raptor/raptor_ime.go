package raptor

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/internal/ime"
	shared "github.com/mirusu400/aram-core/runtime"
)

// LGT exports the InputMethod HAL as module 507 ordinals 300..304.
// These are mode count/list/set/get and handleInput, not filesystem volumes.
// The six-argument handleInput ABI and separate committed/preedit byte buffers
// are documented at https://mirusu400.github.io/wipi-wiki/hal/input-method.md.
// Wild Frontier's name widget calls this ABI directly rather than MC_uic*.
const IMEModeTable = uint32(0x01008c00)

var raptorIMEModes = [...]string{"EN/S", "EN/L", "N123", "KO"}

type inputMethod struct {
	automata ime.Automata
	preedit  rune
}

func (r *Runtime) inputMethod() *inputMethod {
	if r.ime == nil {
		r.ime = &inputMethod{automata: ime.New(ime.ModeENLower)}
	}
	return r.ime
}

func (r *Runtime) installInputMethodModes() error {
	data := make([]byte, 40)
	offset := len(raptorIMEModes) * 4
	for i, mode := range raptorIMEModes {
		binary.LittleEndian.PutUint32(data[i*4:], IMEModeTable+uint32(offset))
		copy(data[offset:], mode)
		offset += len(mode) + 1
	}
	return r.CPU.WriteMemory(IMEModeTable, data[:offset])
}

func (r *Runtime) dispatchInputMethod(ordinal uint32) (guest.WIPIReturn, string, bool, error) {
	switch ordinal {
	case 300:
		return guest.WIPIReturn{Low: uint32(len(raptorIMEModes))}, "RAPTOR.IMAgetSupportModeCount", true, nil
	case 301:
		return guest.WIPIReturn{Low: IMEModeTable}, "RAPTOR.IMAgetSupportedModes", true, nil
	case 302:
		mode, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.IMAsetCurrentMode", true, err
		}
		if mode >= uint32(len(raptorIMEModes)) {
			return guest.WIPIReturn{}, "RAPTOR.IMAsetCurrentMode", true, nil
		}
		state := r.inputMethod()
		state.automata.SetMode(ime.Mode(mode))
		state.preedit = 0
		return guest.WIPIReturn{Low: 1}, "RAPTOR.IMAsetCurrentMode", true, nil
	case 303:
		return guest.WIPIReturn{Low: uint32(r.inputMethod().automata.CurrentMode())}, "RAPTOR.IMAgetCurrentMode", true, nil
	case 304:
		result, err := r.handleInputMethod()
		return result, "RAPTOR.IMAhandleInput", true, err
	}
	return guest.WIPIReturn{}, "", false, nil
}

func (r *Runtime) handleInputMethod() (guest.WIPIReturn, error) {
	var args [6]uint32
	for i := 0; i < 4; i++ {
		v, err := r.CPU.ReadRegister(uint32(i))
		if err != nil {
			return guest.WIPIReturn{}, err
		}
		args[i] = v
	}
	// The import dispatcher has already unwound the Raptor veneer. Extra
	// arguments therefore start at the caller's SP, as for public WIPI calls.
	sp, err := r.CPU.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	if uint64(sp)+8 > 1<<32 {
		return guest.WIPIReturn{}, fmt.Errorf("IMA argument address overflow")
	}
	var stack [8]byte
	if err = r.CPU.ReadMemory(sp, stack[:]); err != nil {
		return guest.WIPIReturn{}, err
	}
	args[4] = binary.LittleEndian.Uint32(stack[:4])
	args[5] = binary.LittleEndian.Uint32(stack[4:])
	if args[1] != raptorKeyPressEvent {
		return guest.WIPIReturn{}, nil
	}
	state := r.inputMethod()
	next := *state
	var text []rune
	if next.preedit != 0 {
		text = append(text, next.preedit)
	}
	key := int32(args[0])
	// The HAL prototype spells key as char, so accept either sign extension of
	// MH_IMA_FLUSH or the zero-extended low byte emitted by a char caller.
	if key == -99 || key == 157 {
		next.automata.Commit()
	} else {
		ops, handled := next.automata.Press(key)
		if !handled {
			return guest.WIPIReturn{}, nil
		}
		for _, op := range ops {
			switch op.Kind {
			case ime.OpInsert:
				text = append(text, op.Char)
			case ime.OpReplace:
				if len(text) > 0 {
					text[len(text)-1] = op.Char
				} else {
					text = append(text, op.Char)
				}
			case ime.OpDelete:
				if len(text) > 0 {
					text = text[:len(text)-1]
				}
			}
		}
	}
	next.preedit = 0
	snapshot := next.automata.Snapshot()
	if (snapshot.Composing || snapshot.ComposingKey != 0) && len(text) > 0 {
		next.preedit = text[len(text)-1]
		text = text[:len(text)-1]
	}
	preedit := ""
	if next.preedit != 0 {
		preedit = string(next.preedit)
	}
	outputs := [2]string{string(text), preedit}
	var encoded [2][]byte
	for i, value := range outputs {
		encoded[i], err = r.Public.Services.Text.Encode(value, shared.EncodingEUCKR)
		if err != nil {
			return guest.WIPIReturn{}, err
		}
		lengthAddress := args[3+i*2]
		if lengthAddress == 0 {
			return guest.WIPIReturn{}, nil
		}
		capacity, err := r.Public.ReadU32(lengthAddress)
		if err != nil {
			return guest.WIPIReturn{}, err
		}
		if capacity > 1<<20 || uint32(len(encoded[i])) > capacity {
			return guest.WIPIReturn{}, nil
		}
		address := args[2+i*2]
		if len(encoded[i]) > 0 {
			if address == 0 || uint64(address)+uint64(len(encoded[i])) > 1<<32 {
				return guest.WIPIReturn{}, nil
			}
			// Validate both outputs before committing any bytes or composition state.
			if err := r.CPU.ReadMemory(address, make([]byte, len(encoded[i]))); err != nil {
				return guest.WIPIReturn{}, err
			}
		}
	}
	for i, data := range encoded {
		if len(data) > 0 {
			if err := r.CPU.WriteMemory(args[2+i*2], data); err != nil {
				return guest.WIPIReturn{}, err
			}
		}
		if err := r.Public.WriteU32(args[3+i*2], uint32(len(data))); err != nil {
			return guest.WIPIReturn{}, err
		}
	}
	*state = next
	return guest.WIPIReturn{Low: 1}, nil
}
