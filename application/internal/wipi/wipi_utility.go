package wipi

import (
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"net"
)

func (r *Runtime) dispatchUtility(name string) (guest.WIPIReturn, bool, error) {
	args, err := r.args(2)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	switch name {
	case "MC_utilHtonl", "MC_utilNtohl":
		return guest.WIPIReturn{Low: reverse32(args[0])}, true, nil
	case "MC_utilHtons", "MC_utilNtohs":
		return guest.WIPIReturn{Low: uint32(reverse16(uint16(args[0])))}, true, nil
	case "MC_utilInetAddrInt":
		value, err := r.ReadCString(args[0])
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		ip := net.ParseIP(string(value)).To4()
		if ip == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{Low: binary.BigEndian.Uint32(ip)}, true, nil
	case "MC_utilInetAddrStr":
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], args[0])
		_, err := r.writeCString(args[1], []byte(net.IP(encoded[:]).String()), -1)
		return guest.WIPIReturn{}, true, err
	default:
		return guest.WIPIReturn{}, false, nil
	}
}

func reverse16(value uint16) uint16 {
	return value>>8 | value<<8
}

func reverse32(value uint32) uint32 {
	return uint32(reverse16(uint16(value)))<<16 | uint32(reverse16(uint16(value>>16)))
}
