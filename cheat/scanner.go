package cheat

import (
	"bytes"
	"fmt"
	"math"
	"math/bits"
)

// Scan preserves the legacy materialized-result limit and baseline values.
// Interactive clients should use StartScan and ScanPage instead.
func (e *Engine) Scan(request ScanRequest) ([]Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.startScanLocked(request, e.maxResults); err != nil {
		return nil, err
	}
	return e.matchesLocked()
}

// StartScan stores region snapshots and one bit per aligned candidate. It is
// bounded by MaxScanBytes rather than the legacy materialized MaxResults cap.
func (e *Engine) StartScan(request ScanRequest) (ScanSummary, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.startScanLocked(request, 0); err != nil {
		return ScanSummary{}, err
	}
	return e.summaryLocked(), nil
}

func (e *Engine) startScanLocked(request ScanRequest, resultLimit int) error {
	if !request.Type.Valid() {
		return fmt.Errorf("invalid scan value type %d", request.Type)
	}
	if !request.Comparison.Valid() || request.Comparison.needsPrevious() {
		return fmt.Errorf("comparison %d is invalid for a first scan", request.Comparison)
	}
	target, err := validateScanTarget(request.Type, request.Comparison, request.Value)
	if err != nil {
		return err
	}
	alignment := request.Alignment
	if alignment == 0 {
		alignment = uint32(request.Type.Size())
	}
	indexes, err := e.selectScanRegionsLocked(request.Regions)
	if err != nil {
		return err
	}
	snapshots, err := e.readScanRegionsLocked(indexes)
	if err != nil {
		return err
	}
	state := &scanState{valueType: request.Type, alignment: alignment}
	size := uint64(request.Type.Size())
	for _, index := range indexes {
		region, data := e.regions[index], snapshots[index]
		first := uint64(0)
		if remainder := region.Start % alignment; remainder != 0 {
			first = uint64(alignment - remainder)
		}
		var slots uint64
		if first+size <= uint64(len(data)) {
			slots = (uint64(len(data))-first-size)/uint64(alignment) + 1
		}
		part := scanRegion{index: index, first: first, slots: slots, bitmap: make([]uint64, (slots+63)/64), previous: data}
		before := state.count
		for slot := uint64(0); slot < slots; slot++ {
			offset := first + slot*uint64(alignment)
			current, err := Decode(request.Type, data[offset:offset+size], e.byteOrder)
			if err != nil {
				return err
			}
			matched, err := initialMatch(request.Comparison, current, target)
			if err != nil {
				return err
			}
			if matched {
				state.count++
				if resultLimit > 0 && state.count > resultLimit {
					return fmt.Errorf("%w: limit %d", ErrTooManyResults, resultLimit)
				}
				part.bitmap[slot/64] |= uint64(1) << (slot % 64)
			}
		}
		// A region without candidates is never read again; drop its snapshot.
		if state.count > before {
			state.regions = append(state.regions, part)
		}
	}
	e.scan = state
	return nil
}

func (e *Engine) NextScan(request NextScanRequest) ([]Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.scan != nil && e.scan.count > e.maxResults {
		return nil, fmt.Errorf("%w: use RefineScan for paged sessions", ErrTooManyResults)
	}
	if err := e.refineScanLocked(request); err != nil {
		return nil, err
	}
	return e.matchesLocked()
}

// RefineScan compares live memory against the last successful scan, then
// replaces that baseline. Failed scans leave the previous session intact.
func (e *Engine) RefineScan(request NextScanRequest) (ScanSummary, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refineScanLocked(request); err != nil {
		return ScanSummary{}, err
	}
	return e.summaryLocked(), nil
}

