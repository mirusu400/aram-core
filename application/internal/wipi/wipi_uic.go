package wipi

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
)

func (r *Runtime) dispatchUIC(name string) (guest.WIPIReturn, bool, error) {
	count, ok := uicArgumentCount(name)
	if !ok {
		return guest.WIPIReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	arg := func(index int) uint32 {
		if index >= len(args) {
			return 0
		}
		return args[index]
	}
	component := func() *Component {
		return r.UicComponents[arg(0)]
	}
	switch name {
	case "MC_uicCreateApplicationContext":
		handle, err := r.Heap.Allocate(64, true)
		if handle != 0 {
			r.UicContexts[handle] = true
		}
		return guest.WIPIReturn{Low: handle}, true, err
	case "MC_uicGetClass":
		className, err := r.ReadCString(arg(0))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		key := string(className)
		if handle := r.UicClasses[key]; handle != 0 {
			return guest.WIPIReturn{Low: handle}, true, nil
		}
		handle, err := r.Heap.Allocate(16, true)
		if err != nil || handle == 0 {
			return guest.WIPIReturn{}, true, err
		}
		r.UicClasses[key] = handle
		r.UicClassNames[handle] = key
		return guest.WIPIReturn{Low: handle}, true, nil
	case "MC_uicCreate":
		if !r.UicContexts[arg(0)] {
			return guest.WIPIReturn{}, true, nil
		}
		className, ok := r.UicClassNames[arg(1)]
		if !ok {
			return guest.WIPIReturn{}, true, nil
		}
		handle, err := r.Heap.Allocate(128, true)
		if err != nil || handle == 0 {
			return guest.WIPIReturn{}, true, err
		}
		r.UicComponents[handle] = &Component{
			Handle:     handle,
			ClassName:  className,
			Enabled:    true,
			Callbacks:  make(map[int32]UICallback),
			ActiveMenu: -1,
			ActiveList: -1,
			MaxText:    256,
		}
		return guest.WIPIReturn{Low: handle}, true, nil
	case "MC_uicDestroy":
		if _, ok := r.UicComponents[arg(0)]; ok {
			delete(r.UicComponents, arg(0))
			r.Heap.Release(arg(0))
		}
		if r.UicContexts[arg(0)] {
			delete(r.UicContexts, arg(0))
			r.Heap.Release(arg(0))
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicRepaint":
		current := component()
		if current == nil {
			return guest.WIPIReturn{}, true, nil
		}
		width := int32(arg(3))
		height := int32(arg(4))
		if width == -1 {
			width = current.Width - int32(arg(1))
		}
		if height == -1 {
			height = current.Height - int32(arg(2))
		}
		if len(r.UicRepaints) == wipiMaxUICRepaints {
			copy(r.UicRepaints, r.UicRepaints[1:])
			r.UicRepaints = r.UicRepaints[:len(r.UicRepaints)-1]
		}
		r.UicRepaints = append(r.UicRepaints, UICRepaint{
			Component: current.Handle,
			X:         int32(arg(1)),
			Y:         int32(arg(2)),
			Width:     width,
			Height:    height,
		})
		// A headless machine is the UIC host. Schedule the component's paint
		// callback just as a device UI loop would after accepting the damage
		// request; a null graphics context selects the public default context.
		r.queueUICCallback(current, 2, 0)
		return guest.WIPIReturn{}, true, nil
	case "MC_uicPaint":
		current := component()
		if current != nil {
			r.queueUICCallback(current, 2, arg(1))
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicHandleEvent":
		current := component()
		if current == nil || !current.Enabled || current.ClassName == "Label" {
			return guest.WIPIReturn{}, true, nil
		}
		if current.eventHandler != 0 {
			handled, err := r.CallGuestFunction(
				current.eventHandler,
				current.Handle,
				arg(1),
				arg(2),
				arg(3),
			)
			if err != nil {
				return guest.WIPIReturn{}, true, err
			}
			if handled == 0 {
				return guest.WIPIReturn{}, true, nil
			}
		}
		if callback := current.Callbacks[5]; callback.procedure != 0 {
			eventType, err := r.Heap.Allocate(4, true)
			if err != nil || eventType == 0 {
				return guest.WIPIReturn{}, true, err
			}
			if err := r.WriteU32(eventType, arg(1)); err != nil {
				r.Heap.Release(eventType)
				return guest.WIPIReturn{}, true, err
			}
			_, err = r.CallGuestFunction(
				callback.procedure,
				current.Handle,
				eventType,
				callback.client,
			)
			r.Heap.Release(eventType)
			if err != nil {
				return guest.WIPIReturn{}, true, err
			}
		}
		return guest.WIPIReturn{Low: 1}, true, nil
	case "MC_uicGetClassName":
		current := component()
		if current == nil {
			return guest.WIPIReturn{}, true, nil
		}
		return r.allocateUICString([]byte(current.ClassName))
	case "MC_uicIsInstance":
		current := component()
		if current == nil {
			return guest.WIPIReturn{}, true, nil
		}
		className, err := r.ReadCString(arg(1))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		if current.ClassName == string(className) {
			return guest.WIPIReturn{Low: 1}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicConfigure":
		current := component()
		if current != nil {
			mask := arg(5)
			if mask&1 != 0 {
				current.x = int32(arg(1))
				current.y = int32(arg(2))
			}
			if mask&2 != 0 && int32(arg(3)) > 0 && int32(arg(4)) > 0 {
				current.Width = int32(arg(3))
				current.Height = int32(arg(4))
			}
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicGetGeometry":
		current := component()
		if current == nil {
			return guest.WIPIReturn{}, true, nil
		}
		for index, value := range []int32{current.x, current.y, current.Width, current.Height} {
			if arg(index+1) != 0 {
				if err := r.WriteU32(arg(index+1), uint32(value)); err != nil {
					return guest.WIPIReturn{}, true, err
				}
			}
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicSetEnable":
		if current := component(); current != nil {
			current.Enabled = arg(1) != 0
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicSetCallback":
		current := component()
		if current == nil {
			return guest.WIPIReturn{}, true, nil
		}
		index := int32(arg(1))
		if index <= 0 || index >= 6 {
			return guest.WIPIReturn{}, true, nil
		}
		previous := current.Callbacks[index]
		current.Callbacks[index] = UICallback{procedure: arg(2), client: arg(3)}
		return guest.WIPIReturn{Low: previous.procedure}, true, nil
	case "MC_uicSetEventHandler":
		current := component()
		if current == nil {
			return guest.WIPIReturn{}, true, nil
		}
		previous := current.eventHandler
		current.eventHandler = arg(1)
		return guest.WIPIReturn{Low: previous}, true, nil
	case "MC_uicSetFont":
		if current := component(); current != nil {
			current.font = int32(arg(1))
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_uicGetFont":
		if current := component(); current != nil {
			return guest.WIPIReturn{Low: uint32(current.font)}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_uicSetFgColor":
		if current := component(); current != nil {
			current.foreground = arg(1)
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicSetBgColor":
		if current := component(); current != nil {
			current.background = arg(1)
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicSetLabel":
		current := component()
		if current == nil {
			return guest.WIPIReturn{}, true, nil
		}
		label, err := r.ReadCString(arg(1))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		current.Label = append(current.Label[:0], label...)
		r.queueUICCallback(current, 4, arg(1))
		return guest.WIPIReturn{}, true, nil
	case "MC_uicGetLabel":
		if current := component(); current != nil {
			return r.allocateUICString(current.Label)
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicSetlabelAlignment":
		if current := component(); current != nil {
			current.alignment = int32(arg(1))
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_uicSetTimeMask":
		if current := component(); current != nil {
			current.timeMask = int32(arg(1))
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_uicSetTime":
		current := component()
		if current != nil && arg(1) != 0 {
			if err := r.CPU.ReadMemory(arg(1), current.timeData[:]); err != nil {
				return guest.WIPIReturn{}, true, err
			}
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicSetTimeLong":
		current := component()
		if current != nil {
			seconds := uint64(arg(2)) | uint64(arg(3))<<32
			fields, err := r.timeFields(seconds)
			if err != nil {
				return guest.WIPIReturn{}, true, err
			}
			current.timeData = fields
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicGetTime":
		current := component()
		if current != nil && arg(1) != 0 {
			return guest.WIPIReturn{}, true, r.CPU.WriteMemory(arg(1), current.timeData[:])
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicAddMenuItem":
		return r.addUICItem(component(), arg(1), arg(2), false)
	case "MC_uicGetMenuItem":
		return r.getUICItem(component(), int32(arg(1)), arg(2), int32(arg(3)), arg(4), false)
	case "MC_uicRemoveMenuItem":
		return r.removeUICItem(component(), int32(arg(1)), false)
	case "MC_uicSetActiveMenuItem":
		current := component()
		if current == nil || !validUICIndex(current.menuItems, int32(arg(1))) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		current.ActiveMenu = int32(arg(1))
		return guest.WIPIReturn{}, true, nil
	case "MC_uicGetActiveMenuItem":
		if current := component(); current != nil {
			return guest.WIPIReturn{Low: uint32(current.ActiveMenu)}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_uicInsertText":
		return r.insertUICText(component(), int32(arg(1)), arg(2), int32(arg(3)))
	case "MC_uicDeleteText":
		current := component()
		if current != nil {
			start := guest.Clamp(int(int32(arg(1))), 0, len(current.text))
			count := max(0, int(int32(arg(2))))
			end := min(len(current.text), start+count)
			current.text = append(current.text[:start], current.text[end:]...)
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicGetMaxTextSize":
		if current := component(); current != nil {
			return guest.WIPIReturn{Low: uint32(current.MaxText)}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_uicSetMaxTextSize":
		current := component()
		maximum := int32(arg(1))
		if current == nil || maximum < 0 || maximum > int32(maxWIPIString) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		current.MaxText = maximum
		if len(current.text) > int(maximum) {
			current.text = current.text[:maximum]
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_uicGetTextSize":
		if current := component(); current != nil {
			return guest.WIPIReturn{Low: uint32(len(current.text))}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_uicGetText":
		current := component()
		start, length := int(int32(arg(1))), int(int32(arg(3)))
		if current == nil || start < 0 || length < 0 || start > len(current.text) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		data := current.text[start:min(len(current.text), start+length)]
		if err := r.CPU.WriteMemory(arg(2), data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{Low: uint32(len(data))}, true, nil
	case "MC_uicAddListItem":
		return r.addUICItem(component(), arg(1), arg(2), true)
	case "MC_uicGetListItem":
		return r.getUICItem(component(), int32(arg(1)), arg(2), int32(arg(3)), arg(4), true)
	case "MC_uicRemoveListItem":
		return r.removeUICItem(component(), int32(arg(1)), true)
	case "MC_uicSetActiveListItem":
		current := component()
		if current == nil || !validUICIndex(current.listItems, int32(arg(1))) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		current.ActiveList = int32(arg(1))
		return guest.WIPIReturn{}, true, nil
	case "MC_uicGetActiveListItem":
		if current := component(); current != nil {
			return guest.WIPIReturn{Low: uint32(current.ActiveList)}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	default:
		return guest.WIPIReturn{}, false, nil
	}
}

func uicArgumentCount(name string) (int, bool) {
	switch name {
	case "MC_uicCreateApplicationContext":
		return 0, true
	case "MC_uicGetClass", "MC_uicDestroy", "MC_uicGetClassName",
		"MC_uicGetFont", "MC_uicGetLabel", "MC_uicGetActiveMenuItem",
		"MC_uicGetMaxTextSize", "MC_uicGetTextSize", "MC_uicGetActiveListItem":
		return 1, true
	case "MC_uicCreate", "MC_uicPaint", "MC_uicIsInstance", "MC_uicSetEnable",
		"MC_uicSetEventHandler", "MC_uicSetFont", "MC_uicSetFgColor",
		"MC_uicSetBgColor", "MC_uicSetLabel", "MC_uicSetlabelAlignment",
		"MC_uicSetTimeMask", "MC_uicSetTime", "MC_uicGetTime",
		"MC_uicRemoveMenuItem", "MC_uicSetActiveMenuItem",
		"MC_uicSetMaxTextSize",
		"MC_uicRemoveListItem", "MC_uicSetActiveListItem":
		return 2, true
	case "MC_uicAddMenuItem", "MC_uicDeleteText", "MC_uicAddListItem":
		return 3, true
	case "MC_uicHandleEvent", "MC_uicSetCallback", "MC_uicSetTimeLong",
		"MC_uicInsertText", "MC_uicGetText":
		return 4, true
	case "MC_uicRepaint", "MC_uicGetGeometry", "MC_uicGetMenuItem", "MC_uicGetListItem":
		return 5, true
	case "MC_uicConfigure":
		return 6, true
	default:
		return 0, false
	}
}

func (r *Runtime) allocateUICString(value []byte) (guest.WIPIReturn, bool, error) {
	address, err := r.Heap.Allocate(uint32(len(value)+1), true)
	if err != nil || address == 0 {
		return guest.WIPIReturn{}, true, err
	}
	_, err = r.writeCString(address, value, -1)
	return guest.WIPIReturn{Low: address}, true, err
}

func (r *Runtime) queueUICCallback(
	component *Component,
	index int32,
	serverData uint32,
) {
	if component == nil {
		return
	}
	callback := component.Callbacks[index]
	r.EnqueueCallback(
		callback.procedure,
		component.Handle,
		serverData,
		callback.client,
	)
}

func (r *Runtime) addUICItem(component *Component, labelAddress, image uint32, list bool) (guest.WIPIReturn, bool, error) {
	if component == nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	label, err := r.ReadCString(labelAddress)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	item := wipiUIItem{Label: append([]byte(nil), label...), image: image}
	if list {
		component.listItems = append(component.listItems, item)
		return guest.WIPIReturn{Low: uint32(len(component.listItems) - 1)}, true, nil
	}
	component.menuItems = append(component.menuItems, item)
	return guest.WIPIReturn{Low: uint32(len(component.menuItems) - 1)}, true, nil
}

func (r *Runtime) getUICItem(
	component *Component,
	index int32,
	output uint32,
	bufferLength int32,
	imagePointer uint32,
	list bool,
) (guest.WIPIReturn, bool, error) {
	if component == nil || bufferLength <= 0 {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	items := component.menuItems
	if list {
		items = component.listItems
	}
	if !validUICIndex(items, index) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	item := items[index]
	if _, err := r.writeCString(output, item.Label, bufferLength); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	if imagePointer != 0 {
		if err := r.WriteU32(imagePointer, item.image); err != nil {
			return guest.WIPIReturn{}, true, err
		}
	}
	return guest.WIPIReturn{}, true, nil
}

func (r *Runtime) removeUICItem(component *Component, index int32, list bool) (guest.WIPIReturn, bool, error) {
	if component == nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	items := component.menuItems
	if list {
		items = component.listItems
	}
	if !validUICIndex(items, index) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	items = append(items[:index], items[index+1:]...)
	if list {
		component.listItems = items
		if component.ActiveList >= int32(len(items)) {
			component.ActiveList = int32(len(items)) - 1
		}
	} else {
		component.menuItems = items
		if component.ActiveMenu >= int32(len(items)) {
			component.ActiveMenu = int32(len(items)) - 1
		}
	}
	return guest.WIPIReturn{}, true, nil
}

func (r *Runtime) insertUICText(component *Component, index int32, source uint32, length int32) (guest.WIPIReturn, bool, error) {
	if component == nil || index < 0 || length < 0 ||
		index > int32(len(component.text)) ||
		length > component.MaxText-int32(len(component.text)) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	data := make([]byte, length)
	if err := r.CPU.ReadMemory(source, data); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	position := int(index)
	component.text = append(component.text, make([]byte, len(data))...)
	copy(component.text[position+len(data):], component.text[position:len(component.text)-len(data)])
	copy(component.text[position:], data)
	return guest.WIPIReturn{Low: uint32(len(data))}, true, nil
}

func validUICIndex(items []wipiUIItem, index int32) bool {
	return index >= 0 && index < int32(len(items))
}
