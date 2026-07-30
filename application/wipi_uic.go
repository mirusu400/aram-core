package application

func (r *wipiRuntime) dispatchUIC(name string) (wipiReturn, bool, error) {
	count, ok := uicArgumentCount(name)
	if !ok {
		return wipiReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return wipiReturn{}, true, err
	}
	arg := func(index int) uint32 {
		if index >= len(args) {
			return 0
		}
		return args[index]
	}
	component := func() *wipiComponent {
		return r.uicComponents[arg(0)]
	}
	switch name {
	case "MC_uicCreateApplicationContext":
		handle, err := r.heap.allocate(64, true)
		if handle != 0 {
			r.uicContexts[handle] = true
		}
		return wipiReturn{low: handle}, true, err
	case "MC_uicGetClass":
		className, err := r.readCString(arg(0))
		if err != nil {
			return wipiReturn{}, true, err
		}
		key := string(className)
		if handle := r.uicClasses[key]; handle != 0 {
			return wipiReturn{low: handle}, true, nil
		}
		handle, err := r.heap.allocate(16, true)
		if err != nil || handle == 0 {
			return wipiReturn{}, true, err
		}
		r.uicClasses[key] = handle
		r.uicClassNames[handle] = key
		return wipiReturn{low: handle}, true, nil
	case "MC_uicCreate":
		if !r.uicContexts[arg(0)] {
			return wipiReturn{}, true, nil
		}
		className, ok := r.uicClassNames[arg(1)]
		if !ok {
			return wipiReturn{}, true, nil
		}
		handle, err := r.heap.allocate(128, true)
		if err != nil || handle == 0 {
			return wipiReturn{}, true, err
		}
		r.uicComponents[handle] = &wipiComponent{
			handle:     handle,
			className:  className,
			enabled:    true,
			callbacks:  make(map[int32]wipiUICallback),
			activeMenu: -1,
			activeList: -1,
			maxText:    256,
		}
		return wipiReturn{low: handle}, true, nil
	case "MC_uicDestroy":
		if _, ok := r.uicComponents[arg(0)]; ok {
			delete(r.uicComponents, arg(0))
			r.heap.release(arg(0))
		}
		if r.uicContexts[arg(0)] {
			delete(r.uicContexts, arg(0))
			r.heap.release(arg(0))
		}
		return wipiReturn{}, true, nil
	case "MC_uicRepaint":
		current := component()
		if current == nil {
			return wipiReturn{}, true, nil
		}
		width := int32(arg(3))
		height := int32(arg(4))
		if width == -1 {
			width = current.width - int32(arg(1))
		}
		if height == -1 {
			height = current.height - int32(arg(2))
		}
		if len(r.uicRepaints) == wipiMaxUICRepaints {
			copy(r.uicRepaints, r.uicRepaints[1:])
			r.uicRepaints = r.uicRepaints[:len(r.uicRepaints)-1]
		}
		r.uicRepaints = append(r.uicRepaints, wipiUICRepaint{
			component: current.handle,
			x:         int32(arg(1)),
			y:         int32(arg(2)),
			width:     width,
			height:    height,
		})
		// A headless machine is the UIC host. Schedule the component's paint
		// callback just as a device UI loop would after accepting the damage
		// request; a null graphics context selects the public default context.
		r.queueUICCallback(current, 2, 0)
		return wipiReturn{}, true, nil
	case "MC_uicPaint":
		current := component()
		if current != nil {
			r.queueUICCallback(current, 2, arg(1))
		}
		return wipiReturn{}, true, nil
	case "MC_uicHandleEvent":
		current := component()
		if current == nil || !current.enabled || current.className == "Label" {
			return wipiReturn{}, true, nil
		}
		if current.eventHandler != 0 {
			handled, err := r.callGuestFunction(
				current.eventHandler,
				current.handle,
				arg(1),
				arg(2),
				arg(3),
			)
			if err != nil {
				return wipiReturn{}, true, err
			}
			if handled == 0 {
				return wipiReturn{}, true, nil
			}
		}
		if callback := current.callbacks[5]; callback.procedure != 0 {
			eventType, err := r.heap.allocate(4, true)
			if err != nil || eventType == 0 {
				return wipiReturn{}, true, err
			}
			if err := r.writeU32(eventType, arg(1)); err != nil {
				r.heap.release(eventType)
				return wipiReturn{}, true, err
			}
			_, err = r.callGuestFunction(
				callback.procedure,
				current.handle,
				eventType,
				callback.client,
			)
			r.heap.release(eventType)
			if err != nil {
				return wipiReturn{}, true, err
			}
		}
		return wipiReturn{low: 1}, true, nil
	case "MC_uicGetClassName":
		current := component()
		if current == nil {
			return wipiReturn{}, true, nil
		}
		return r.allocateUICString([]byte(current.className))
	case "MC_uicIsInstance":
		current := component()
		if current == nil {
			return wipiReturn{}, true, nil
		}
		className, err := r.readCString(arg(1))
		if err != nil {
			return wipiReturn{}, true, err
		}
		if current.className == string(className) {
			return wipiReturn{low: 1}, true, nil
		}
		return wipiReturn{}, true, nil
	case "MC_uicConfigure":
		current := component()
		if current != nil {
			mask := arg(5)
			if mask&1 != 0 {
				current.x = int32(arg(1))
				current.y = int32(arg(2))
			}
			if mask&2 != 0 && int32(arg(3)) > 0 && int32(arg(4)) > 0 {
				current.width = int32(arg(3))
				current.height = int32(arg(4))
			}
		}
		return wipiReturn{}, true, nil
	case "MC_uicGetGeometry":
		current := component()
		if current == nil {
			return wipiReturn{}, true, nil
		}
		for index, value := range []int32{current.x, current.y, current.width, current.height} {
			if arg(index+1) != 0 {
				if err := r.writeU32(arg(index+1), uint32(value)); err != nil {
					return wipiReturn{}, true, err
				}
			}
		}
		return wipiReturn{}, true, nil
	case "MC_uicSetEnable":
		if current := component(); current != nil {
			current.enabled = arg(1) != 0
		}
		return wipiReturn{}, true, nil
	case "MC_uicSetCallback":
		current := component()
		if current == nil {
			return wipiReturn{}, true, nil
		}
		index := int32(arg(1))
		if index <= 0 || index >= 6 {
			return wipiReturn{}, true, nil
		}
		previous := current.callbacks[index]
		current.callbacks[index] = wipiUICallback{procedure: arg(2), client: arg(3)}
		return wipiReturn{low: previous.procedure}, true, nil
	case "MC_uicSetEventHandler":
		current := component()
		if current == nil {
			return wipiReturn{}, true, nil
		}
		previous := current.eventHandler
		current.eventHandler = arg(1)
		return wipiReturn{low: previous}, true, nil
	case "MC_uicSetFont":
		if current := component(); current != nil {
			current.font = int32(arg(1))
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_uicGetFont":
		if current := component(); current != nil {
			return wipiReturn{low: uint32(current.font)}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_uicSetFgColor":
		if current := component(); current != nil {
			current.foreground = arg(1)
		}
		return wipiReturn{}, true, nil
	case "MC_uicSetBgColor":
		if current := component(); current != nil {
			current.background = arg(1)
		}
		return wipiReturn{}, true, nil
	case "MC_uicSetLabel":
		current := component()
		if current == nil {
			return wipiReturn{}, true, nil
		}
		label, err := r.readCString(arg(1))
		if err != nil {
			return wipiReturn{}, true, err
		}
		current.label = append(current.label[:0], label...)
		r.queueUICCallback(current, 4, arg(1))
		return wipiReturn{}, true, nil
	case "MC_uicGetLabel":
		if current := component(); current != nil {
			return r.allocateUICString(current.label)
		}
		return wipiReturn{}, true, nil
	case "MC_uicSetlabelAlignment":
		if current := component(); current != nil {
			current.alignment = int32(arg(1))
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_uicSetTimeMask":
		if current := component(); current != nil {
			current.timeMask = int32(arg(1))
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_uicSetTime":
		current := component()
		if current != nil && arg(1) != 0 {
			if err := r.cpu.ReadMemory(arg(1), current.timeData[:]); err != nil {
				return wipiReturn{}, true, err
			}
		}
		return wipiReturn{}, true, nil
	case "MC_uicSetTimeLong":
		current := component()
		if current != nil {
			seconds := uint64(arg(2)) | uint64(arg(3))<<32
			fields, err := r.timeFields(seconds)
			if err != nil {
				return wipiReturn{}, true, err
			}
			current.timeData = fields
		}
		return wipiReturn{}, true, nil
	case "MC_uicGetTime":
		current := component()
		if current != nil && arg(1) != 0 {
			return wipiReturn{}, true, r.cpu.WriteMemory(arg(1), current.timeData[:])
		}
		return wipiReturn{}, true, nil
	case "MC_uicAddMenuItem":
		return r.addUICItem(component(), arg(1), arg(2), false)
	case "MC_uicGetMenuItem":
		return r.getUICItem(component(), int32(arg(1)), arg(2), int32(arg(3)), arg(4), false)
	case "MC_uicRemoveMenuItem":
		return r.removeUICItem(component(), int32(arg(1)), false)
	case "MC_uicSetActiveMenuItem":
		current := component()
		if current == nil || !validUICIndex(current.menuItems, int32(arg(1))) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		current.activeMenu = int32(arg(1))
		return wipiReturn{}, true, nil
	case "MC_uicGetActiveMenuItem":
		if current := component(); current != nil {
			return wipiReturn{low: uint32(current.activeMenu)}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_uicInsertText":
		return r.insertUICText(component(), int32(arg(1)), arg(2), int32(arg(3)))
	case "MC_uicDeleteText":
		current := component()
		if current != nil {
			start := clamp(int(int32(arg(1))), 0, len(current.text))
			count := max(0, int(int32(arg(2))))
			end := min(len(current.text), start+count)
			current.text = append(current.text[:start], current.text[end:]...)
		}
		return wipiReturn{}, true, nil
	case "MC_uicGetMaxTextSize":
		if current := component(); current != nil {
			return wipiReturn{low: uint32(current.maxText)}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_uicSetMaxTextSize":
		current := component()
		maximum := int32(arg(1))
		if current == nil || maximum < 0 || maximum > int32(maxWIPIString) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		current.maxText = maximum
		if len(current.text) > int(maximum) {
			current.text = current.text[:maximum]
		}
		return wipiReturn{}, true, nil
	case "MC_uicGetTextSize":
		if current := component(); current != nil {
			return wipiReturn{low: uint32(len(current.text))}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_uicGetText":
		current := component()
		start, length := int(int32(arg(1))), int(int32(arg(3)))
		if current == nil || start < 0 || length < 0 || start > len(current.text) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		data := current.text[start:min(len(current.text), start+length)]
		if err := r.cpu.WriteMemory(arg(2), data); err != nil {
			return wipiReturn{}, true, err
		}
		return wipiReturn{low: uint32(len(data))}, true, nil
	case "MC_uicAddListItem":
		return r.addUICItem(component(), arg(1), arg(2), true)
	case "MC_uicGetListItem":
		return r.getUICItem(component(), int32(arg(1)), arg(2), int32(arg(3)), arg(4), true)
	case "MC_uicRemoveListItem":
		return r.removeUICItem(component(), int32(arg(1)), true)
	case "MC_uicSetActiveListItem":
		current := component()
		if current == nil || !validUICIndex(current.listItems, int32(arg(1))) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		current.activeList = int32(arg(1))
		return wipiReturn{}, true, nil
	case "MC_uicGetActiveListItem":
		if current := component(); current != nil {
			return wipiReturn{low: uint32(current.activeList)}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	default:
		return wipiReturn{}, false, nil
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

func (r *wipiRuntime) allocateUICString(value []byte) (wipiReturn, bool, error) {
	address, err := r.heap.allocate(uint32(len(value)+1), true)
	if err != nil || address == 0 {
		return wipiReturn{}, true, err
	}
	_, err = r.writeCString(address, value, -1)
	return wipiReturn{low: address}, true, err
}

func (r *wipiRuntime) queueUICCallback(
	component *wipiComponent,
	index int32,
	serverData uint32,
) {
	if component == nil {
		return
	}
	callback := component.callbacks[index]
	r.enqueueCallback(
		callback.procedure,
		component.handle,
		serverData,
		callback.client,
	)
}

func (r *wipiRuntime) timeFields(seconds uint64) ([36]byte, error) {
	temporary, err := r.heap.allocate(36, true)
	if err != nil || temporary == 0 {
		return [36]byte{}, err
	}
	if err := r.writeU64(temporary, seconds); err != nil {
		return [36]byte{}, err
	}
	result, _, err := r.breakDownTime(temporary)
	r.heap.release(temporary)
	if err != nil {
		return [36]byte{}, err
	}
	var fields [36]byte
	if err := r.cpu.ReadMemory(result.low, fields[:]); err != nil {
		return [36]byte{}, err
	}
	r.heap.release(result.low)
	return fields, nil
}

func (r *wipiRuntime) addUICItem(component *wipiComponent, labelAddress, image uint32, list bool) (wipiReturn, bool, error) {
	if component == nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	label, err := r.readCString(labelAddress)
	if err != nil {
		return wipiReturn{}, true, err
	}
	item := wipiUIItem{label: append([]byte(nil), label...), image: image}
	if list {
		component.listItems = append(component.listItems, item)
		return wipiReturn{low: uint32(len(component.listItems) - 1)}, true, nil
	}
	component.menuItems = append(component.menuItems, item)
	return wipiReturn{low: uint32(len(component.menuItems) - 1)}, true, nil
}

func (r *wipiRuntime) getUICItem(
	component *wipiComponent,
	index int32,
	output uint32,
	bufferLength int32,
	imagePointer uint32,
	list bool,
) (wipiReturn, bool, error) {
	if component == nil || bufferLength <= 0 {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	items := component.menuItems
	if list {
		items = component.listItems
	}
	if !validUICIndex(items, index) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	item := items[index]
	if _, err := r.writeCString(output, item.label, bufferLength); err != nil {
		return wipiReturn{}, true, err
	}
	if imagePointer != 0 {
		if err := r.writeU32(imagePointer, item.image); err != nil {
			return wipiReturn{}, true, err
		}
	}
	return wipiReturn{}, true, nil
}

func (r *wipiRuntime) removeUICItem(component *wipiComponent, index int32, list bool) (wipiReturn, bool, error) {
	if component == nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	items := component.menuItems
	if list {
		items = component.listItems
	}
	if !validUICIndex(items, index) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	items = append(items[:index], items[index+1:]...)
	if list {
		component.listItems = items
		if component.activeList >= int32(len(items)) {
			component.activeList = int32(len(items)) - 1
		}
	} else {
		component.menuItems = items
		if component.activeMenu >= int32(len(items)) {
			component.activeMenu = int32(len(items)) - 1
		}
	}
	return wipiReturn{}, true, nil
}

func (r *wipiRuntime) insertUICText(component *wipiComponent, index int32, source uint32, length int32) (wipiReturn, bool, error) {
	if component == nil || index < 0 || length < 0 ||
		index > int32(len(component.text)) ||
		length > component.maxText-int32(len(component.text)) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	data := make([]byte, length)
	if err := r.cpu.ReadMemory(source, data); err != nil {
		return wipiReturn{}, true, err
	}
	position := int(index)
	component.text = append(component.text, make([]byte, len(data))...)
	copy(component.text[position+len(data):], component.text[position:len(component.text)-len(data)])
	copy(component.text[position:], data)
	return wipiReturn{low: uint32(len(data))}, true, nil
}

func validUICIndex(items []wipiUIItem, index int32) bool {
	return index >= 0 && index < int32(len(items))
}
