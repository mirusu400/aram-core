package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type LifecycleState uint8

const (
	LifecycleLoaded LifecycleState = iota + 1
	LifecycleReady
	LifecycleRunning
	LifecyclePaused
	LifecycleBackground
	LifecycleStopped
	LifecycleFaulted
	LifecycleDestroyed
)

func (s LifecycleState) Valid() bool {
	return s >= LifecycleLoaded && s <= LifecycleDestroyed
}

type CoordinatorLimits struct {
	MaxAdapters      uint32
	DefaultRunBudget uint64
	MaxRunBudget     uint64
	MaxCallbacks     uint32
}

func DefaultCoordinatorLimits() CoordinatorLimits {
	return CoordinatorLimits{
		MaxAdapters: 16, DefaultRunBudget: 100_000,
		MaxRunBudget: 256_000_000, MaxCallbacks: 1024,
	}
}

func (l CoordinatorLimits) Validate() error {
	if l.MaxAdapters == 0 || l.DefaultRunBudget == 0 ||
		l.MaxRunBudget < l.DefaultRunBudget || l.MaxCallbacks == 0 {
		return fmt.Errorf("%w: invalid coordinator limits", ErrInvalidArgument)
	}
	return nil
}

type AdapterState struct {
	Owner         OwnerID
	Name          string
	Lifecycle     LifecycleState
	RunBudget     uint64
	BudgetUsed    uint64
	CallbackCount uint32
	LastSequence  uint64
	Fault         string
}

type CoordinatorState struct {
	Limits            CoordinatorLimits
	NextOwner         OwnerID
	QuantumSequence   uint64
	ForegroundOwner   OwnerID
	PresentationOwner OwnerID
	SchedulerCursor   OwnerID
	Adapters          []AdapterState
}

type adapterRuntime struct {
	AdapterState
}

// Coordinator owns lifecycle, execution budgets, foreground selection, and
// presentation ownership while continuations remain adapter-specific.
type Coordinator struct {
	limits            CoordinatorLimits
	nextOwner         OwnerID
	quantumSequence   uint64
	foregroundOwner   OwnerID
	presentationOwner OwnerID
	schedulerCursor   OwnerID
	adapters          map[OwnerID]*adapterRuntime
}

