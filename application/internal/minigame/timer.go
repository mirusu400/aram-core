package minigame

import (
	"context"
	"fmt"
)

func (r *Runtime) renderTimerFirstFrame(ctx context.Context) error {
	resolver, callback, err := r.createBootstrap()
	if err != nil {
		return err
	}
	run := func(event uint32, args [4]uint32) error {
		result, completed, err := r.runEventSlice(ctx, event, args, true, eadsEventInstructionLimit)
		if err != nil {
			return fmt.Errorf("initialize EADS event %#x: %w", event, err)
		}
		if !completed {
			return fmt.Errorf("initialize EADS event %#x exceeded instruction limit", event)
		}
		r.appendEventResult(result)
		r.stage++
		return nil
	}
	if err := run(BootstrapEvent, [4]uint32{BootstrapEvent, resolver, callback}); err != nil {
		return err
	}
	if err := run(SetupEvent, [4]uint32{SetupEvent}); err != nil {
		return err
	}
	for range 64 {
		selected := -1
		for index, timer := range r.eventTimers {
			if timer.enabled && (selected < 0 || int32(timer.deadline-r.eventTimers[selected].deadline) < 0) {
				selected = index
			}
		}
		if selected < 0 {
			return fmt.Errorf("EADS initialization did not schedule a display event")
		}
		timer := &r.eventTimers[selected]
		if int32(timer.deadline-r.clockMS) > 0 {
			r.clockMS = timer.deadline
		}
		timer.enabled = false
		event := StartEvent + uint32(selected)
		if err := run(event, [4]uint32{event}); err != nil {
			return err
		}
		if r.presentCount > 0 {
			r.stage = 5
			r.Stats.PresentCount = r.presentCount
			r.Stats.TickMS = r.tickMS
			return nil
		}
	}
	return fmt.Errorf("EADS initialization exceeded timer event limit")
}
