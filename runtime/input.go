package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type InputState struct {
	MaxControls    uint32
	RepeatDelayNS  int64
	RepeatPeriodNS int64
	Focused        bool
	Controls       []InputControlState
}

type InputControlState struct {
	Name       string
	Pressed    bool
	NextRepeat int64
}

type inputControl struct {
	pressed    bool
	nextRepeat time.Duration
}

type inputAdvanceState struct {
	controls []inputControlAdvanceState
}

type inputControlAdvanceState struct {
	name    string
	control inputControl
}

// Input tracks held controls and emits normalized press/release/repeat events.
type Input struct {
	maxControls  uint32
	repeatDelay  time.Duration
	repeatPeriod time.Duration
	focused      bool
	controls     map[string]inputControl
	dueNames     []string
}

func NewInput(maxControls uint32, repeatDelay, repeatPeriod time.Duration) *Input {
	if maxControls == 0 {
		maxControls = 128
	}
	if repeatDelay <= 0 {
		repeatDelay = 500 * time.Millisecond
	}
	if repeatPeriod <= 0 {
		repeatPeriod = 50 * time.Millisecond
	}
	return &Input{
		maxControls:  maxControls,
		repeatDelay:  repeatDelay,
		repeatPeriod: repeatPeriod,
		focused:      true,
		controls:     make(map[string]inputControl),
	}
}

func (i *Input) SetFocus(focused bool) {
	i.focused = focused
}

func (i *Input) Held(control string) bool {
	return i.controls[control].pressed
}

func (i *Input) Change(bus *EventBus, owner OwnerID, control string, pressed bool, at time.Duration) error {
	if bus == nil || strings.TrimSpace(control) == "" || len(control) > 255 ||
		strings.IndexByte(control, 0) >= 0 || at < 0 {
		return fmt.Errorf("%w: invalid input change", ErrInvalidArgument)
	}
	current, exists := i.controls[control]
	if !exists && uint32(len(i.controls)) >= i.maxControls {
		return fmt.Errorf("%w: input controls reached %d", ErrLimitExceeded, i.maxControls)
	}
	if current.pressed == pressed {
		return nil
	}
	current.pressed = pressed
	if pressed {
		if at > time.Duration(math.MaxInt64-int64(i.repeatDelay)) {
			return fmt.Errorf("%w: input repeat deadline overflow", ErrLimitExceeded)
		}
		current.nextRepeat = at + i.repeatDelay
	} else {
		current.nextRepeat = 0
	}
	if !i.focused {
		i.controls[control] = current
		return nil
	}
	kind := EventInputRelease
	if pressed {
		kind = EventInputPress
	}
	if _, err := bus.Enqueue(Event{
		At:      at,
		Kind:    kind,
		Owner:   owner,
		Control: control,
	}); err != nil {
		return err
	}
	i.controls[control] = current
	return nil
}

func (i *Input) Advance(bus *EventBus, owner OwnerID, now time.Duration) error {
	if bus == nil || now < 0 {
		return fmt.Errorf("%w: invalid input advance", ErrInvalidArgument)
	}
	if !i.focused {
		return nil
	}
	busBefore := bus.Snapshot()
	var inputBefore inputAdvanceState
	if err := i.advanceLocked(bus, owner, now, &inputBefore); err != nil {
		_ = bus.Restore(busBefore)
		i.restoreAdvance(&inputBefore)
		return err
	}
	return nil
}

// advanceLocked emits repeats without taking a full component snapshot. The
// caller owns the event-bus transaction and may retain saved to undo only the
// controls whose repeat deadlines changed.
func (i *Input) advanceLocked(
	bus *EventBus,
	owner OwnerID,
	now time.Duration,
	saved *inputAdvanceState,
) error {
	if bus == nil || now < 0 {
		return fmt.Errorf("%w: invalid input advance", ErrInvalidArgument)
	}
	if saved != nil {
		saved.controls = saved.controls[:0]
	}
	if !i.focused {
		return nil
	}
	i.dueNames = i.dueNames[:0]
	for name, control := range i.controls {
		if control.pressed && control.nextRepeat <= now {
			i.dueNames = append(i.dueNames, name)
		}
	}
	sort.Strings(i.dueNames)
	for _, name := range i.dueNames {
		control := i.controls[name]
		if saved != nil {
			saved.controls = append(saved.controls, inputControlAdvanceState{
				name:    name,
				control: control,
			})
		}
		for control.nextRepeat <= now {
			// Coalesce: a repeat this control has not had delivered yet says
			// everything a second one would. Without this a title that stops
			// taking input while a key is held - because its own key handler
			// has not returned - watches the queue fill at the repeat rate
			// until it hits the bound and dies.
			if !bus.HasPendingRepeat(owner, name) {
				if _, err := bus.Enqueue(Event{
					At:      control.nextRepeat,
					Kind:    EventInputRepeat,
					Owner:   owner,
					Control: name,
				}); err != nil {
					return err
				}
			}
			if control.nextRepeat > time.Duration(math.MaxInt64-int64(i.repeatPeriod)) {
				return fmt.Errorf(
					"%w: input repeat deadline overflow",
					ErrLimitExceeded,
				)
			}
			control.nextRepeat += i.repeatPeriod
		}
		i.controls[name] = control
	}
	return nil
}

func (i *Input) restoreAdvance(saved *inputAdvanceState) {
	if saved == nil {
		return
	}
	for _, change := range saved.controls {
		i.controls[change.name] = change.control
	}
}

func (i *Input) Snapshot() InputState {
	state := InputState{
		MaxControls:    i.maxControls,
		RepeatDelayNS:  int64(i.repeatDelay),
		RepeatPeriodNS: int64(i.repeatPeriod),
		Focused:        i.focused,
	}
	names := make([]string, 0, len(i.controls))
	for name := range i.controls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		control := i.controls[name]
		state.Controls = append(state.Controls, InputControlState{
			Name:       name,
			Pressed:    control.pressed,
			NextRepeat: int64(control.nextRepeat),
		})
	}
	return state
}

func (i *Input) Restore(state InputState) error {
	if state.MaxControls == 0 ||
		state.RepeatDelayNS <= 0 ||
		state.RepeatPeriodNS <= 0 ||
		len(state.Controls) > int(state.MaxControls) {
		return fmt.Errorf("%w: invalid input state limits", ErrInvalidState)
	}
	controls := make(map[string]inputControl, len(state.Controls))
	previous := ""
	for index, saved := range state.Controls {
		if strings.TrimSpace(saved.Name) == "" || len(saved.Name) > 255 ||
			strings.IndexByte(saved.Name, 0) >= 0 ||
			(index != 0 && saved.Name <= previous) ||
			saved.NextRepeat < 0 ||
			(saved.Pressed && saved.NextRepeat == 0) ||
			(!saved.Pressed && saved.NextRepeat != 0) {
			return fmt.Errorf("%w: invalid input control %d", ErrInvalidState, index)
		}
		controls[saved.Name] = inputControl{
			pressed:    saved.Pressed,
			nextRepeat: time.Duration(saved.NextRepeat),
		}
		previous = saved.Name
	}
	i.maxControls = state.MaxControls
	i.repeatDelay = time.Duration(state.RepeatDelayNS)
	i.repeatPeriod = time.Duration(state.RepeatPeriodNS)
	i.focused = state.Focused
	i.controls = controls
	return nil
}
