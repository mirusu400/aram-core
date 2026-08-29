package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"sort"
)

// QualcommClockRegimeWindowSize covers the sparse clock-control apertures used
// below 0x90007000. Reserved gaps are kept unmapped by the device even though
// the bus owns the enclosing window.
const QualcommClockRegimeWindowSize = 0x7000

var ErrQualcommClockRegimeMMIO = errors.New("unsupported Qualcomm clock-regime register")

// QualcommClockRegime is the sparse word-addressed legacy clock-regime
// register file. Current firmware programs dividers, sources, and gate values
// through read/modify/write sequences, so words inside an evidenced aperture
// are persistent latches.
// Oscillator lock timing and derived device frequencies remain separate from
// this register-storage layer.
type QualcommClockRegime struct {
	registers                   [QualcommClockRegimeWindowSize / 4]uint32
	sleepControllers            map[uint32]struct{}
	counters                    []QualcommClockRegimeCounterConfig
	counterPhases               []uint64
	comparators                 []QualcommClockRegimeComparatorConfig
	comparatorPhases            []uint64
	interruptController         *QualcommInterruptController
	vectoredInterruptController *QualcommVectoredInterruptController
}

// QualcommClockRegimeCounterConfig derives a free-running hardware counter
// from retired guest instructions. Bits controls the visible wrap width while
// InstructionsPerSecond and CounterHz form the deterministic virtual-time
// conversion used by ClockedRunner.
type QualcommClockRegimeCounterConfig struct {
	Offset                uint32
	InstructionsPerSecond uint64
	CounterHz             uint64
	Bits                  uint8
}

// QualcommClockRegimeComparatorConfig describes one field-packed sleep-timer
// bank. Qualcomm parts commonly share counter and match words between several
// timers, so masks and register locations are profile data rather than fixed
// device constants. Each set bit in EventMask maps, from least to greatest, to
// one match register beginning at MatchBaseOffset.
type QualcommClockRegimeComparatorConfig struct {
	CounterOffset         uint32
	CounterMask           uint32
	InstructionsPerSecond uint64
	CounterHz             uint64
	CounterModulus        uint32
	MatchBaseOffset       uint32
	MatchStride           uint32
	MatchMask             uint32
	EnableOffset          uint32
	StatusOffset          uint32
	AcknowledgeOffset     uint32
	EventMask             uint32
	InterruptSource       uint8
	UseVectoredController bool
}

// QualcommClockRegimeConfig declares the optional state machines layered on
// top of the sparse legacy clock-register latch bank.
type QualcommClockRegimeConfig struct {
	SleepControllers            []uint32
	Counters                    []QualcommClockRegimeCounterConfig
	Comparators                 []QualcommClockRegimeComparatorConfig
	InterruptController         *QualcommInterruptController
	VectoredInterruptController *QualcommVectoredInterruptController
}

func NewQualcommClockRegime() *QualcommClockRegime {
	return &QualcommClockRegime{}
}

// NewQualcommClockRegimeWithSleepControllers enables the legacy sleep-control
// state machine only at profile-declared register-block offsets. A stop
// command of zero at base+0x30 transitions base+0x24 to the stopped state 1,
// matching the contract observed in firmware that uses this register layout.
func NewQualcommClockRegimeWithSleepControllers(offsets []uint32) (*QualcommClockRegime, error) {
	return NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		SleepControllers: offsets,
	})
}

