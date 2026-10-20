package application

import (
	"bytes"
	"fmt"
)

const maxGVMPersistentDataBytes = 65_534

// gvmPersistentStore models the guest's fixed persistent data record for one
// machine lifetime. The owner serializes calls through the machine lock.
type gvmPersistentStore struct {
	data []byte
}

func (s *gvmPersistentStore) ReadGVMData(size uint32) ([]byte, error) {
	result := make([]byte, size)
	copy(result, s.data)
	return result, nil
}

func (s *gvmPersistentStore) WriteGVMData(data []byte) error {
	if len(data) > maxGVMPersistentDataBytes {
		return fmt.Errorf("GVM persistent data exceeds %d bytes", maxGVMPersistentDataBytes)
	}
	s.data = bytes.Clone(data)
	return nil
}

func (s *gvmPersistentStore) snapshot() []byte {
	if s == nil {
		return nil
	}
	return bytes.Clone(s.data)
}

func (s *gvmPersistentStore) restore(data []byte) error {
	if len(data) > maxGVMPersistentDataBytes {
		return fmt.Errorf("GVM persistent data exceeds %d bytes", maxGVMPersistentDataBytes)
	}
	s.data = bytes.Clone(data)
	return nil
}
