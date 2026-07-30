package application

func (r *wipiRuntime) dispatchMedia(name string) (wipiReturn, bool, error) {
	count := mediaArgumentCount(name)
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
	clip := func() *wipiMediaClip { return r.mediaClips[arg(0)] }
	switch name {
	case "MC_mdaClipCreate":
		mediaType, err := r.readCString(arg(0))
		if err != nil {
			return wipiReturn{}, true, err
		}
		capacity := int32(arg(1))
		if capacity < 0 || capacity > int32(maxWIPIString) {
			return wipiReturn{}, true, nil
		}
		handle, err := r.heap.allocate(64, true)
		if err != nil || handle == 0 {
			return wipiReturn{}, true, err
		}
		r.mediaClips[handle] = &wipiMediaClip{
			handle:    handle,
			mediaType: append([]byte(nil), mediaType...),
			capacity:  capacity,
			callback:  arg(2),
			volume:    100,
		}
		return wipiReturn{low: handle}, true, nil
	case "MC_mdaClipFree":
		if clip() == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(r.mediaClips, arg(0))
		r.heap.release(arg(0))
		return wipiReturn{}, true, nil
	case "MC_mdaClipGetType":
		current := clip()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		count, err := r.writeCString(arg(1), current.mediaType, int32(arg(2)))
		return wipiReturn{low: count}, true, err
	case "MC_mdaClipPutData":
		return r.putMediaData(clip(), arg(1), int32(arg(2)))
	case "MC_mdaClipPutDataByFile":
		current := clip()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		name, err := r.guestPath(arg(1), int32(arg(3)))
		if err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		data, ok := r.files[name]
		if !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		size := min(len(data), max(0, int(int32(arg(2)))))
		return r.appendMediaData(current, data[:size])
	case "MC_mdaClipGetData":
		current := clip()
		length := int(int32(arg(2)))
		if current == nil || length < 0 {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		count := min(length, len(current.data))
		if err := r.cpu.WriteMemory(arg(1), current.data[:count]); err != nil {
			return wipiReturn{}, true, err
		}
		current.data = append(current.data[:0], current.data[count:]...)
		return wipiReturn{low: uint32(count)}, true, nil
	case "MC_mdaClipAvailableDataSize":
		if current := clip(); current != nil {
			return wipiReturn{low: uint32(len(current.data))}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_mdaClipClearData":
		if current := clip(); current != nil {
			current.data = nil
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_mdaClipSetPosition":
		if current := clip(); current != nil {
			current.position = int32(arg(1))
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_mdaClipGetVolume":
		if current := clip(); current != nil {
			return wipiReturn{low: uint32(current.volume)}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_mdaClipSetVolume":
		if current := clip(); current != nil {
			current.volume = int32(clamp(int(int32(arg(1))), 0, 100))
		}
		return wipiReturn{}, true, nil
	case "MC_mdaPlay":
		if current := clip(); current != nil {
			current.state = 1
			current.repeat = arg(1) != 0
			r.enqueueCallback(current.callback, current.handle, uint32(current.state))
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_mdaPause":
		return r.setMediaState(clip(), 2)
	case "MC_mdaResume":
		return r.setMediaState(clip(), 1)
	case "MC_mdaStop":
		return r.setMediaState(clip(), 0)
	case "MC_mdaRecord":
		return r.setMediaState(clip(), 3)
	case "MC_mdaGetVolume":
		return wipiReturn{low: uint32(r.mediaVolume)}, true, nil
	case "MC_mdaSetVolume":
		r.mediaVolume = int32(clamp(int(int32(arg(0))), 0, 100))
		return wipiReturn{}, true, nil
	case "MC_mdaVibrator":
		r.vibratorLevel = int32(arg(0))
		r.vibratorTimeout = max(0, int32(arg(1)))
		return wipiReturn{}, true, nil
	case "MC_mdaSetMuteState":
		r.mediaMute[int32(arg(0))] = arg(1) != 0
		return wipiReturn{}, true, nil
	case "MC_mdaGetMuteState":
		if r.mediaMute[int32(arg(0))] {
			return wipiReturn{low: 1}, true, nil
		}
		return wipiReturn{}, true, nil
	default:
		return wipiReturn{}, false, nil
	}
}

func mediaArgumentCount(name string) int {
	switch name {
	case "MC_mdaGetVolume":
		return 0
	case "MC_mdaClipFree", "MC_mdaClipAvailableDataSize", "MC_mdaClipClearData",
		"MC_mdaClipGetVolume", "MC_mdaPause", "MC_mdaResume", "MC_mdaStop",
		"MC_mdaRecord", "MC_mdaSetVolume", "MC_mdaGetMuteState":
		return 1
	case "MC_mdaClipSetPosition", "MC_mdaClipSetVolume", "MC_mdaPlay",
		"MC_mdaVibrator", "MC_mdaSetMuteState":
		return 2
	case "MC_mdaClipCreate", "MC_mdaClipPutData", "MC_mdaClipGetType",
		"MC_mdaClipGetData":
		return 3
	case "MC_mdaClipPutDataByFile":
		return 4
	default:
		return 0
	}
}

func (r *wipiRuntime) putMediaData(clip *wipiMediaClip, source uint32, length int32) (wipiReturn, bool, error) {
	if clip == nil || length < 0 || length > int32(maxWIPIString) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	data := make([]byte, length)
	if err := r.cpu.ReadMemory(source, data); err != nil {
		return wipiReturn{}, true, err
	}
	return r.appendMediaData(clip, data)
}

func (r *wipiRuntime) appendMediaData(clip *wipiMediaClip, data []byte) (wipiReturn, bool, error) {
	if clip.capacity > 0 && len(clip.data)+len(data) > int(clip.capacity) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	clip.data = append(clip.data, data...)
	return wipiReturn{low: uint32(len(data))}, true, nil
}

func (r *wipiRuntime) setMediaState(clip *wipiMediaClip, state uint8) (wipiReturn, bool, error) {
	if clip == nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	clip.state = state
	if state == 0 {
		clip.repeat = false
	}
	r.enqueueCallback(clip.callback, clip.handle, uint32(state))
	return wipiReturn{}, true, nil
}

func (r *wipiRuntime) dispatchMisc(name string) (wipiReturn, bool, error) {
	switch name {
	case "MC_miscBackLight":
		args, err := r.args(4)
		if err != nil {
			return wipiReturn{}, true, err
		}
		for index, value := range args {
			r.backlight[index] = int32(value)
		}
		return wipiReturn{}, true, nil
	case "MC_miscSetLed":
		value, err := r.arg(0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		r.ledState = int32(value)
		return wipiReturn{}, true, nil
	case "MC_miscGetLed":
		return wipiReturn{low: uint32(r.ledState)}, true, nil
	case "MC_miscGetLedCount":
		return wipiReturn{low: 1}, true, nil
	default:
		return wipiReturn{}, false, nil
	}
}

func (r *wipiRuntime) dispatchPhone(name string) (wipiReturn, bool, error) {
	if name != "MC_phnCallPlace" {
		return wipiReturn{}, false, nil
	}
	address, err := r.arg(0)
	if err != nil {
		return wipiReturn{}, true, err
	}
	number, err := r.readCString(address)
	if err != nil {
		return wipiReturn{}, true, err
	}
	r.phoneRequests = append(r.phoneRequests, append([]byte(nil), number...))
	return wipiReturn{}, true, nil
}

func (r *wipiRuntime) dispatchSerial(name string) (wipiReturn, bool, error) {
	count := map[string]int{
		"MC_srlOpen":       2,
		"MC_srlWrite":      3,
		"MC_srlRead":       3,
		"MC_srlSetReadCB":  3,
		"MC_srlSetWriteCB": 3,
		"MC_srlClose":      1,
	}[name]
	if count == 0 {
		return wipiReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return wipiReturn{}, true, err
	}
	switch name {
	case "MC_srlOpen":
		if int32(args[0]) != 0 {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		descriptor := r.nextSerial
		r.nextSerial++
		r.serialPorts[descriptor] = &wipiSerialPort{descriptor: descriptor, port: 0}
		return wipiReturn{low: uint32(descriptor)}, true, nil
	case "MC_srlClose":
		descriptor := int32(args[0])
		if r.serialPorts[descriptor] == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(r.serialPorts, descriptor)
		return wipiReturn{}, true, nil
	case "MC_srlWrite":
		port := r.serialPorts[int32(args[0])]
		length := int32(args[2])
		if port == nil || length < 0 || length > int32(maxWIPIString) ||
			len(port.data)+int(length) > int(maxWIPIString) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		data := make([]byte, length)
		if err := r.cpu.ReadMemory(args[1], data); err != nil {
			return wipiReturn{}, true, err
		}
		port.data = append(port.data, data...)
		return wipiReturn{low: uint32(len(data))}, true, nil
	case "MC_srlRead":
		port := r.serialPorts[int32(args[0])]
		length := int32(args[2])
		if port == nil || length < 0 {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		count := min(len(port.data), int(length))
		if err := r.cpu.WriteMemory(args[1], port.data[:count]); err != nil {
			return wipiReturn{}, true, err
		}
		port.data = append(port.data[:0], port.data[count:]...)
		return wipiReturn{low: uint32(count)}, true, nil
	case "MC_srlSetReadCB":
		port := r.serialPorts[int32(args[0])]
		if port == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		port.readCallback, port.readParameter = args[1], args[2]
		if len(port.data) != 0 && port.readCallback != 0 {
			r.enqueueCallback(
				port.readCallback,
				uint32(port.descriptor),
				0,
				port.readParameter,
			)
			port.readCallback, port.readParameter = 0, 0
		}
		return wipiReturn{}, true, nil
	case "MC_srlSetWriteCB":
		port := r.serialPorts[int32(args[0])]
		if port == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		port.writeCallback, port.writeParameter = args[1], args[2]
		if port.writeCallback != 0 {
			r.enqueueCallback(
				port.writeCallback,
				uint32(port.descriptor),
				0,
				port.writeParameter,
			)
			port.writeCallback, port.writeParameter = 0, 0
		}
		return wipiReturn{}, true, nil
	default:
		return wipiReturn{}, false, nil
	}
}