// NewQualcommClockRegimeWithConfig constructs the profile-declared dynamic
// register behavior. Configuration order is intentionally normalized so that
// checkpoint identity does not depend on profile slice ordering.
func NewQualcommClockRegimeWithConfig(config QualcommClockRegimeConfig) (*QualcommClockRegime, error) {
	if err := validateQualcommClockRegimeConfig(config); err != nil {
		return nil, err
	}
	device := NewQualcommClockRegime()
	device.sleepControllers = make(map[uint32]struct{}, len(config.SleepControllers))
	for _, offset := range config.SleepControllers {
		device.sleepControllers[offset] = struct{}{}
	}
	device.counters = append([]QualcommClockRegimeCounterConfig(nil), config.Counters...)
	sort.Slice(device.counters, func(i, j int) bool {
		return device.counters[i].Offset < device.counters[j].Offset
	})
	device.counterPhases = make([]uint64, len(device.counters))
	device.comparators = append([]QualcommClockRegimeComparatorConfig(nil), config.Comparators...)
	sort.Slice(device.comparators, func(i, j int) bool {
		left, right := device.comparators[i], device.comparators[j]
		if left.CounterOffset != right.CounterOffset {
			return left.CounterOffset < right.CounterOffset
		}
		return left.CounterMask < right.CounterMask
	})
	device.comparatorPhases = make([]uint64, len(device.comparators))
	device.interruptController = config.InterruptController
	device.vectoredInterruptController = config.VectoredInterruptController
	for _, comparator := range device.comparators {
		if comparator.UseVectoredController {
			if device.vectoredInterruptController == nil ||
				comparator.InterruptSource >= device.vectoredInterruptController.SourceCount() {
				return nil, fmt.Errorf(
					"Qualcomm clock-regime comparator interrupt source %d exceeds vectored controller",
					comparator.InterruptSource,
				)
			}
		} else if device.interruptController == nil {
			return nil, fmt.Errorf(
				"Qualcomm clock-regime comparator interrupt source %d has no controller",
				comparator.InterruptSource,
			)
		}
	}
	return device, nil
}

