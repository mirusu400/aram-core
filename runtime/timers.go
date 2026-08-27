package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type TimerState struct {
	ID         ServiceID
	Owner      OwnerID
	Name       string
	DeadlineNS int64
	IntervalNS int64
	Active     bool
	Value      int64
}

type TimersState struct {
	MaxTimers uint32
	Timers    []TimerState
}

type Timer struct {
	ID       ServiceID
	Owner    OwnerID
	Name     string
	Deadline time.Duration
	Interval time.Duration
	Active   bool
	Value    int64
}

type timersAdvanceState struct {
	timers []timerAdvanceState
}

type timerAdvanceState struct {
	timer    *Timer
	deadline time.Duration
	active   bool
}

// Timers owns ordered virtual-time deadlines. Callback representations remain
// in the adapter; expiry is delivered as a guest-neutral event.
type Timers struct {
	registry  *Registry
	maxTimers uint32
	timers    map[ServiceID]*Timer
}

func NewTimers(registry *Registry, maxTimers uint32) *Timers {
	if registry == nil {
		registry = NewRegistry(0)
	}
	if maxTimers == 0 {
		maxTimers = 1024
	}
	return &Timers{
		registry:  registry,
		maxTimers: maxTimers,
		timers:    make(map[ServiceID]*Timer),
	}
}

func (t *Timers) Define(owner OwnerID, name string) (ServiceID, error) {
	if strings.TrimSpace(name) == "" || len(name) > 255 ||
		strings.IndexByte(name, 0) >= 0 {
		return 0, fmt.Errorf("%w: invalid timer name", ErrInvalidArgument)
	}
	if uint32(len(t.timers)) >= t.maxTimers {
		return 0, fmt.Errorf("%w: timer count reached %d", ErrLimitExceeded, t.maxTimers)
	}
	id, err := t.registry.Create(owner, KindTimer)
	if err != nil {
		return 0, err
	}
	t.timers[id] = &Timer{ID: id, Owner: owner, Name: name}
	return id, nil
}

func (t *Timers) Set(id ServiceID, owner OwnerID, deadline, interval time.Duration, value int64) error {
	timer, err := t.get(id, owner)
	if err != nil {
		return err
	}
	if deadline < 0 || interval < 0 {
		return fmt.Errorf("%w: negative timer duration", ErrInvalidArgument)
	}
	timer.Deadline = deadline
	timer.Interval = interval
	timer.Value = value
	timer.Active = true
	return nil
}

func (t *Timers) Cancel(id ServiceID, owner OwnerID) error {
	timer, err := t.get(id, owner)
	if err != nil {
		return err
	}
	timer.Active = false
	return nil
}

func (t *Timers) Get(id ServiceID, owner OwnerID) (Timer, error) {
	timer, err := t.get(id, owner)
	if err != nil {
		return Timer{}, err
	}
	return *timer, nil
}

func (t *Timers) Destroy(id ServiceID, owner OwnerID, bus *EventBus) error {
	if _, err := t.get(id, owner); err != nil {
		return err
	}
	if err := t.registry.Destroy(id, owner, KindTimer); err != nil {
		return err
	}
	delete(t.timers, id)
	if bus != nil {
		bus.RemoveService(id)
	}
	return nil
}

func (t *Timers) Advance(now time.Duration, bus *EventBus) error {
	if now < 0 || bus == nil {
		return fmt.Errorf("%w: invalid timer advance", ErrInvalidArgument)
	}
	busBefore := bus.Snapshot()
	var timerBefore timersAdvanceState
	if err := t.advanceLocked(now, bus, &timerBefore); err != nil {
		_ = bus.Restore(busBefore)
		t.restoreAdvance(&timerBefore)
		return err
	}
	return nil
}

