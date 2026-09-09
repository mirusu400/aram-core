package ktf

import (
	"net/url"
	"strings"
)

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
		"load(Ljava/lang/String;)I",
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
	case "isStream()Z":
		return 1, nil
	case "getMessageCount()I":
		return ^uint32(0), nil
	case "getMessageMaxLength()I":
		return 65535, nil
	case "send(Lorg/kwis/msf/io/Message;)V",
		"recv(Lorg/kwis/msf/io/Message;)V",
		"accept()Lorg/kwis/msf/io/Socket;":
		return 0, r.raiseHostJavaException("java/io/IOException")
	case "getRequestMethod()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if method := r.lwcEventData[instance]; method != 0 {
			return method, nil
		}
		return r.NewJavaString("GET")
	case "getProtocol()Ljava/lang/String;":
		parsed, err := r.msfSocketURL()
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(parsed.Scheme)
	case "getHost()Ljava/lang/String;":
		parsed, err := r.msfSocketURL()
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(parsed.Hostname())
	case "getFile()Ljava/lang/String;":
		parsed, err := r.msfSocketURL()
		if err != nil {
			return 0, err
		}
		file := parsed.EscapedPath()
		if parsed.RawQuery != "" {
			file += "?" + parsed.RawQuery
		}
		return r.NewJavaString(file)
	case "getQuery()Ljava/lang/String;":
		parsed, err := r.msfSocketURL()
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(parsed.RawQuery)
	case "getRef()Ljava/lang/String;":
		parsed, err := r.msfSocketURL()
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(parsed.Fragment)
	case "getURL()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.lwcComponent(instance).text, nil
	case "getResponseCode()I":
		// HTTP_UNAVAILABLE: the handset has no data connection.
		return 503, nil
	case "getResponseMessage()Ljava/lang/String;":
		return r.NewJavaString("Service Unavailable")
	case "getPort()I":
		parsed, err := r.msfSocketURL()
		if err != nil {
			return 0, err
		}
		if port := parsed.Port(); port != "" {
			value := uint32(0)
			for _, digit := range port {
				if digit < '0' || digit > '9' {
					return 0, nil
				}
				value = value*10 + uint32(digit-'0')
			}
			return value, nil
		}
		if strings.EqualFold(parsed.Scheme, "https") {
			return 443, nil
		}
		return 80, nil
	case "getLength()J", "getDate()J", "getExpiration()J",
		"getLastModified()J":
		return r.javaLongResult(0), nil
	case "getEncoding()Ljava/lang/String;",
		"getHeaderField(Ljava/lang/String;)Ljava/lang/String;",
		"getType()Ljava/lang/String;",
		"relocation()Lorg/kwis/msf/io/HttpSocket;":
		return 0, nil
	case "isRelocatable()Z":
		return 0, nil
	case "setRequestMethod(Ljava/lang/String;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		method, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if method == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		r.lwcEventData[instance] = method
		return 0, nil
	case "setRequestProperty(Ljava/lang/String;Ljava/lang/String;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		key, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		if key == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		properties := r.Vectors[instance]
		for index := 0; index+1 < len(properties); index += 2 {
			if strings.EqualFold(
				r.javaStringValue(properties[index]),
				r.javaStringValue(key),
			) {
				properties[index+1] = value
				r.Vectors[instance] = properties
				return 0, nil
			}
		}
		r.Vectors[instance] = append(properties, key, value)
		return 0, nil
	case "getRequestProperty(Ljava/lang/String;)Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		key, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		properties := r.Vectors[instance]
		for index := 0; index+1 < len(properties); index += 2 {
			if strings.EqualFold(
				r.javaStringValue(properties[index]),
				r.javaStringValue(key),
			) {
				return properties[index+1], nil
			}
		}
		return 0, nil
	case "setProxy(Ljava/lang/String;I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		host, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		port, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		state := r.lwcComponent(instance)
		state.image = host
		state.minimum = int32(port)
		return 0, nil
	default:
		// Remaining accessors resolve to null and mutators are absorbed.
		return 0, nil
	}
}

func (r *Runtime) msfSocketURL() (*url.URL, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(r.javaStringValue(r.lwcComponent(instance).text))
	if err != nil {
		return &url.URL{}, nil
	}
	return parsed, nil
}

func (r *Runtime) newOfflineMSFSocket(urlAddress uint32) (uint32, error) {
	rawURL := r.javaStringValue(urlAddress)
	parsed, _ := url.Parse(rawURL)
	className := "org/kwis/msf/io/Socket"
	if strings.EqualFold(parsed.Scheme, "http") ||
		strings.EqualFold(parsed.Scheme, "https") {
		className = "org/kwis/msf/io/HttpSocket"
	}
	instance, err := r.NewHostJavaObject(className)
	if err != nil {
		return 0, err
	}
	r.lwcComponent(instance).text = urlAddress
	return instance, nil
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
	case "<init>()V":
		return 0, nil
	case "<init>([B)V":
		data, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		length, valueErr := r.javaArrayLength(data)
		if valueErr != nil {
			return 0, valueErr
		}
		state.image = data
		state.viewAmount = int32(length)
		return 0, nil
	case "<init>(Ljava/lang/String;[B)V",
		"<init>(Ljava/lang/String;[BII)V":
		address, valueErr := argument()
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		arrayLength, valueErr := r.javaArrayLength(data)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, length := uint32(0), arrayLength
		if descriptor == "(Ljava/lang/String;[BII)V" {
			offset, valueErr = r.parameter(4)
			if valueErr != nil {
				return 0, valueErr
			}
			length, valueErr = r.parameter(5)
			if valueErr != nil {
				return 0, valueErr
			}
			if offset > arrayLength || length > arrayLength-offset {
				return 0, r.raiseHostJavaException(
					"java/lang/IndexOutOfBoundsException",
				)
			}
		}
		state.text = address
		state.image = data
		state.changeAmount = int32(offset)
		state.viewAmount = int32(length)
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
	case "setAddressInt(I)V", "getAddressInt(I)V":
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
	case "setDate(Ljava/util/Date;)V", "getDate(Ljava/util/Date;)V":
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