func NewCoordinator(limits CoordinatorLimits) (*Coordinator, error) {
	if limits == (CoordinatorLimits{}) {
		limits = DefaultCoordinatorLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Coordinator{
		limits: limits, nextOwner: 1,
		adapters: make(map[OwnerID]*adapterRuntime),
	}, nil
}

func (c *Coordinator) Register(name string, budget uint64) (OwnerID, error) {
	if strings.TrimSpace(name) == "" || len(name) > 127 ||
		strings.IndexByte(name, 0) >= 0 {
		return 0, fmt.Errorf("%w: invalid adapter name", ErrInvalidArgument)
	}
	if uint32(len(c.adapters)) >= c.limits.MaxAdapters ||
		c.nextOwner == 0 || c.nextOwner == OwnerID(math.MaxUint32) {
		return 0, fmt.Errorf("%w: adapter owner IDs exhausted", ErrLimitExceeded)
	}
	if budget == 0 {
		budget = c.limits.DefaultRunBudget
	}
	if budget > c.limits.MaxRunBudget {
		return 0, fmt.Errorf("%w: adapter budget %d", ErrLimitExceeded, budget)
	}
	owner := c.nextOwner
	c.nextOwner++
	c.adapters[owner] = &adapterRuntime{AdapterState: AdapterState{
		Owner: owner, Name: name, Lifecycle: LifecycleLoaded, RunBudget: budget,
	}}
	if c.foregroundOwner == 0 {
		c.foregroundOwner = owner
	}
	if c.presentationOwner == 0 {
		c.presentationOwner = owner
	}
	return owner, nil
}

func (c *Coordinator) Unregister(owner OwnerID) error {
	adapter, err := c.adapter(owner)
	if err != nil {
		return err
	}
	if adapter.Lifecycle != LifecycleDestroyed {
		return fmt.Errorf("%w: adapter %d is not destroyed", ErrInvalidState, owner)
	}
	delete(c.adapters, owner)
	if c.foregroundOwner == owner {
		c.foregroundOwner = c.firstOwner()
	}
	if c.presentationOwner == owner {
		c.presentationOwner = c.foregroundOwner
	}
	if c.schedulerCursor == owner {
		c.schedulerCursor = 0
	}
	return nil
}

func (c *Coordinator) Transition(
	owner OwnerID,
	next LifecycleState,
	at time.Duration,
	bus *EventBus,
) error {
	adapter, err := c.adapter(owner)
	if err != nil {
		return err
	}
	if !next.Valid() || at < 0 || !validLifecycleTransition(adapter.Lifecycle, next) {
		return fmt.Errorf(
			"%w: lifecycle transition %d -> %d",
			ErrInvalidState,
			adapter.Lifecycle,
			next,
		)
	}
	previous, previousFault := adapter.Lifecycle, adapter.Fault
	adapter.Lifecycle = next
	adapter.Fault = ""
	if bus != nil {
		sequence, enqueueErr := bus.Enqueue(Event{
			At: at, Kind: EventLifecycle, Owner: owner,
			Name: lifecycleName(next), Value: int64(next),
		})
		if enqueueErr != nil {
			adapter.Lifecycle = previous
			adapter.Fault = previousFault
			return enqueueErr
		}
		adapter.LastSequence = sequence
	}
	return nil
}

func (c *Coordinator) Fault(
	owner OwnerID,
	message string,
	at time.Duration,
	bus *EventBus,
) error {
	adapter, err := c.adapter(owner)
	if err != nil {
		return err
	}
	if adapter.Lifecycle == LifecycleDestroyed || len(message) > 4096 || at < 0 {
		return fmt.Errorf("%w: invalid adapter fault", ErrInvalidState)
	}
	if strings.IndexByte(message, 0) >= 0 {
		return fmt.Errorf("%w: invalid adapter fault", ErrInvalidState)
	}
	previousState, previousFault := adapter.Lifecycle, adapter.Fault
	adapter.Lifecycle, adapter.Fault = LifecycleFaulted, message
	if bus != nil {
		sequence, enqueueErr := bus.Enqueue(Event{
			At: at, Kind: EventLifecycle, Owner: owner,
			Name: "faulted", Value: int64(LifecycleFaulted),
		})
		if enqueueErr != nil {
			adapter.Lifecycle, adapter.Fault = previousState, previousFault
			return enqueueErr
		}
		adapter.LastSequence = sequence
	}
	return nil
}

func (c *Coordinator) SetForeground(owner OwnerID, foreground bool) error {
	adapter, err := c.adapter(owner)
	if err != nil {
		return err
	}
	if adapter.Lifecycle == LifecycleDestroyed ||
		adapter.Lifecycle == LifecycleFaulted {
		return fmt.Errorf("%w: adapter cannot change foreground", ErrInvalidState)
	}
	if foreground {
		if previous := c.adapters[c.foregroundOwner]; previous != nil &&
			previous.Owner != owner && previous.Lifecycle == LifecycleRunning {
			previous.Lifecycle = LifecycleBackground
		}
		c.foregroundOwner = owner
		if adapter.Lifecycle == LifecycleBackground {
			adapter.Lifecycle = LifecyclePaused
		}
	} else if c.foregroundOwner == owner {
		c.foregroundOwner = 0
		if adapter.Lifecycle == LifecycleRunning ||
			adapter.Lifecycle == LifecyclePaused {
			adapter.Lifecycle = LifecycleBackground
		}
	}
	return nil
}

func (c *Coordinator) SetPresentationOwner(owner OwnerID) error {
	if owner == 0 {
		c.presentationOwner = 0
		return nil
	}
	adapter, err := c.adapter(owner)
	if err != nil {
		return err
	}
	if adapter.Lifecycle == LifecycleDestroyed ||
		adapter.Lifecycle == LifecycleFaulted {
		return fmt.Errorf("%w: invalid presentation owner", ErrInvalidState)
	}
	c.presentationOwner = owner
	return nil
}

func (c *Coordinator) ForegroundOwner() OwnerID {
	return c.foregroundOwner
}

func (c *Coordinator) PresentationOwner() OwnerID {
	return c.presentationOwner
}

func (c *Coordinator) BeginQuantum() (uint64, error) {
	if c.quantumSequence == math.MaxUint64 {
		return 0, fmt.Errorf("%w: coordinator quantum exhausted", ErrLimitExceeded)
	}
	c.quantumSequence++
	for _, adapter := range c.adapters {
		adapter.BudgetUsed = 0
		adapter.CallbackCount = 0
	}
	return c.quantumSequence, nil
}

func (c *Coordinator) Consume(owner OwnerID, instructions uint64) error {
	adapter, err := c.adapter(owner)
	if err != nil {
		return err
	}
	if adapter.Lifecycle != LifecycleRunning {
		return fmt.Errorf("%w: adapter %d is not running", ErrInvalidState, owner)
	}
	if instructions > adapter.RunBudget-adapter.BudgetUsed {
		return fmt.Errorf("%w: adapter %d execution budget", ErrLimitExceeded, owner)
	}
	adapter.BudgetUsed += instructions
	return nil
}

func (c *Coordinator) EnterCallback(owner OwnerID) error {
	adapter, err := c.adapter(owner)
	if err != nil {
		return err
	}
	if adapter.CallbackCount >= c.limits.MaxCallbacks {
		return fmt.Errorf("%w: adapter callback budget", ErrLimitExceeded)
	}
	adapter.CallbackCount++
	return nil
}

func (c *Coordinator) NextRunnable() (OwnerID, bool) {
	owners := c.sortedOwners()
	if len(owners) == 0 {
		return 0, false
	}
	start := sort.Search(len(owners), func(index int) bool {
		return owners[index] > c.schedulerCursor
	})
	for offset := 0; offset < len(owners); offset++ {
		owner := owners[(start+offset)%len(owners)]
		adapter := c.adapters[owner]
		if adapter.Lifecycle == LifecycleRunning &&
			adapter.BudgetUsed < adapter.RunBudget {
			c.schedulerCursor = owner
			return owner, true
		}
	}
	return 0, false
}

func (c *Coordinator) Adapter(owner OwnerID) (AdapterState, error) {
	adapter, err := c.adapter(owner)
	if err != nil {
		return AdapterState{}, err
	}
	return adapter.AdapterState, nil
}

func (c *Coordinator) Snapshot() CoordinatorState {
	state := CoordinatorState{
		Limits: c.limits, NextOwner: c.nextOwner,
		QuantumSequence:   c.quantumSequence,
		ForegroundOwner:   c.foregroundOwner,
		PresentationOwner: c.presentationOwner,
		SchedulerCursor:   c.schedulerCursor,
	}
	for _, owner := range c.sortedOwners() {
		state.Adapters = append(state.Adapters, c.adapters[owner].AdapterState)
	}
	return state
}

func (c *Coordinator) Restore(state CoordinatorState) error {
	if err := state.Limits.Validate(); err != nil || state.NextOwner == 0 ||
		len(state.Adapters) > int(state.Limits.MaxAdapters) {
		return fmt.Errorf("%w: invalid coordinator state", ErrInvalidState)
	}
	adapters := make(map[OwnerID]*adapterRuntime, len(state.Adapters))
	var previous OwnerID
	for index, saved := range state.Adapters {
		if saved.Owner == 0 || saved.Owner >= state.NextOwner ||
			(index != 0 && saved.Owner <= previous) ||
			strings.TrimSpace(saved.Name) == "" || len(saved.Name) > 127 ||
			strings.IndexByte(saved.Name, 0) >= 0 ||
			!saved.Lifecycle.Valid() || saved.RunBudget == 0 ||
			saved.RunBudget > state.Limits.MaxRunBudget ||
			saved.BudgetUsed > saved.RunBudget ||
			saved.CallbackCount > state.Limits.MaxCallbacks ||
			len(saved.Fault) > 4096 ||
			strings.IndexByte(saved.Fault, 0) >= 0 ||
			(saved.Lifecycle != LifecycleFaulted && saved.Fault != "") {
			return fmt.Errorf("%w: invalid adapter state %d", ErrInvalidState, index)
		}
		adapters[saved.Owner] = &adapterRuntime{AdapterState: saved}
		previous = saved.Owner
	}
	for name, owner := range map[string]OwnerID{
		"foreground":   state.ForegroundOwner,
		"presentation": state.PresentationOwner,
		"scheduler":    state.SchedulerCursor,
	} {
		if owner != 0 && adapters[owner] == nil {
			return fmt.Errorf("%w: %s owner %d is missing", ErrInvalidState, name, owner)
		}
	}
	c.limits = state.Limits
	c.nextOwner = state.NextOwner
	c.quantumSequence = state.QuantumSequence
	c.foregroundOwner = state.ForegroundOwner
	c.presentationOwner = state.PresentationOwner
	c.schedulerCursor = state.SchedulerCursor
	c.adapters = adapters
	return nil
}

func (c *Coordinator) adapter(owner OwnerID) (*adapterRuntime, error) {
	if owner == 0 || c.adapters[owner] == nil {
		return nil, fmt.Errorf("%w: adapter owner %d", ErrNotFound, owner)
	}
	return c.adapters[owner], nil
}

func (c *Coordinator) sortedOwners() []OwnerID {
	owners := make([]OwnerID, 0, len(c.adapters))
	for owner := range c.adapters {
		owners = append(owners, owner)
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	return owners
}

func (c *Coordinator) firstOwner() OwnerID {
	owners := c.sortedOwners()
	if len(owners) == 0 {
		return 0
	}
	return owners[0]
}

func validLifecycleTransition(current, next LifecycleState) bool {
	if current == next {
		return true
	}
	switch current {
	case LifecycleLoaded:
		return next == LifecycleReady || next == LifecycleDestroyed ||
			next == LifecycleFaulted
	case LifecycleReady:
		return next == LifecycleRunning || next == LifecycleStopped ||
			next == LifecycleDestroyed || next == LifecycleFaulted
	case LifecycleRunning:
		return next == LifecyclePaused || next == LifecycleBackground ||
			next == LifecycleStopped || next == LifecycleFaulted
	case LifecyclePaused:
		return next == LifecycleRunning || next == LifecycleBackground ||
			next == LifecycleStopped || next == LifecycleDestroyed ||
			next == LifecycleFaulted
	case LifecycleBackground:
		return next == LifecycleRunning || next == LifecyclePaused ||
			next == LifecycleStopped || next == LifecycleDestroyed ||
			next == LifecycleFaulted
	case LifecycleStopped:
		return next == LifecycleReady || next == LifecycleDestroyed ||
			next == LifecycleFaulted
	case LifecycleFaulted:
		return next == LifecycleReady || next == LifecycleDestroyed
	case LifecycleDestroyed:
		return false
	default:
		return false
	}
}

func lifecycleName(state LifecycleState) string {
	switch state {
	case LifecycleLoaded:
		return "loaded"
	case LifecycleReady:
		return "ready"
	case LifecycleRunning:
		return "running"
	case LifecyclePaused:
		return "paused"
	case LifecycleBackground:
		return "background"
	case LifecycleStopped:
		return "stopped"
	case LifecycleFaulted:
		return "faulted"
	case LifecycleDestroyed:
		return "destroyed"
	default:
		return "invalid"
	}
}
