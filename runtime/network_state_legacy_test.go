package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

func TestLegacyNetworkStateDoesNotReprocessOldWrites(t *testing.T) {
	s, err := NewServices(Config{})
	check(t, err)
	id := localSocket(t, s, "211.110.18.253", 8501)
	request := []byte{2, 0, 0xf8, 0x2a}
	// Old adapters recorded the write but generated no local response.
	_, err = s.Network.SocketWrite(1, id, request)
	check(t, err)
	old := networkStateV2{Limits: s.Network.limits, Sockets: []socketStateV2{{
		ID: id, Owner: 1, Domain: 2, Type: 1, State: ConnectionConnected,
		Host: "211.110.18.253", Address: 0xd36e12fd, Port: 8501, WriteData: request,
	}}, HTTP: s.Network.Snapshot().HTTP, Serial: s.Network.Snapshot().Serial}
	oldBytes, err := encodeStateValue(old)
	check(t, err)
	encoded, err := s.MarshalBinary()
	check(t, err)
	// Replace only the network component and recompute both checksums.
	d := binaryStateDecoder{reader: bytes.NewReader(encoded[:len(encoded)-sha256.Size])}
	headerSize := len(servicesStateMagic) + 12
	d.bytes(headerSize)
	var migrated bytes.Buffer
	migrated.Write(encoded[:headerSize])
	for _, spec := range requiredServiceComponents {
		start := d.offset
		name := string(d.bytes(int(d.u16())))
		d.u32()
		d.bytes(int(d.u64()))
		d.bytes(sha256.Size)
		if name != "network" {
			migrated.Write(encoded[start:d.offset])
			continue
		}
		check(t, binary.Write(&migrated, binary.LittleEndian, uint16(len(spec.id))))
		migrated.WriteString(spec.id)
		check(t, binary.Write(&migrated, binary.LittleEndian, uint32(2)))
		check(t, binary.Write(&migrated, binary.LittleEndian, uint64(len(oldBytes))))
		migrated.Write(oldBytes)
		digest := sha256.Sum256(oldBytes)
		migrated.Write(digest[:])
	}
	check(t, d.err)
	digest := sha256.Sum256(migrated.Bytes())
	migrated.Write(digest[:])
	clone, err := NewServices(Config{})
	check(t, err)
	check(t, clone.UnmarshalBinary(migrated.Bytes()))
	_, err = clone.WriteSocketRequest(1, id, request)
	check(t, err)
	got, err := clone.Network.SocketRead(1, id, 100)
	check(t, err)
	if !bytes.Equal(got, []byte{3, 0, 0xf9, 0x2a, 0}) {
		t.Fatalf("old writes replayed: %x", got)
	}
}