func (e *Engine) refineScanLocked(request NextScanRequest) error {
	if e.scan == nil {
		return ErrScanNotStarted
	}
	if !request.Comparison.Valid() || request.Comparison == CompareUnknown {
		return fmt.Errorf("invalid next-scan comparison %d", request.Comparison)
	}
	target, err := validateScanTarget(e.scan.valueType, request.Comparison, request.Value)
	if err != nil {
		return err
	}
	// Empty sessions cannot acquire candidates again. Preserve the legacy
	// behavior of refining an empty set without touching guest memory.
	if e.scan.count == 0 {
		return nil
	}
	indexes := make([]int, 0, len(e.scan.regions))
	for _, part := range e.scan.regions {
		indexes = append(indexes, part.index)
	}
	snapshots, err := e.readScanRegionsLocked(indexes)
	if err != nil {
		return err
	}
	state := &scanState{valueType: e.scan.valueType, alignment: e.scan.alignment}
	size := uint64(state.valueType.Size())
	for _, old := range e.scan.regions {
		data := snapshots[old.index]
		part := scanRegion{index: old.index, first: old.first, slots: old.slots, bitmap: make([]uint64, len(old.bitmap)), previous: data}
		before := state.count
		for wordIndex, word := range old.bitmap {
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				slot := uint64(wordIndex)*64 + uint64(bit)
				offset := part.first + slot*uint64(state.alignment)
				raw, before := data[offset:offset+size], old.previous[offset:offset+size]
				current, err := Decode(state.valueType, raw, e.byteOrder)
				if err != nil {
					return err
				}
				previous, err := Decode(state.valueType, before, e.byteOrder)
				if err != nil {
					return err
				}
				matched, err := nextMatch(request.Comparison, current, previous, target, bytes.Equal(raw, before))
				if err != nil {
					return err
				}
				if matched {
					part.bitmap[wordIndex] |= uint64(1) << bit
					state.count++
				}
				word &= word - 1
			}
		}
		if state.count > before {
			state.regions = append(state.regions, part)
		}
	}
	e.scan = state
	return nil
}

func (e *Engine) summaryLocked() ScanSummary {
	return ScanSummary{Type: e.scan.valueType, Total: e.scan.count}
}

func (e *Engine) ScanSummary() (ScanSummary, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.scan == nil {
		return ScanSummary{}, ErrScanNotStarted
	}
	return e.summaryLocked(), nil
}

// ScanPage returns at most MaxScanPageSize values read now from guest memory.
func (e *Engine) ScanPage(offset, limit int) (ScanPage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.scan == nil {
		return ScanPage{}, ErrScanNotStarted
	}
	if offset < 0 || limit <= 0 || limit > MaxScanPageSize {
		return ScanPage{}, fmt.Errorf("invalid scan page offset %d or size %d", offset, limit)
	}
	matches, err := e.scanMatchesLocked(offset, limit, true)
	return ScanPage{ScanSummary: e.summaryLocked(), Offset: offset, Matches: matches}, err
}

func (e *Engine) ScanResults() ([]Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.matchesLocked()
}

func (e *Engine) ResetScan() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scan = nil
}

func (e *Engine) matchesLocked() ([]Match, error) {
	if e.scan == nil {
		return nil, ErrScanNotStarted
	}
	if e.scan.count > e.maxResults {
		return nil, fmt.Errorf("%w: use ScanPage", ErrTooManyResults)
	}
	return e.scanMatchesLocked(0, e.scan.count, false)
}

func (e *Engine) scanMatchesLocked(offset, limit int, live bool) ([]Match, error) {
	output := make([]Match, 0, min(limit, max(0, e.scan.count-offset)))
	if limit == 0 || offset >= e.scan.count {
		return output, nil
	}
	skip := offset
	size := uint64(e.scan.valueType.Size())
	var buffer [8]byte
	for _, part := range e.scan.regions {
		region := e.regions[part.index]
		for wordIndex, word := range part.bitmap {
			count := bits.OnesCount64(word)
			if skip >= count {
				skip -= count
				continue
			}
			for word != 0 {
				if len(output) == limit {
					return output, nil
				}
				bit := bits.TrailingZeros64(word)
				word &= word - 1
				if skip > 0 {
					skip--
					continue
				}
				position := part.first + (uint64(wordIndex)*64+uint64(bit))*uint64(e.scan.alignment)
				address := region.Start + uint32(position)
				raw := part.previous[position : position+size]
				if live {
					raw = buffer[:size]
					if err := e.memory.ReadMemory(address, raw); err != nil {
						return nil, fmt.Errorf("read scan result 0x%08x: %w", address, err)
					}
				}
				value, err := Decode(e.scan.valueType, raw, e.byteOrder)
				if err != nil {
					return nil, err
				}
				output = append(output, Match{Address: address, Region: region.Name, Value: value})
			}
		}
	}
	return output, nil
}

func validateScanTarget(
	valueType ValueType,
	comparison Comparison,
	target *Value,
) (*Value, error) {
	if comparison.needsTarget() {
		if target == nil {
			return nil, fmt.Errorf("scan comparison %d requires a target value", comparison)
		}
		if target.Type != valueType {
			return nil, fmt.Errorf(
				"scan target type %d does not match scan type %d",
				target.Type,
				valueType,
			)
		}
		if err := target.Validate(); err != nil {
			return nil, err
		}
		return target, nil
	}
	if target != nil {
		return nil, fmt.Errorf("scan comparison %d does not accept a target value", comparison)
	}
	return nil, nil
}

