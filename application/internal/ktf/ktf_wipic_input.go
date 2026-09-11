package ktf

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/mirusu400/aram-core/internal/ime"
	shared "github.com/mirusu400/aram-core/runtime"
)

// MH_KEY_PRESSED in the WIPI-C/HAL event ABI.
const ktfWIPICInputKeyPressed uint32 = 2

var ktfWIPICInputModes = [...]string{"EN/S", "EN/L", "N123", "KO"}

// The C ABI returns separate completed/composing strings, not Java edit callbacks.
// Slot ordering is corroborated by the KTF caller's six-argument HandleInput
// calls and the public graphics input-method sequence, not Raptor ordinals.
type ktfWIPICInputState struct {
	Automata    ime.State
	Pending     rune
	Initialized bool
}

func (s ktfWIPICInputState) words() [11]uint32 {
	a := s.Automata
	w := [11]uint32{uint32(a.Mode), uint32(a.ComposingKey), uint32(a.ComposingIndex), uint32(a.Choseong), uint32(a.Jungseong), uint32(a.Jongseong), 0, uint32(a.LastKey), uint32(a.LastIndex), uint32(s.Pending), 0}
	if a.Composing {
		w[6] = 1
	}
	if s.Initialized {
		w[10] = 1
	}
	return w
}

func ktfWIPICInputFromWords(w [11]uint32) (ktfWIPICInputState, error) {
	if w[0] >= uint32(ime.ModeCount) || w[2] > 7 || w[6] > 1 || w[8] > 7 || w[10] > 1 || w[9] > 0x10ffff || (w[9] >= 0xd800 && w[9] <= 0xdfff) {
		return ktfWIPICInputState{}, errors.New("invalid KTF C input state")
	}
	for i, maximum := range []int32{18, 20, 27} {
		minimum := []int32{-1, -3, 0}[i]
		if int32(w[i+3]) < minimum || int32(w[i+3]) > maximum {
			return ktfWIPICInputState{}, errors.New("invalid KTF C input composition")
		}
	}
	return ktfWIPICInputState{Automata: ime.State{Mode: int32(w[0]), ComposingKey: int32(w[1]), ComposingIndex: int32(w[2]), Choseong: int32(w[3]), Jungseong: int32(w[4]), Jongseong: int32(w[5]), Composing: w[6] != 0, LastKey: int32(w[7]), LastIndex: int32(w[8])}, Pending: rune(w[9]), Initialized: w[10] != 0}, nil
}

func (s ktfWIPICInputState) automata() ime.Automata {
	a := ime.New(ime.ModeENLower)
	if s.Initialized {
		a.Restore(s.Automata)
	}
	return a
}

func ktfWIPICInputSetMode(_ context.Context, r *Runtime) (uint32, error) {
	mode, err := r.parameter(0)
	if err != nil {
		return 0, err
	}
	if mode >= uint32(len(ktfWIPICInputModes)) {
		return 0, nil
	}
	a := ime.New(ime.Mode(mode))
	r.wipicInput = ktfWIPICInputState{Automata: a.Snapshot(), Initialized: true}
	return 1, nil
}

func ktfWIPICInputGetMode(_ context.Context, r *Runtime) (uint32, error) {
	a := r.wipicInput.automata()
	return uint32(a.CurrentMode()), nil
}

func ktfWIPICInputHandle(_ context.Context, r *Runtime) (uint32, error) {
	var args [6]uint32
	for i := range args {
		v, err := r.parameter(uint32(i))
		if err != nil {
			return 0, err
		}
		args[i] = v
	}
	// MH_KEY_PRESSED is 2. Releases and other events do not compose text.
	if args[1] != ktfWIPICInputKeyPressed {
		return 0, nil
	}
	var capacity [2]uint32
	for i, p := range []uint32{args[3], args[5]} {
		if p == 0 || args[2+i*2] == 0 {
			return 0, nil
		}
		var b [4]byte
		if err := r.CPU.ReadMemory(p, b[:]); err != nil {
			return 0, err
		}
		capacity[i] = binary.LittleEndian.Uint32(b[:])
		if int32(capacity[i]) <= 0 {
			return 0, nil
		}
	}
	next := r.wipicInput
	a := next.automata()
	completed := []rune{}
	handled := false
	// C char is signed for MH_IMA_FLUSH (-99), including zero-extended 0x9d.
	key := int32(int8(args[0]))
	if args[1] == ktfWIPICInputKeyPressed {
		if key >= '0' && key <= '9' {
			ops, ok := a.Press(key)
			handled = ok
			for _, op := range ops {
				switch op.Kind {
				case ime.OpInsert:
					if next.Pending != 0 {
						completed = append(completed, next.Pending)
					}
					next.Pending = op.Char
				case ime.OpReplace:
					next.Pending = op.Char
				case ime.OpDelete:
					next.Pending = 0
				}
			}
			state := a.Snapshot()
			if state.ComposingKey == 0 && !state.Composing && next.Pending != 0 {
				completed = append(completed, next.Pending)
				next.Pending = 0
			}
		} else {
			// WIPI 2.0 explicitly commits pending text even for unhandled keys.
			if next.Pending != 0 {
				completed = append(completed, next.Pending)
				next.Pending = 0
			}
			a.Commit()
			handled = key == -99
		}
	}
	next.Automata, next.Initialized = a.Snapshot(), true
	composing := ""
	if next.Pending != 0 {
		composing = string(next.Pending)
	}
	var output [2][]byte
	for i, text := range []string{string(completed), composing} {
		encoded, err := r.Services.Text.Encode(text, shared.EncodingEUCKR)
		if err != nil {
			return 0, err
		}
		output[i] = append(encoded, 0)
		if uint64(len(output[i])) > uint64(capacity[i]) {
			return 0, nil
		}
		// Check both readable ranges before writing. This does not prove write
		// permissions or make separate guest-memory writes atomic.
		if err := r.CPU.ReadMemory(args[2+i*2], make([]byte, len(output[i]))); err != nil {
			return 0, err
		}
	}
	for i, data := range output {
		if err := r.CPU.WriteMemory(args[2+i*2], data); err != nil {
			return 0, err
		}
	}
	r.wipicInput = next
	if handled {
		return 1, nil
	}
	return 0, nil
}

func ktfWIPICInputGetSupportedModeCount(
	_ context.Context,
	_ *Runtime,
) (uint32, error) {
	return uint32(len(ktfWIPICInputModes)), nil
}

func ktfWIPICInputGetSupportedModes(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	if runtime.wipicInputModes != 0 {
		return runtime.wipicInputModes, nil
	}
	size := len(ktfWIPICInputModes) * 4
	for _, mode := range ktfWIPICInputModes {
		size += len(mode) + 1
	}
	address, err := runtime.allocateJavaHeapBytes(uint32(size), true)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return 0, errors.New("KTF guest heap exhausted allocating input modes")
	}

	encoded := make([]byte, size)
	cursor := len(ktfWIPICInputModes) * 4
	for index, mode := range ktfWIPICInputModes {
		binary.LittleEndian.PutUint32(
			encoded[index*4:],
			address+uint32(cursor),
		)
		copy(encoded[cursor:], mode)
		cursor += len(mode) + 1
	}
	if err := runtime.CPU.WriteMemory(address, encoded); err != nil {
		runtime.Heap.Release(address)
		return 0, err
	}
	runtime.wipicInputModes = address
	return address, nil
}