// advanceLocked records only timers that it mutates. A repeating timer may be
// selected more than once in one large virtual-time step, so its original
// deadline is journaled once and restored by pointer on transaction failure.
func (t *Timers) advanceLocked(
	now time.Duration,
	bus *EventBus,
	saved *timersAdvanceState,
) error {
	if now < 0 || bus == nil {
		return fmt.Errorf("%w: invalid timer advance", ErrInvalidArgument)
	}
	if saved != nil {
		saved.timers = saved.timers[:0]
	}
	for {
		var selected *Timer
		for _, timer := range t.timers {
			if !timer.Active || timer.Deadline > now {
				continue
			}
			if selected == nil ||
				timer.Deadline < selected.Deadline ||
				timer.Deadline == selected.Deadline && timer.ID < selected.ID {
				selected = timer
			}
		}
		if selected == nil {
			return nil
		}
		if saved != nil {
			alreadySaved := false
			for _, change := range saved.timers {
				if change.timer == selected {
					alreadySaved = true
					break
				}
			}
			if !alreadySaved {
				saved.timers = append(saved.timers, timerAdvanceState{
					timer:    selected,
					deadline: selected.Deadline,
					active:   selected.Active,
				})
			}
		}
		if _, err := bus.Enqueue(Event{
			At:        selected.Deadline,
			Kind:      EventTimer,
			Owner:     selected.Owner,
			ServiceID: selected.ID,
			Name:      selected.Name,
			Value:     selected.Value,
		}); err != nil {
			return err
		}
		if selected.Interval == 0 {
			selected.Active = false
			continue
		}
		if selected.Deadline > time.Duration(math.MaxInt64-int64(selected.Interval)) {
			return fmt.Errorf(
				"%w: timer deadline overflow",
				ErrLimitExceeded,
			)
		}
		selected.Deadline += selected.Interval
	}
}

func (t *Timers) restoreAdvance(saved *timersAdvanceState) {
	if saved == nil {
		return
	}
	for _, change := range saved.timers {
		change.timer.Deadline = change.deadline
		change.timer.Active = change.active
	}
}

func (t *Timers) get(id ServiceID, owner OwnerID) (*Timer, error) {
	if err := t.registry.Validate(id, owner, KindTimer); err != nil {
		return nil, err
	}
	timer := t.timers[id]
	if timer == nil {
		return nil, fmt.Errorf("%w: timer %s", ErrInvalidState, id)
	}
	return timer, nil
}

func (t *Timers) Snapshot() TimersState {
	state := TimersState{MaxTimers: t.maxTimers}
	ids := make([]ServiceID, 0, len(t.timers))
	for id := range t.timers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		timer := t.timers[id]
		state.Timers = append(state.Timers, TimerState{
			ID:         timer.ID,
			Owner:      timer.Owner,
			Name:       timer.Name,
			DeadlineNS: int64(timer.Deadline),
			IntervalNS: int64(timer.Interval),
			Active:     timer.Active,
			Value:      timer.Value,
		})
	}
	return state
}

func (t *Timers) Restore(state TimersState) error {
	if state.MaxTimers == 0 || len(state.Timers) > int(state.MaxTimers) {
		return fmt.Errorf("%w: invalid timer state limits", ErrInvalidState)
	}
	timers := make(map[ServiceID]*Timer, len(state.Timers))
	var previous ServiceID
	for index, saved := range state.Timers {
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			strings.TrimSpace(saved.Name) == "" || len(saved.Name) > 255 ||
			strings.IndexByte(saved.Name, 0) >= 0 ||
			saved.DeadlineNS < 0 || saved.IntervalNS < 0 ||
			t.registry.Validate(saved.ID, saved.Owner, KindTimer) != nil {
			return fmt.Errorf("%w: invalid timer %d", ErrInvalidState, index)
		}
		timers[saved.ID] = &Timer{
			ID:       saved.ID,
			Owner:    saved.Owner,
			Name:     saved.Name,
			Deadline: time.Duration(saved.DeadlineNS),
			Interval: time.Duration(saved.IntervalNS),
			Active:   saved.Active,
			Value:    saved.Value,
		}
		previous = saved.ID
	}
	t.maxTimers = state.MaxTimers
	t.timers = timers
	return nil
}