func (e *Engine) selectScanRegionsLocked(names []string) ([]int, error) {
	selected := make([]int, 0)
	if len(names) == 0 {
		for index, region := range e.regions {
			if region.Scannable {
				selected = append(selected, index)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("no regions are marked scannable")
		}
		return selected, nil
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		found := false
		for index, region := range e.regions {
			if region.Name == name {
				selected = append(selected, index)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown cheat scan region %q", name)
		}
		seen[name] = true
	}
	return selected, nil
}

func (e *Engine) readScanRegionsLocked(
	indexes []int,
) (map[int][]byte, error) {
	var total uint64
	for _, index := range indexes {
		total += uint64(e.regions[index].Size)
		if total > e.maxScanBytes {
			return nil, fmt.Errorf(
				"%w: selected %d bytes, limit %d",
				ErrScanLimitExceeded,
				total,
				e.maxScanBytes,
			)
		}
	}
	output := make(map[int][]byte, len(indexes))
	for _, index := range indexes {
		region := e.regions[index]
		data := make([]byte, int(region.Size))
		if err := e.memory.ReadMemory(region.Start, data); err != nil {
			return nil, fmt.Errorf(
				"scan cheat region %q at 0x%08x: %w",
				region.Name,
				region.Start,
				err,
			)
		}
		output[index] = data
	}
	return output, nil
}

func initialMatch(
	comparison Comparison,
	current Value,
	target *Value,
) (bool, error) {
	switch comparison {
	case CompareUnknown:
		return true, nil
	case CompareEqual, CompareNotEqual, CompareGreater, CompareLess:
		return targetMatch(comparison, current, *target)
	default:
		return false, fmt.Errorf("comparison %d requires a previous scan", comparison)
	}
}

func nextMatch(
	comparison Comparison,
	current Value,
	previous Value,
	target *Value,
	sameBits bool,
) (bool, error) {
	switch comparison {
	case CompareEqual, CompareNotEqual, CompareGreater, CompareLess:
		return targetMatch(comparison, current, *target)
	case CompareChanged:
		return !sameBits, nil
	case CompareUnchanged:
		return sameBits, nil
	case CompareIncreased:
		result, ordered, err := compareOrdered(current, previous)
		return result > 0 && ordered, err
	case CompareDecreased:
		result, ordered, err := compareOrdered(current, previous)
		return result < 0 && ordered, err
	default:
		return false, fmt.Errorf("invalid next-scan comparison %d", comparison)
	}
}

func targetMatch(
	comparison Comparison,
	current Value,
	target Value,
) (bool, error) {
	result, ordered, err := compareOrdered(current, target)
	if err != nil {
		return false, err
	}
	switch comparison {
	case CompareEqual:
		return ordered && result == 0, nil
	case CompareNotEqual:
		return !ordered || result != 0, nil
	case CompareGreater:
		return ordered && result > 0, nil
	case CompareLess:
		return ordered && result < 0, nil
	default:
		return false, fmt.Errorf("invalid target comparison %d", comparison)
	}
}

func compareOrdered(left, right Value) (result int, ordered bool, err error) {
	if left.Type != right.Type {
		return 0, false, fmt.Errorf(
			"cannot compare value types %d and %d",
			left.Type,
			right.Type,
		)
	}
	if err := left.Validate(); err != nil {
		return 0, false, err
	}
	if err := right.Validate(); err != nil {
		return 0, false, err
	}
	if left.Type.floating() {
		var a, b float64
		if left.Type == TypeFloat32 {
			a = float64(math.Float32frombits(uint32(left.Bits)))
			b = float64(math.Float32frombits(uint32(right.Bits)))
		} else {
			a = math.Float64frombits(left.Bits)
			b = math.Float64frombits(right.Bits)
		}
		if math.IsNaN(a) || math.IsNaN(b) {
			return 0, false, nil
		}
		return compare(a, b), true, nil
	}
	if left.Type.signed() {
		return compare(signedValue(left), signedValue(right)), true, nil
	}
	return compare(left.Bits, right.Bits), true, nil
}

func signedValue(value Value) int64 {
	switch value.Type.Size() {
	case 1:
		return int64(int8(value.Bits))
	case 2:
		return int64(int16(value.Bits))
	case 4:
		return int64(int32(value.Bits))
	default:
		return int64(value.Bits)
	}
}

func compare[T ~int64 | ~uint64 | ~float64](left, right T) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
