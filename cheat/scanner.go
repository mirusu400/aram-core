package cheat

import (
	"bytes"
	"fmt"
	"math"
)

func (e *Engine) Scan(request ScanRequest) ([]Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !request.Type.Valid() {
		return nil, fmt.Errorf("invalid scan value type %d", request.Type)
	}
	if !request.Comparison.Valid() {
		return nil, fmt.Errorf("invalid scan comparison %d", request.Comparison)
	}
	if request.Comparison.needsPrevious() {
		return nil, fmt.Errorf(
			"comparison %d requires a previous scan",
			request.Comparison,
		)
	}
	target, err := validateScanTarget(request.Type, request.Comparison, request.Value)
	if err != nil {
		return nil, err
	}
	alignment := request.Alignment
	if alignment == 0 {
		alignment = uint32(request.Type.Size())
	}
	if alignment == 0 {
		return nil, fmt.Errorf("scan alignment is zero")
	}
	regionIndexes, err := e.selectScanRegionsLocked(request.Regions)
	if err != nil {
		return nil, err
	}
	snapshots, err := e.readScanRegionsLocked(regionIndexes)
	if err != nil {
		return nil, err
	}

	candidates := make([]scanCandidate, 0)
	valueSize := request.Type.Size()
	for _, regionIndex := range regionIndexes {
		region := e.regions[regionIndex]
		data := snapshots[regionIndex]
		first := uint32(0)
		if remainder := region.Start % alignment; remainder != 0 {
			first = alignment - remainder
		}
		for offset := uint64(first); offset+uint64(valueSize) <= uint64(len(data)); offset += uint64(alignment) {
			raw := data[offset : offset+uint64(valueSize)]
			current, decodeErr := Decode(request.Type, raw, e.byteOrder)
			if decodeErr != nil {
				return nil, decodeErr
			}
			matched, compareErr := initialMatch(
				request.Comparison,
				current,
				target,
			)
			if compareErr != nil {
				return nil, compareErr
			}
			if !matched {
				continue
			}
			if len(candidates) >= e.maxResults {
				return nil, fmt.Errorf(
					"%w: limit %d",
					ErrTooManyResults,
					e.maxResults,
				)
			}
			candidates = append(candidates, scanCandidate{
				address:  region.Start + uint32(offset),
				region:   regionIndex,
				previous: append([]byte(nil), raw...),
			})
		}
	}
	e.scan = &scanState{
		valueType:  request.Type,
		alignment:  alignment,
		candidates: candidates,
	}
	return e.matchesLocked()
}

func (e *Engine) NextScan(request NextScanRequest) ([]Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.scan == nil {
		return nil, ErrScanNotStarted
	}
	if !request.Comparison.Valid() || request.Comparison == CompareUnknown {
		return nil, fmt.Errorf("invalid next-scan comparison %d", request.Comparison)
	}
	target, err := validateScanTarget(
		e.scan.valueType,
		request.Comparison,
		request.Value,
	)
	if err != nil {
		return nil, err
	}
	regionIndexes := make([]int, 0)
	seen := make(map[int]bool)
	for _, candidate := range e.scan.candidates {
		if !seen[candidate.region] {
			seen[candidate.region] = true
			regionIndexes = append(regionIndexes, candidate.region)
		}
	}
	snapshots, err := e.readScanRegionsLocked(regionIndexes)
	if err != nil {
		return nil, err
	}

	valueSize := e.scan.valueType.Size()
	filtered := make([]scanCandidate, 0, len(e.scan.candidates))
	for _, candidate := range e.scan.candidates {
		region := e.regions[candidate.region]
		offset := uint64(candidate.address) - uint64(region.Start)
		data := snapshots[candidate.region]
		if offset+uint64(valueSize) > uint64(len(data)) {
			return nil, fmt.Errorf(
				"scan candidate 0x%08x escaped region %q",
				candidate.address,
				region.Name,
			)
		}
		raw := data[offset : offset+uint64(valueSize)]
		current, decodeErr := Decode(e.scan.valueType, raw, e.byteOrder)
		if decodeErr != nil {
			return nil, decodeErr
		}
		previous, decodeErr := Decode(
			e.scan.valueType,
			candidate.previous,
			e.byteOrder,
		)
		if decodeErr != nil {
			return nil, decodeErr
		}
		matched, compareErr := nextMatch(
			request.Comparison,
			current,
			previous,
			target,
			bytes.Equal(raw, candidate.previous),
		)
		if compareErr != nil {
			return nil, compareErr
		}
		if matched {
			candidate.previous = append(candidate.previous[:0], raw...)
			filtered = append(filtered, candidate)
		}
	}
	e.scan.candidates = filtered
	return e.matchesLocked()
}

func (e *Engine) ScanResults() ([]Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.scan == nil {
		return nil, ErrScanNotStarted
	}
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
	output := make([]Match, 0, len(e.scan.candidates))
	for _, candidate := range e.scan.candidates {
		value, err := Decode(e.scan.valueType, candidate.previous, e.byteOrder)
		if err != nil {
			return nil, err
		}
		output = append(output, Match{
			Address: candidate.address,
			Region:  e.regions[candidate.region].Name,
			Value:   value,
		})
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
