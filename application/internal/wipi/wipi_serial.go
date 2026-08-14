package wipi

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
)

func (r *Runtime) dispatchSerial(name string) (guest.WIPIReturn, bool, error) {
	count := map[string]int{
		"MC_srlOpen":       2,
		"MC_srlWrite":      3,
		"MC_srlRead":       3,
		"MC_srlSetReadCB":  3,
		"MC_srlSetWriteCB": 3,
		"MC_srlClose":      1,
	}[name]
	if count == 0 {
		return guest.WIPIReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	switch name {
	case "MC_srlOpen":
		if int32(args[0]) != 0 {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		serviceID, serviceErr := r.Services.Network.OpenSerial(
			r.ServiceOwner,
			int32(args[0]),
		)
		if serviceErr != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		descriptor := r.nextSerial
		r.nextSerial++
		r.serialPorts[descriptor] = &wipiSerialPort{descriptor: descriptor, port: 0}
		r.serialServices[descriptor] = serviceID
		return guest.WIPIReturn{Low: uint32(descriptor)}, true, nil
	case "MC_srlClose":
		descriptor := int32(args[0])
		if r.serialPorts[descriptor] == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if serviceID := r.serialServices[descriptor]; serviceID != 0 {
			if err := r.Services.Network.CloseSerial(
				r.ServiceOwner,
				serviceID,
				r.Services.Events,
			); err != nil {
				return guest.WIPIReturn{}, true, err
			}
			delete(r.serialServices, descriptor)
		}
		delete(r.serialPorts, descriptor)
		return guest.WIPIReturn{}, true, nil
	case "MC_srlWrite":
		port := r.serialPorts[int32(args[0])]
		length := int32(args[2])
		if port == nil || length < 0 || length > int32(maxWIPIString) ||
			len(port.Data)+int(length) > int(maxWIPIString) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		data := make([]byte, length)
		if err := r.CPU.ReadMemory(args[1], data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		serviceID := r.serialServices[port.descriptor]
		if _, err := r.Services.Network.SerialWrite(
			r.ServiceOwner,
			serviceID,
			data,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if err := r.Services.InjectSerialResponse(
			r.ServiceOwner,
			serviceID,
			data,
			r.Services.Clock.Monotonic(),
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		port.Data = append(port.Data, data...)
		return guest.WIPIReturn{Low: uint32(len(data))}, true, nil
	case "MC_srlRead":
		port := r.serialPorts[int32(args[0])]
		length := int32(args[2])
		if port == nil || length < 0 {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		count := min(len(port.Data), int(length))
		data, serviceErr := r.Services.Network.SerialRead(
			r.ServiceOwner,
			r.serialServices[port.descriptor],
			uint64(count),
		)
		if serviceErr != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if len(data) != count {
			data = append([]byte(nil), port.Data[:count]...)
		}
		if err := r.CPU.WriteMemory(args[1], data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		port.Data = append(port.Data[:0], port.Data[count:]...)
		return guest.WIPIReturn{Low: uint32(count)}, true, nil
	case "MC_srlSetReadCB":
		port := r.serialPorts[int32(args[0])]
		if port == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		port.readCallback, port.readParameter = args[1], args[2]
		if len(port.Data) != 0 && port.readCallback != 0 {
			r.EnqueueCallback(
				port.readCallback,
				uint32(port.descriptor),
				0,
				port.readParameter,
			)
			port.readCallback, port.readParameter = 0, 0
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_srlSetWriteCB":
		port := r.serialPorts[int32(args[0])]
		if port == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		port.writeCallback, port.writeParameter = args[1], args[2]
		if port.writeCallback != 0 {
			r.EnqueueCallback(
				port.writeCallback,
				uint32(port.descriptor),
				0,
				port.writeParameter,
			)
			port.writeCallback, port.writeParameter = 0, 0
		}
		return guest.WIPIReturn{}, true, nil
	default:
		return guest.WIPIReturn{}, false, nil
	}
}
