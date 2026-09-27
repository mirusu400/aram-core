package ktf

import shared "github.com/mirusu400/aram-core/runtime"

func (r *Runtime) closeLocalSocket(instance uint32) error {
	id := r.socketServices[instance]
	if id == 0 {
		return nil
	}
	if err := r.Services.Network.CloseSocket(r.ServiceOwner, id, r.Services.Events); err != nil {
		return r.raiseHostJavaException("java/io/IOException")
	}
	for object, current := range r.socketServices {
		if current == id {
			r.socketServices[object] = 0
		}
	}
	return nil
}

func (r *Runtime) refreshLocalSocketInput(instance uint32) error {
	id, exists := r.socketServices[instance]
	if !exists {
		return nil
	}
	if id == 0 {
		return r.raiseHostJavaException("java/io/IOException")
	}
	info, err := r.Services.Network.SocketInfo(r.ServiceOwner, id)
	if err != nil {
		return r.raiseHostJavaException("java/io/IOException")
	}
	stream := r.inputStreams[instance]
	if stream == nil || uint64(stream.position) > uint64(len(stream.data)) {
		return r.raiseHostJavaException("java/io/IOException")
	}
	if info.ReadBytes == 0 {
		return nil
	}
	unread := stream.data[stream.position:]
	if uint64(len(unread))+info.ReadBytes > r.Services.Config.Limits.Network.MaxBufferBytes {
		return r.raiseHostJavaException("java/io/IOException")
	}
	data, err := r.Services.Network.SocketRead(r.ServiceOwner, id, info.ReadBytes)
	if err != nil {
		return r.raiseHostJavaException("java/io/IOException")
	}
	stream.data = append(append([]byte(nil), unread...), data...)
	stream.position, stream.mark = 0, 0
	return nil
}

func (r *Runtime) outputStreamTarget(instance uint32) uint32 {
	for depth := 0; depth < 64; depth++ {
		target := r.outputTargets[instance]
		if target == 0 || target == instance {
			break
		}
		instance = target
	}
	return instance
}

func (r *Runtime) appendOutputBytes(target uint32, data []byte) error {
	if id, exists := r.socketServices[target]; exists {
		if id == 0 {
			return r.raiseHostJavaException("java/io/IOException")
		}
		if _, err := r.Services.WriteSocketRequest(r.ServiceOwner, id, data); err != nil {
			return r.raiseHostJavaException("java/io/IOException")
		}
		return nil
	}
	r.outputStreams[target] = append(r.outputStreams[target], data...)
	if file := r.fileStreamTargets[target]; file != 0 {
		_, err := r.writeKTFFile(file, data)
		return err
	}
	return nil
}

func validateKTFLocalSockets(services *shared.Services, owner shared.OwnerID, sockets map[uint32]shared.ServiceID, meta ktfMetadataSnapshot) error {
	if len(sockets) > maxKTFStateEntries {
		return shared.ErrLimitExceeded
	}
	for object, id := range sockets {
		_, input := meta.InputStreams[object]
		_, output := meta.OutputStreams[object]
		_, socket := meta.LWCComponents[object]
		if object == 0 || (!input && !output && !socket) {
			return shared.ErrInvalidState
		}
		if id != 0 && !services.Network.HasLocalSocketServer(owner, id) {
			return shared.ErrInvalidState
		}
		if input {
			stream := meta.InputStreams[object]
			if uint64(stream.Position) > uint64(len(stream.Data)) || uint64(len(stream.Data)) > services.Config.Limits.Network.MaxBufferBytes {
				return shared.ErrInvalidState
			}
		}
	}
	return nil
}
