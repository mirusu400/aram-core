package application

import "bytes"

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
	s.data = bytes.Clone(data)
	return nil
}