func validateQualcommClockRegimeConfig(config QualcommClockRegimeConfig) error {
	sleepControllers := make(map[uint32]struct{}, len(config.SleepControllers))
	for _, offset := range config.SleepControllers {
		if !validQualcommClockRegimeSleepControllerOffset(offset) {
			return fmt.Errorf("sleep-controller offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := sleepControllers[offset]; duplicate {
			return fmt.Errorf("duplicate sleep-controller offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		sleepControllers[offset] = struct{}{}
	}
	const maximumClockHz = uint64(1) << 48
	counters := make(map[uint32]struct{}, len(config.Counters))
	for _, counter := range config.Counters {
		if !isQualcommClockRegimeOffset(counter.Offset) ||
			counter.InstructionsPerSecond == 0 || counter.CounterHz == 0 ||
			counter.InstructionsPerSecond > maximumClockHz ||
			counter.CounterHz > counter.InstructionsPerSecond ||
			counter.Bits == 0 || counter.Bits > 32 {
			return fmt.Errorf("free-running counter at 0x%x: %w", counter.Offset, ErrInvalidRegion)
		}
		if _, duplicate := counters[counter.Offset]; duplicate {
			return fmt.Errorf("duplicate free-running counter at 0x%x: %w", counter.Offset, ErrInvalidRegion)
		}
		for base := range sleepControllers {
			if counter.Offset == base+qualcommClockRegimeSleepStatusOffset ||
				counter.Offset == base+qualcommClockRegimeSleepCommandOffset {
				return fmt.Errorf("counter and sleep controller overlap at 0x%x: %w", counter.Offset, ErrInvalidRegion)
			}
		}
		counters[counter.Offset] = struct{}{}
	}
	comparators := append([]QualcommClockRegimeComparatorConfig(nil), config.Comparators...)
	for index, comparator := range comparators {
		if !validQualcommClockRegimeComparatorConfig(comparator) {
			return fmt.Errorf(
				"sleep-timer comparator at 0x%x mask 0x%x: %w",
				comparator.CounterOffset,
				comparator.CounterMask,
				ErrInvalidRegion,
			)
		}
		if _, overlap := counters[comparator.CounterOffset]; overlap {
			return fmt.Errorf(
				"free-running counter and sleep-timer comparator overlap at 0x%x: %w",
				comparator.CounterOffset,
				ErrInvalidRegion,
			)
		}
		for previousIndex := 0; previousIndex < index; previousIndex++ {
			previous := comparators[previousIndex]
			if comparator.CounterOffset == previous.CounterOffset &&
				comparator.CounterMask&previous.CounterMask != 0 {
				return fmt.Errorf("overlapping sleep-timer counter fields: %w", ErrInvalidRegion)
			}
			if comparator.EnableOffset == previous.EnableOffset &&
				comparator.EventMask&previous.EventMask != 0 {
				return fmt.Errorf("overlapping sleep-timer enable fields: %w", ErrInvalidRegion)
			}
			if comparator.StatusOffset == previous.StatusOffset &&
				comparator.EventMask&previous.EventMask != 0 {
				return fmt.Errorf("overlapping sleep-timer status fields: %w", ErrInvalidRegion)
			}
			leftCount := bits.OnesCount32(comparator.EventMask)
			rightCount := bits.OnesCount32(previous.EventMask)
			for left := 0; left < leftCount; left++ {
				leftOffset := comparator.MatchBaseOffset + uint32(left)*comparator.MatchStride
				for right := 0; right < rightCount; right++ {
					rightOffset := previous.MatchBaseOffset + uint32(right)*previous.MatchStride
					if leftOffset == rightOffset && comparator.MatchMask&previous.MatchMask != 0 {
						return fmt.Errorf("overlapping sleep-timer match fields: %w", ErrInvalidRegion)
					}
				}
			}
		}
	}
	return nil
}

func validQualcommClockRegimeComparatorConfig(config QualcommClockRegimeComparatorConfig) bool {
	const maximumClockHz = uint64(1) << 48
	if !isQualcommClockRegimeOffset(config.CounterOffset) ||
		!isQualcommClockRegimeOffset(config.MatchBaseOffset) ||
		!isQualcommClockRegimeOffset(config.EnableOffset) ||
		!isQualcommClockRegimeOffset(config.StatusOffset) ||
		!isQualcommClockRegimeOffset(config.AcknowledgeOffset) ||
		!isContiguousQualcommClockRegimeMask(config.CounterMask) ||
		!isContiguousQualcommClockRegimeMask(config.MatchMask) ||
		config.InstructionsPerSecond == 0 || config.CounterHz == 0 ||
		config.InstructionsPerSecond > maximumClockHz ||
		config.CounterHz > config.InstructionsPerSecond ||
		config.CounterModulus < 2 || config.EventMask == 0 ||
		config.MatchStride < 4 || config.MatchStride%4 != 0 ||
		config.InterruptSource >= 64 {
		return false
	}
	counterBits := bits.OnesCount32(config.CounterMask)
	matchBits := bits.OnesCount32(config.MatchMask)
	if counterBits < 32 && config.CounterModulus > uint32(1)<<counterBits ||
		matchBits < 32 && config.CounterModulus > uint32(1)<<matchBits {
		return false
	}
	matchCount := bits.OnesCount32(config.EventMask)
	lastMatch := uint64(config.MatchBaseOffset) + uint64(matchCount-1)*uint64(config.MatchStride)
	return lastMatch <= uint64(^uint32(0)) && isQualcommClockRegimeOffset(uint32(lastMatch))
}

func isContiguousQualcommClockRegimeMask(mask uint32) bool {
	if mask == 0 {
		return false
	}
	shifted := mask >> bits.TrailingZeros32(mask)
	return shifted&(shifted+1) == 0
}

const (
	qualcommClockRegimeSleepStatusOffset  = 0x24
	qualcommClockRegimeSleepCommandOffset = 0x30
	qualcommClockRegimeSleepStopCommand   = 0
	qualcommClockRegimeSleepStoppedStatus = 1
)

func validQualcommClockRegimeSleepControllerOffset(offset uint32) bool {
	return isQualcommClockRegimeOffset(offset) &&
		isQualcommClockRegimeOffset(offset+qualcommClockRegimeSleepStatusOffset) &&
		isQualcommClockRegimeOffset(offset+qualcommClockRegimeSleepCommandOffset)
}

var qualcommClockRegimeApertures = [...]struct {
	start uint32
	end   uint32
}{
	// MSM6550 GPIO occupies the complete +0x0400..+0x04fc block. Runtime
	// clock/pin setup uses ordinary read-modify-write transactions at +0x404
	// as well as the later status words previously observed from +0x480.
	{0x0400, 0x0500},
	// The adjacent MSM6550 sleep-control block spans +0x0500..+0x06fc;
	// source and divider setup also performs RMW accesses at +0x604.
	{0x0500, 0x0700},
	{0x1000, 0x1100},
	{0x1400, 0x1500},
	{0x1800, 0x1900},
	{0x1900, 0x1a00},
	{0x2000, 0x2100},
	{0x2400, 0x2600},
	// DA05's AMSS clock initializer continues the adjacent register bank into
	// the +0x2600 page and performs ordinary read/modify/write at +0x262c.
	{0x2600, 0x2700},
	// A late USB_TDI sub-block is initialized through the +0x2940 register
	// bank. Keep the evidenced page separate from the unmodeled FIFO/status
	// portions of the much larger USB aperture.
	{0x2900, 0x2a00},
	{0x3080, 0x3100},
	{0x4800, 0x4900},
	{0x4900, 0x4a00},
	{0x4d00, 0x4e00},
	{0x5000, 0x6000},
	{0x6000, 0x6100},
}

func isQualcommClockRegimeOffset(offset uint32) bool {
	if offset%4 != 0 {
		return false
	}
	for _, aperture := range qualcommClockRegimeApertures {
		if offset >= aperture.start && offset < aperture.end {
			return true
		}
	}
	return false
}

func (d *QualcommClockRegime) Reset() error {
	d.registers = [QualcommClockRegimeWindowSize / 4]uint32{}
	clear(d.counterPhases)
	clear(d.comparatorPhases)
	return nil
}

func (d *QualcommClockRegime) Read(offset uint32, width Width) (uint32, error) {
	if width != Width32 || !isQualcommClockRegimeOffset(offset) {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommClockRegimeMMIO, width*8, offset)
	}
	return d.registers[offset/4], nil
}

func (d *QualcommClockRegime) Write(offset uint32, width Width, value uint32) error {
	if width != Width32 || !isQualcommClockRegimeOffset(offset) {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrQualcommClockRegimeMMIO, width*8, value, offset,
		)
	}
	d.registers[offset/4] = value
	for _, comparator := range d.comparators {
		if offset == comparator.AcknowledgeOffset {
			d.registers[comparator.StatusOffset/4] &^= value & comparator.EventMask
		}
	}
	for base := range d.sleepControllers {
		if offset == base+qualcommClockRegimeSleepCommandOffset &&
			value == qualcommClockRegimeSleepStopCommand {
			d.registers[(base+qualcommClockRegimeSleepStatusOffset)/4] =
				qualcommClockRegimeSleepStoppedStatus
			break
		}
	}
	return nil
}

// Advance implements ClockedDevice for profile-declared free-running counters
// and field-packed sleep-timer comparators. Each clock retains a fractional
// phase so execution sliced into different runner quanta produces exactly the
// same guest-visible value and interrupt sequence.
func (d *QualcommClockRegime) Advance(retiredInstructions uint64) error {
	if retiredInstructions == 0 {
		return nil
	}
	for index, counter := range d.counters {
		high, low := bits.Mul64(retiredInstructions, counter.CounterHz)
		low, carry := bits.Add64(low, d.counterPhases[index], 0)
		high, carry = bits.Add64(high, 0, carry)
		if carry != 0 || high >= counter.InstructionsPerSecond {
			return fmt.Errorf("Qualcomm clock-regime counter advance overflow at 0x%x", counter.Offset)
		}
		increments, remainder := bits.Div64(high, low, counter.InstructionsPerSecond)
		d.counterPhases[index] = remainder
		mask := uint32(^uint32(0))
		if counter.Bits < 32 {
			mask = uint32(1)<<counter.Bits - 1
		}
		d.registers[counter.Offset/4] =
			(d.registers[counter.Offset/4] + uint32(increments)) & mask
	}
	for index, comparator := range d.comparators {
		incrementHigh, increments, remainder, err := qualcommClockRegimeTicks(
			retiredInstructions,
			comparator.CounterHz,
			comparator.InstructionsPerSecond,
			d.comparatorPhases[index],
		)
		if err != nil {
			return fmt.Errorf(
				"Qualcomm clock-regime comparator advance at 0x%x: %w",
				comparator.CounterOffset,
				err,
			)
		}
		d.comparatorPhases[index] = remainder
		if incrementHigh == 0 && increments == 0 {
			continue
		}
		counterWord := d.registers[comparator.CounterOffset/4]
		previous := qualcommClockRegimeField(counterWord, comparator.CounterMask)
		incrementModulo := qualcommClockRegimeIncrementModulo(
			incrementHigh,
			increments,
			comparator.CounterModulus,
		)
		current := uint32(
			(uint64(previous) + uint64(incrementModulo)) % uint64(comparator.CounterModulus),
		)
		d.registers[comparator.CounterOffset/4] = qualcommClockRegimeSetField(
			counterWord,
			comparator.CounterMask,
			current,
		)

		enabled := d.registers[comparator.EnableOffset/4] & comparator.EventMask
		pending := d.registers[comparator.StatusOffset/4] & comparator.EventMask
		newEvents := uint32(0)
		remainingEvents := comparator.EventMask
		for matchIndex := uint32(0); remainingEvents != 0; matchIndex++ {
			eventBit := uint32(1) << bits.TrailingZeros32(remainingEvents)
			remainingEvents &^= eventBit
			if enabled&eventBit == 0 || pending&eventBit != 0 {
				continue
			}
			matchOffset := comparator.MatchBaseOffset + matchIndex*comparator.MatchStride
			target := qualcommClockRegimeField(
				d.registers[matchOffset/4],
				comparator.MatchMask,
			)
			if target >= comparator.CounterModulus {
				continue
			}
			distance := uint64(
				(target + comparator.CounterModulus - previous) % comparator.CounterModulus,
			)
			if distance == 0 {
				distance = uint64(comparator.CounterModulus)
			}
			if incrementHigh != 0 || increments >= distance {
				newEvents |= eventBit
			}
		}
		if newEvents == 0 {
			continue
		}
		d.registers[comparator.StatusOffset/4] |= newEvents
		if comparator.UseVectoredController {
			err = d.vectoredInterruptController.PulseSource(comparator.InterruptSource)
		} else {
			err = d.interruptController.PulseSource(comparator.InterruptSource)
		}
		if err != nil {
			return fmt.Errorf(
				"signal Qualcomm clock-regime comparator interrupt source %d: %w",
				comparator.InterruptSource,
				err,
			)
		}
	}
	return nil
}

func qualcommClockRegimeTicks(
	retiredInstructions, clockHz, instructionsPerSecond, phase uint64,
) (uint64, uint64, uint64, error) {
	high, low := bits.Mul64(retiredInstructions, clockHz)
	low, carry := bits.Add64(low, phase, 0)
	high, carry = bits.Add64(high, 0, carry)
	if carry != 0 {
		return 0, 0, 0, fmt.Errorf("clock conversion overflow")
	}
	quotientHigh, remainder := bits.Div64(0, high, instructionsPerSecond)
	quotientLow, remainder := bits.Div64(remainder, low, instructionsPerSecond)
	return quotientHigh, quotientLow, remainder, nil
}

func qualcommClockRegimeIncrementModulo(high, low uint64, modulus uint32) uint32 {
	modulus64 := uint64(modulus)
	twoTo64Modulo := (^uint64(0)%modulus64 + 1) % modulus64
	return uint32(
		((high%modulus64)*twoTo64Modulo + low%modulus64) % modulus64,
	)
}

func qualcommClockRegimeField(word, mask uint32) uint32 {
	return (word & mask) >> bits.TrailingZeros32(mask)
}

func qualcommClockRegimeSetField(word, mask, value uint32) uint32 {
	return word&^mask | value<<bits.TrailingZeros32(mask)&mask
}

func (d *QualcommClockRegime) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("QCRG")
	_ = binary.Write(&output, binary.LittleEndian, uint32(3))
	for _, value := range d.registers {
		_ = binary.Write(&output, binary.LittleEndian, value)
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.counters)))
	for index, counter := range d.counters {
		_ = binary.Write(&output, binary.LittleEndian, counter.Offset)
		_ = binary.Write(&output, binary.LittleEndian, counter.InstructionsPerSecond)
		_ = binary.Write(&output, binary.LittleEndian, counter.CounterHz)
		_ = output.WriteByte(counter.Bits)
		_ = binary.Write(&output, binary.LittleEndian, d.counterPhases[index])
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.comparators)))
	for index, comparator := range d.comparators {
		writeQualcommClockRegimeComparatorConfig(&output, comparator)
		_ = binary.Write(&output, binary.LittleEndian, d.comparatorPhases[index])
	}
	return output.Bytes(), nil
}

