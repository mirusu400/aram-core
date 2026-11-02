package application

import (
	"crypto/sha256"
	"math"
	"sort"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

var _ machinecore.VibrationMachine = (*Machine)(nil)
var _ machinecore.VibrationMachine = (*gnex32Machine)(nil)
var _ machinecore.ExternalRequestMachine = (*Machine)(nil)
var _ machinecore.ExternalRequestMachine = (*gnex32Machine)(nil)

// externalRequestSequences assigns host-visible IDs independently of the
// runtime-local request counter. Runtime reset and save-state restoration can
// rewind that counter; retaining the host counter prevents a delayed ACK from
// an earlier guest timeline from consuming a new request with the same local
// sequence number.
type externalRequestSequences struct {
	next        uint64
	exhausted   bool
	hostByGuest map[uint64]externalRequestMapping
}

type externalRequestMapping struct {
	host     uint64
	atNS     int64
	kind     shared.ExternalRequestKind
	target   string
	dataHash [sha256.Size]byte
}

func newExternalRequestMapping(host uint64, request shared.ExternalRequest) externalRequestMapping {
	return externalRequestMapping{
		host:     host,
		atNS:     request.AtNS,
		kind:     request.Kind,
		target:   request.Target,
		dataHash: sha256.Sum256(request.Data),
	}
}

func (m externalRequestMapping) matches(request shared.ExternalRequest) bool {
	return m.atNS == request.AtNS &&
		m.kind == request.Kind &&
		m.target == request.Target &&
		m.dataHash == sha256.Sum256(request.Data)
}

func (s *externalRequestSequences) project(
	requests []shared.ExternalRequest,
) []machinecore.ExternalRequest {
	if len(requests) == 0 {
		s.hostByGuest = nil
		return nil
	}
	if s.next == 0 {
		s.next = 1
	}
	if s.hostByGuest == nil {
		s.hostByGuest = make(map[uint64]externalRequestMapping, len(requests))
	}
	ordered := append([]shared.ExternalRequest(nil), requests...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].Sequence < ordered[right].Sequence
	})
	unique := ordered[:0]
	for _, request := range ordered {
		if request.Sequence == 0 ||
			len(unique) != 0 && unique[len(unique)-1].Sequence == request.Sequence {
			continue
		}
		unique = append(unique, request)
	}
	ordered = unique
	if len(ordered) == 0 {
		s.hostByGuest = nil
		return nil
	}
	live := make(map[uint64]struct{}, len(ordered))
	for _, request := range ordered {
		live[request.Sequence] = struct{}{}
	}
	for guest := range s.hostByGuest {
		if _, exists := live[guest]; !exists {
			delete(s.hostByGuest, guest)
		}
	}
	result := make([]machinecore.ExternalRequest, 0, len(ordered))
	var previousHost uint64
	for _, request := range ordered {
		mapping, exists := s.hostByGuest[request.Sequence]
		if exists && !mapping.matches(request) {
			delete(s.hostByGuest, request.Sequence)
			exists = false
		}
		if exists && mapping.host <= previousHost {
			delete(s.hostByGuest, request.Sequence)
			exists = false
		}
		if !exists {
			if s.exhausted {
				continue
			}
			sequence := s.next
			if s.next == math.MaxUint64 {
				s.exhausted = true
			} else {
				s.next++
			}
			mapping = newExternalRequestMapping(sequence, request)
			s.hostByGuest[request.Sequence] = mapping
		}
		result = append(result, machinecore.ExternalRequest{
			Sequence: mapping.host,
			GuestNS:  request.AtNS,
			Kind:     machinecore.ExternalRequestKind(request.Kind),
			Target:   request.Target,
			Data:     append([]byte(nil), request.Data...),
		})
		previousHost = mapping.host
	}
	return result
}

func (s *externalRequestSequences) acknowledge(through uint64) (uint64, bool) {
	var guestThrough uint64
	for guest, mapping := range s.hostByGuest {
		if mapping.host == through {
			guestThrough = guest
			break
		}
	}
	if guestThrough == 0 {
		return 0, false
	}
	for guest := range s.hostByGuest {
		if guest <= guestThrough {
			delete(s.hostByGuest, guest)
		}
	}
	return guestThrough, true
}

func (s *externalRequestSequences) nextSession() {
	s.hostByGuest = nil
}

// Vibration exposes GNEX handset vibration using the same optional host
// contract as the other runtimes.
func (m *gnex32Machine) Vibration() (uint8, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return 0, 0
	}
	level, until := m.runtime.Vibration()
	return hostVibration(level, until, m.runtime.AudioTime())
}

func (m *Machine) ExternalRequests() []machinecore.ExternalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	services := m.guestServicesLocked()
	if services == nil || services.Device == nil {
		return nil
	}
	return m.requestIDs.project(services.Device.Requests())
}

func (m *Machine) ClearExternalRequests(through uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	services := m.guestServicesLocked()
	if services != nil && services.Device != nil {
		if guestThrough, ok := m.requestIDs.acknowledge(through); ok {
			services.Device.ClearRequests(guestThrough)
		}
	}
}

func (m *gnex32Machine) ExternalRequests() []machinecore.ExternalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.runtime == nil {
		return nil
	}
	return m.requestIDs.project(m.runtime.ExternalRequests())
}

func (m *gnex32Machine) ClearExternalRequests(through uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed && m.runtime != nil {
		if guestThrough, ok := m.requestIDs.acknowledge(through); ok {
			m.runtime.ClearExternalRequests(guestThrough)
		}
	}
}
