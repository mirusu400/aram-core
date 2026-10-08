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
	Pending        []InputChangeState
}

// InputChangeState stores a future transition. Equal timestamps retain the
// order in which QueueInput accepted them.
type InputChangeState struct {
	AtNS    int64
	Owner   OwnerID
	Control string
	Pressed bool
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
	controls     []inputControlAdvanceState
	pending      []InputChangeState
	pendingSaved bool
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
	pending      []InputChangeState
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

func validInputControl(control string) bool {
	return strings.TrimSpace(control) != "" && len(control) <= 255 && strings.IndexByte(control, 0) < 0
}

// queueChange defers future transitions until Advance reaches their timestamp.
// Reserving the control now makes control-limit failures atomic at queue time.
func (i *Input) queueChange(bus *EventBus, owner OwnerID, control string, pressed bool, at, now time.Duration) error {
	if bus == nil || !validInputControl(control) || at < 0 || now < 0 {
		return fmt.Errorf("%w: invalid input change", ErrInvalidArgument)
	}
	if at <= now {
		return i.Change(bus, owner, control, pressed, at)
	}
	if pressed && at > time.Duration(math.MaxInt64-int64(i.repeatDelay)) {
		return fmt.Errorf("%w: input repeat deadline overflow", ErrLimitExceeded)
	}
	_, exists := i.controls[control]
	if !exists && uint32(len(i.controls)) >= i.maxControls {
		return fmt.Errorf("%w: input controls reached %d", ErrLimitExceeded, i.maxControls)
	}
	if uint64(len(i.pending)) >= uint64(bus.maxEvents) {
		return fmt.Errorf("%w: pending input reached %d", ErrLimitExceeded, bus.maxEvents)
	}
	index := sort.Search(len(i.pending), func(index int) bool {
		return i.pending[index].AtNS > int64(at)
	})
	i.pending = append(i.pending, InputChangeState{})
	copy(i.pending[index+1:], i.pending[index:])
	i.pending[index] = InputChangeState{AtNS: int64(at), Owner: owner, Control: control, Pressed: pressed}
	if !exists {
		i.controls[control] = inputControl{}
	}
	return nil
}

// Change applies a transition immediately, using at for its event and repeat
// timestamps. Use Services.QueueInput to schedule a transition in virtual time.
func (i *Input) Change(bus *EventBus, owner OwnerID, control string, pressed bool, at time.Duration) error {
	if bus == nil || !validInputControl(control) || at < 0 {
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
	busBefore := bus.Snapshot()
	var inputBefore inputAdvanceState
	if err := i.advanceLocked(bus, owner, now, &inputBefore); err != nil {
		_ = bus.Restore(busBefore)
		i.restoreAdvance(&inputBefore)
		return err
	}
	return nil
}

// advanceLocked applies due transitions and emits repeats without taking a full
// component snapshot. The caller owns the event-bus transaction and may retain
// saved to undo changes to controls and the pending queue.
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
		saved.pendingSaved = false
	}
	due := 0
	for due < len(i.pending) && i.pending[due].AtNS <= int64(now) {
		change := i.pending[due]
		if saved != nil && !saved.pendingSaved {
			saved.pending = append(saved.pending[:0], i.pending...)
			saved.pendingSaved = true
		}
		// A transition wins over a repeat at the same timestamp. Repeats before
		// it still belong to the previous held state, even in a long advance.
		if err := i.advanceRepeats(bus, owner, time.Duration(change.AtNS)-1, saved); err != nil {
			return err
		}
		i.captureControl(saved, change.Control)
		if err := i.Change(bus, change.Owner, change.Control, change.Pressed, time.Duration(change.AtNS)); err != nil {
			return err
		}
		due++
	}
	if err := i.advanceRepeats(bus, owner, now, saved); err != nil {
		return err
	}
	if due != 0 {
		copy(i.pending, i.pending[due:])
		clear(i.pending[len(i.pending)-due:])
		i.pending = i.pending[:len(i.pending)-due]
	}
	return nil
}

func (i *Input) advanceRepeats(bus *EventBus, owner OwnerID, now time.Duration, saved *inputAdvanceState) error {
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
		i.captureControl(saved, name)
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

func (i *Input) captureControl(saved *inputAdvanceState, name string) {
	if saved == nil {
		return
	}
	for _, previous := range saved.controls {
		if previous.name == name {
			return
		}
	}
	saved.controls = append(saved.controls, inputControlAdvanceState{name: name, control: i.controls[name]})
}

func (i *Input) restoreAdvance(saved *inputAdvanceState) {
	if saved == nil {
		return
	}
	for _, change := range saved.controls {
		i.controls[change.name] = change.control
	}
	if saved.pendingSaved {
		i.pending = append(i.pending[:0], saved.pending...)
	}
}

func (i *Input) Snapshot() InputState {
	state := InputState{
		MaxControls:    i.maxControls,
		RepeatDelayNS:  int64(i.repeatDelay),
		RepeatPeriodNS: int64(i.repeatPeriod),
		Focused:        i.focused,
		Pending:        append([]InputChangeState(nil), i.pending...),
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
		if !validInputControl(saved.Name) ||
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
	for index, change := range state.Pending {
		_, exists := controls[change.Control]
		if !exists || change.AtNS <= 0 || (index > 0 && change.AtNS < state.Pending[index-1].AtNS) ||
			(change.Pressed && change.AtNS > math.MaxInt64-state.RepeatDelayNS) {
			return fmt.Errorf("%w: invalid pending input %d", ErrInvalidState, index)
		}
	}
	i.maxControls = state.MaxControls
	i.repeatDelay = time.Duration(state.RepeatDelayNS)
	i.repeatPeriod = time.Duration(state.RepeatPeriodNS)
	i.focused = state.Focused
	i.controls = controls
	i.pending = append([]InputChangeState(nil), state.Pending...)
	return nil
}