func writeQualcommClockRegimeComparatorConfig(
	output *bytes.Buffer,
	config QualcommClockRegimeComparatorConfig,
) {
	for _, value := range []uint32{
		config.CounterOffset,
		config.CounterMask,
	} {
		_ = binary.Write(output, binary.LittleEndian, value)
	}
	_ = binary.Write(output, binary.LittleEndian, config.InstructionsPerSecond)
	_ = binary.Write(output, binary.LittleEndian, config.CounterHz)
	for _, value := range []uint32{
		config.CounterModulus,
		config.MatchBaseOffset,
		config.MatchStride,
		config.MatchMask,
		config.EnableOffset,
		config.StatusOffset,
		config.AcknowledgeOffset,
		config.EventMask,
	} {
		_ = binary.Write(output, binary.LittleEndian, value)
	}
	_ = output.WriteByte(config.InterruptSource)
	useVectored := byte(0)
	if config.UseVectoredController {
		useVectored = 1
	}
	_ = output.WriteByte(useVectored)
}

func (d *QualcommClockRegime) LoadState(state []byte) error {
	return d.loadState(state, false)
}

// LoadStateSubset accepts the former v1 latch-only state and v2/v3 states whose
// dynamic device set is a subset of the current profile. Newly declared clocks
// keep their restored register values and start with zero fractional phase.
func (d *QualcommClockRegime) LoadStateSubset(state []byte) error {
	return d.loadState(state, true)
}

