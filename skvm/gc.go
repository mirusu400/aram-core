package skvm

import (
	"fmt"
	"reflect"

	shared "github.com/mirusu400/aram-core/runtime"
)

// collectGarbage reclaims guest objects that are no longer reachable from
// Java-visible VM roots. Native payloads which contain guest references are
// traversed explicitly because those edges are not represented by Object.Fields.
func (vm *VM) collectGarbage() error {
	reachable := make(map[uint32]struct{}, len(vm.heap))
	pending := make([]uint32, 0, len(vm.heap))
	surfaceImages := make(map[shared.ServiceID][]uint32)
	fileReferences := make(map[*xFileState]uint32)
	nativeAliases := make(map[any][]uint32)

	for reference, object := range vm.heap {
		value := reflect.ValueOf(object.Native)
		if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() {
			nativeAliases[object.Native] = append(
				nativeAliases[object.Native],
				reference,
			)
		}
		switch state := object.Native.(type) {
		case *imageState:
			if state.surface != 0 {
				surfaceImages[state.surface] = append(
					surfaceImages[state.surface],
					reference,
				)
			}
		case *xFileState:
			fileReferences[state] = reference
		}
	}

	markReference := func(reference uint32) {
		if reference == 0 {
			return
		}
		if _, exists := vm.heap[reference]; !exists {
			return
		}
		if _, marked := reachable[reference]; marked {
			return
		}
		reachable[reference] = struct{}{}
		pending = append(pending, reference)
	}
	markValue := func(value Value) {
		if value.Kind == ValueReference {
			markReference(uint32(value.bits))
		}
	}

	for _, current := range vm.classes {
		for _, value := range current.static {
			markValue(value)
		}
	}
	for _, value := range vm.hostStatic {
		markValue(value)
	}
	for _, current := range vm.frames {
		for _, value := range current.locals {
			markValue(value)
		}
		for _, value := range current.stack {
			markValue(value)
		}
	}
	for _, reference := range []uint32{
		vm.displayReference,
		vm.currentDisplay,
		vm.screenGraphics,
		vm.runningThread,
	} {
		markReference(reference)
	}

	// The shared scheduler and timer queue retain these Java objects even when
	// application code drops its last explicit reference.
	for reference, object := range vm.heap {
		switch state := object.Native.(type) {
		case *threadState:
			if state.active {
				markReference(reference)
			}
		case *timerTaskState:
			if state.timer != 0 && !state.cancelled {
				markReference(reference)
			}
		}
	}

	// A Runnable handed to Display.callSerially (and a Timer callback) lives only
	// as a reference inside the shared event bus until Advance dispatches its
	// run(). The guest may drop its own reference the instant it schedules the
	// work, so without rooting the pending events here a mid-flight System.gc
	// would reclaim the Runnable and the later dispatch would fault on a stale
	// receiver.
	for _, event := range vm.services.Events.Snapshot().Events {
		if event.Owner != vm.serviceOwner ||
			event.Value <= 0 || event.Value > int64(^uint32(0)) {
			continue
		}
		switch {
		case event.Kind == shared.EventApplication &&
			event.Name == callSeriallyEventName:
			markReference(uint32(event.Value))
		case event.Kind == shared.EventTimer:
			markReference(uint32(event.Value))
		}
	}

	for len(pending) != 0 {
		reference := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		object := vm.heap[reference]
		if object == nil {
			continue
		}
		nativeValue := reflect.ValueOf(object.Native)
		if nativeValue.IsValid() && nativeValue.Kind() == reflect.Pointer &&
			!nativeValue.IsNil() {
			for _, alias := range nativeAliases[object.Native] {
				markReference(alias)
			}
		}
		for _, value := range object.Fields {
			markValue(value)
		}
		if object.Array != nil {
			for _, value := range object.Array.Elements {
				markValue(value)
			}
		}
		switch state := object.Native.(type) {
		case *threadState:
			markReference(state.target)
			for _, current := range state.continuation {
				for _, value := range current.locals {
					markValue(value)
				}
				for _, value := range current.stack {
					markValue(value)
				}
			}
		case *inputStreamState:
			markReference(state.connection)
		case *inputStreamReaderState:
			markReference(state.stream)
		case *dataInputState:
			markReference(state.stream)
		case *dataOutputState:
			markReference(state.stream)
		case *vectorState:
			for _, value := range state.values {
				markReference(value)
			}
		case *hashtableState:
			for _, value := range state.values {
				markReference(value)
			}
			for _, key := range state.keys {
				markReference(key)
			}
		case *outputStreamState:
			markReference(fileReferences[state.file])
			markReference(state.connection)
		case *graphicsState:
			// Image.getGraphics and Graphics2D aliases retain the image surface
			// even though their native link is a service ID rather than a Java
			// field.
			for _, image := range surfaceImages[state.surface] {
				markReference(image)
			}
		}
	}

	for reference, object := range vm.heap {
		if _, marked := reachable[reference]; marked {
			continue
		}
		if state, ok := object.Native.(*imageState); ok {
			if err := vm.releaseImage(state); err != nil {
				return fmt.Errorf("collect SKVM image %d: %w", reference, err)
			}
		}
		if state, ok := object.Native.(*socketConnectionState); ok &&
			!state.closed && state.socket != 0 {
			if err := vm.services.Network.CloseSocket(
				vm.serviceOwner,
				state.socket,
				vm.services.Events,
			); err != nil {
				return fmt.Errorf("collect SKVM socket %d: %w", reference, err)
			}
			state.closed = true
			state.socket = 0
		}
		if state, ok := object.Native.(*httpConnectionState); ok &&
			!state.closed && state.request != 0 {
			if err := vm.services.Network.CloseHTTP(
				vm.serviceOwner,
				state.request,
				vm.services.Events,
			); err != nil {
				return fmt.Errorf("collect SKVM HTTP connection %d: %w", reference, err)
			}
			state.closed = true
			state.request = 0
		}
		delete(vm.heap, reference)
	}
	return nil
}

func (vm *VM) releaseImage(state *imageState) error {
	if state == nil {
		return nil
	}
	if state.asset != 0 {
		if err := vm.services.Assets.Release(
			vm.serviceOwner,
			state.asset,
		); err != nil {
			return err
		}
		state.asset = 0
		state.surface = 0
		return nil
	}
	if state.surface == 0 || state.surface == vm.screenSurface {
		return nil
	}
	if err := vm.services.Graphics.DestroySurface(
		vm.serviceOwner,
		state.surface,
	); err != nil {
		return err
	}
	state.surface = 0
	return nil
}
