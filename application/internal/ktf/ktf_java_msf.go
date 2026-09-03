package ktf

// org/kwis/msf/* is the WIPI framework layer: program control, shared
// memory, and network endpoints. The host models a single offline program,
// so program control reports this one program and endpoint factories fail
// the way a handset without a data connection does.

func (r *Runtime) handleMSFKernelMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "getPrgID()I", "getAMID()I":
		return 1, nil
	case "getParentPrgID()I":
		return 0, nil
	case "getAccessLevel()I":
		// Every access-request mask is granted.
		return 0xff, nil
	case "getPrgName()Ljava/lang/String;":
		return r.NewJavaString(r.Pkg.Descriptor.AID)
	case "getPrgInfo()[I":
		return r.newJavaIntArray([]uint32{1, 0, 0})
	case "getExecNames(Ljava/lang/String;Ljava/lang/String;" +
		"Ljava/lang/String;)[Ljava/lang/String;":
		return r.newJavaReferenceArray("[Ljava/lang/String;", nil)
	case "execute(Ljava/lang/String;[Ljava/lang/String;)I",
		"load(Ljava/lang/String;[Ljava/lang/String;)I",
		"mExecute(Ljava/lang/String;[Ljava/lang/String;)I",
		"mLoad(Ljava/lang/String;[Ljava/lang/String;)I":
		program, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		r.tracef(
			"java_kernel_%s_unavailable:%s",
			name,
			r.javaStringValue(program),
		)
		return ^uint32(0), nil
	case "stop(I)V":
		r.requestJavaTermination(0)
		return 0, nil
	case "letThrowExceptionWhenProgramExit()I":
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleMSFSharedMethod(
	name, descriptor string,
) (uint32, error) {
	makeBuffer := func(key string, size uint32) (uint32, error) {
		if size > 1<<20 {
			return 0, r.raiseHostJavaException(
				"java/lang/IllegalArgumentException",
			)
		}
		buffer, err := r.newJavaByteArray(make([]byte, size))
		if err != nil {
			return 0, err
		}
		r.sharedBuffers[key] = buffer
		return buffer, nil
	}
	switch name + descriptor {
	case "initialize()V":
		return 0, nil
	case "createBuf(I)[B":
		size, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return makeBuffer("", size)
	case "createBuf(Ljava/lang/String;I)[B":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		size, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		return makeBuffer(r.javaStringValue(nameAddress), size)
	case "getBuf()[B":
		return r.sharedBuffers[""], nil
	case "getBuf(Ljava/lang/String;)[B":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.sharedBuffers[r.javaStringValue(nameAddress)], nil
	case "resizeBuf(I)[B":
		size, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.resizeSharedBuffer("", size)
	case "resizeBuf([BI)[B":
		buffer, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		size, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		return r.resizeSharedBuffer(r.sharedBufferKey(buffer), size)
	case "destroyBuf([B)V":
		buffer, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		delete(r.sharedBuffers, r.sharedBufferKey(buffer))
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) sharedBufferKey(buffer uint32) string {
	for key, value := range r.sharedBuffers {
		if value == buffer {
			return key
		}
	}
	return ""
}

func (r *Runtime) resizeSharedBuffer(key string, size uint32) (uint32, error) {
	if size > 1<<20 {
		return 0, r.raiseHostJavaException(
			"java/lang/IllegalArgumentException",
		)
	}
	data := make([]byte, size)
	if previous := r.sharedBuffers[key]; previous != 0 {
		if existing, err := r.readJavaByteArray(previous); err == nil {
			copy(data, existing)
		}
	}
	buffer, err := r.newJavaByteArray(data)
	if err != nil {
		return 0, err
	}
	r.sharedBuffers[key] = buffer
	return buffer, nil
}

// handleMSFSocketMethod covers Socket and HttpSocket references. The host
// never hands one out (URL.find reports the network as unavailable), so
// these are terminal defaults for titles that construct their own wrappers.
func (r *Runtime) handleMSFSocketMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "getInputStream()Ljava/io/InputStream;":
		// The handset is offline, so the stream is at end of input from the
		// start. Answering null instead would only move the guest's null
		// dereference one call further along.
		stream, err := r.newJavaInstance("java/io/InputStream", 4)
		if err != nil {
			return 0, err
		}
		r.inputStreams[stream] = &ktfInputStream{}
		return stream, nil
	case "getOutputStream()Ljava/io/OutputStream;":
		stream, err := r.newJavaInstance("java/io/OutputStream", 4)
		if err != nil {
			return 0, err
		}
		r.outputStreams[stream] = nil
		return stream, nil
	case "close()V":
		return 0, nil
	case "getRequestMethod()Ljava/lang/String;":
		return r.NewJavaString("GET")
	case "getProtocol()Ljava/lang/String;":
		return r.NewJavaString("http")
	case "getResponseCode()I":
		// HTTP_UNAVAILABLE: the handset has no data connection.
		return 503, nil
	case "getResponseMessage()Ljava/lang/String;":
		return r.NewJavaString("Service Unavailable")
	case "getPort()I":
		return 80, nil
	case "getLength()J", "getDate()J", "getExpiration()J",
		"getLastModified()J":
		return r.javaLongResult(0), nil
	case "isRelocatable()Z":
		return 0, nil
	default:
		// Remaining accessors resolve to null and mutators are absorbed.
		return 0, nil
	}
}

// Message state lives in the generic per-instance component record: text is
// the address string, image is the payload array, date is the Date
// reference, minimum is the numeric address, viewAmount is the length,
// changeAmount is the offset, delay is the classification, activeIndex is
// the index, and mode is the teleservice id.
func (r *Runtime) handleMSFMessageMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	state := r.lwcComponent(instance)
	argument := func() (uint32, error) { return r.parameter(2) }
	switch name + descriptor {
	case "<init>()V", "<init>([B)V":
		if descriptor == "([B)V" {
			data, valueErr := argument()
			if valueErr != nil {
				return 0, valueErr
			}
			state.image = data
		}
		return 0, nil
	case "getAddress()Ljava/lang/String;":
		return state.text, nil
	case "setAddress(Ljava/lang/String;)V":
		address, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.text = address
		return 0, nil
	case "getAddressInt()I":
		return uint32(state.minimum), nil
	case "setAddressInt(I)V":
		address, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.minimum = int32(address)
		return 0, nil
	case "getData()[B":
		return state.image, nil
	case "getDate()Ljava/util/Date;":
		return state.date, nil
	case "setDate(Ljava/util/Date;)V":
		date, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.date = date
		return 0, nil
	case "getClassification()B":
		return uint32(state.delay), nil
	case "setClassification(B)V":
		classification, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.delay = int32(int8(classification))
		return 0, nil
	case "getIndex()B":
		return uint32(state.activeIndex), nil
	case "setIndex(B)V":
		index, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.activeIndex = int32(int8(index))
		return 0, nil
	case "getLength()I":
		return uint32(state.viewAmount), nil
	case "setLength(I)I":
		length, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.viewAmount = int32(length)
		return length, nil
	case "getOffset()I":
		return uint32(state.changeAmount), nil
	case "setOffset(I)I":
		offset, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.changeAmount = int32(offset)
		return offset, nil
	case "getTeleServiceID()I":
		return uint32(state.mode), nil
	case "setTeleServiceID(I)V":
		serviceID, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		state.mode = int32(serviceID)
		return 0, nil
	default:
		return 0, nil
	}
}