func (d *QualcommClockRegime) loadState(state []byte, allowCounterSubset bool) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QCRG" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil {
		return ErrInvalidState
	}
	var registers [QualcommClockRegimeWindowSize / 4]uint32
	for index := range registers {
		if binary.Read(reader, binary.LittleEndian, &registers[index]) != nil {
			return ErrInvalidState
		}
	}
	phases := make([]uint64, len(d.counters))
	comparatorPhases := make([]uint64, len(d.comparators))
	if version == 1 {
		if !allowCounterSubset || reader.Len() != 0 {
			return ErrInvalidState
		}
	} else if version == 2 || version == 3 {
		var count uint32
		if binary.Read(reader, binary.LittleEndian, &count) != nil || count > uint32(len(d.registers)) {
			return ErrInvalidState
		}
		seen := make(map[uint32]struct{}, count)
		for index := uint32(0); index < count; index++ {
			var saved QualcommClockRegimeCounterConfig
			var phase uint64
			if binary.Read(reader, binary.LittleEndian, &saved.Offset) != nil ||
				binary.Read(reader, binary.LittleEndian, &saved.InstructionsPerSecond) != nil ||
				binary.Read(reader, binary.LittleEndian, &saved.CounterHz) != nil ||
				binary.Read(reader, binary.LittleEndian, &saved.Bits) != nil ||
				binary.Read(reader, binary.LittleEndian, &phase) != nil ||
				phase >= saved.InstructionsPerSecond {
				return ErrInvalidState
			}
			if _, duplicate := seen[saved.Offset]; duplicate {
				return ErrInvalidState
			}
			seen[saved.Offset] = struct{}{}
			currentIndex := sort.Search(len(d.counters), func(i int) bool {
				return d.counters[i].Offset >= saved.Offset
			})
			if currentIndex == len(d.counters) || d.counters[currentIndex] != saved {
				return ErrInvalidState
			}
			phases[currentIndex] = phase
		}
		if !allowCounterSubset && int(count) != len(d.counters) {
			return ErrInvalidState
		}
		if version == 2 {
			if !allowCounterSubset && len(d.comparators) != 0 || reader.Len() != 0 {
				return ErrInvalidState
			}
		} else {
			var comparatorCount uint32
			if binary.Read(reader, binary.LittleEndian, &comparatorCount) != nil ||
				comparatorCount > uint32(len(d.registers)) {
				return ErrInvalidState
			}
			seenComparators := make(map[[2]uint32]struct{}, comparatorCount)
			for index := uint32(0); index < comparatorCount; index++ {
				saved, phase, err := readQualcommClockRegimeComparatorConfig(reader)
				if err != nil || phase >= saved.InstructionsPerSecond {
					return ErrInvalidState
				}
				identity := [2]uint32{saved.CounterOffset, saved.CounterMask}
				if _, duplicate := seenComparators[identity]; duplicate {
					return ErrInvalidState
				}
				seenComparators[identity] = struct{}{}
				currentIndex := sort.Search(len(d.comparators), func(i int) bool {
					current := d.comparators[i]
					return current.CounterOffset > saved.CounterOffset ||
						current.CounterOffset == saved.CounterOffset && current.CounterMask >= saved.CounterMask
				})
				if currentIndex == len(d.comparators) || d.comparators[currentIndex] != saved {
					return ErrInvalidState
				}
				comparatorPhases[currentIndex] = phase
			}
			if (!allowCounterSubset && int(comparatorCount) != len(d.comparators)) || reader.Len() != 0 {
				return ErrInvalidState
			}
		}
	} else {
		return ErrInvalidState
	}
	d.registers = registers
	copy(d.counterPhases, phases)
	copy(d.comparatorPhases, comparatorPhases)
	return nil
}

