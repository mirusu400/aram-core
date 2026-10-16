package wipi

import "fmt"

// OEMFile adapts the native OEM handle conventions to the shared filesystem.
// Native open failure is zero; read/write/seek keep their byte-count results.
func (r *Runtime) OEMFile(slot uint32, args []uint32) (uint32, error) {
	need := map[uint32]int{0x10: 2, 0x14: 1, 0x18: 3, 0x1c: 3, 0x28: 3, 0x48: 1}[slot]
	if need == 0 || len(args) < need {
		return 0, fmt.Errorf("invalid OEM file operation %#x", slot)
	}
	switch slot {
	case 0x48:
		name, err := r.guestPath(args[0], 0)
		if err != nil {
			return 0, nil
		}
		if _, exists := r.Files[name]; exists {
			return 1, nil
		}
		return 0, nil
	case 0x10:
		mode := args[1]
		if mode > 2 {
			return 0, nil
		}
		name, err := r.guestPath(args[0], 0)
		if err != nil {
			return 0, nil
		}
		if _, exists := r.Files[name]; !exists && mode != 2 {
			return 0, nil
		}
		flags := int32(8)
		if mode == 1 {
			flags = 1
		} else if mode == 2 {
			flags = 10
		}
		result, _, err := r.openFile(args[0], flags, 0)
		if int32(result.Low) < 0 {
			return 0, err
		}
		return result.Low, err
	case 0x14:
		fd := int32(args[0])
		if service := r.fileServices[fd]; service != 0 {
			if err := r.Services.Storage.Close(r.ServiceOwner, service); err != nil {
				return 0, err
			}
			delete(r.fileServices, fd)
		}
		delete(r.fileHandles, fd)
		return 0, nil
	case 0x18:
		result, _, err := r.readFile(int32(args[0]), args[1], int32(args[2]))
		return result.Low, err
	case 0x1c:
		result, _, err := r.writeFile(int32(args[0]), args[1], int32(args[2]))
		return result.Low, err
	case 0x28:
		result, _, err := r.seekFile(int32(args[0]), int32(args[1]), int32(args[2]))
		return result.Low, err
	}
	return 0, nil
}