func readQualcommClockRegimeComparatorConfig(
	reader *bytes.Reader,
) (QualcommClockRegimeComparatorConfig, uint64, error) {
	var config QualcommClockRegimeComparatorConfig
	var useVectored byte
	var phase uint64
	fields := []any{
		&config.CounterOffset,
		&config.CounterMask,
		&config.InstructionsPerSecond,
		&config.CounterHz,
		&config.CounterModulus,
		&config.MatchBaseOffset,
		&config.MatchStride,
		&config.MatchMask,
		&config.EnableOffset,
		&config.StatusOffset,
		&config.AcknowledgeOffset,
		&config.EventMask,
		&config.InterruptSource,
		&useVectored,
		&phase,
	}
	for _, field := range fields {
		if binary.Read(reader, binary.LittleEndian, field) != nil {
			return QualcommClockRegimeComparatorConfig{}, 0, ErrInvalidState
		}
	}
	if useVectored > 1 || !validQualcommClockRegimeComparatorConfig(config) {
		return QualcommClockRegimeComparatorConfig{}, 0, ErrInvalidState
	}
	config.UseVectoredController = useVectored == 1
	return config, phase, nil
}

var (
	_ Device               = (*QualcommClockRegime)(nil)
	_ StatefulDevice       = (*QualcommClockRegime)(nil)
	_ SubsetStatefulDevice = (*QualcommClockRegime)(nil)
	_ ClockedDevice        = (*QualcommClockRegime)(nil)
)
