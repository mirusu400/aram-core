package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type RNGStreamState struct {
	Name      string
	Algorithm RNGAlgorithm
	State     [4]uint64
	Draws     uint64
}

type RandomState struct {
	Seed       uint64
	MaxStreams uint32
	Streams    []RNGStreamState
}

type rngStream struct {
	algorithm RNGAlgorithm
	state     [4]uint64
	draws     uint64
}

// RNGAlgorithm is save-state compatibility policy for a named stream.
// Adapters select an algorithm explicitly when an API requires a historical
// sequence, such as java.util.Random's 48-bit linear congruential generator.
type RNGAlgorithm string

const (
	RNGXoshiro256StarStar RNGAlgorithm = "xoshiro256**"
	RNGJava48             RNGAlgorithm = "java-lcg48"
	javaRandomMultiplier               = uint64(0x5deece66d)
	javaRandomMask                     = uint64(1<<48 - 1)
)

func (a RNGAlgorithm) valid() bool {
	return a == RNGXoshiro256StarStar || a == RNGJava48
}

// Random owns deterministic named streams so Java Random, C rand, and
// carrier-private APIs can remain independent.
type Random struct {
	seed       uint64
	maxStreams uint32
	streams    map[string]*rngStream
}

func NewRandom(seed uint64, maxStreams uint32) *Random {
	if maxStreams == 0 {
		maxStreams = DefaultMaxStreams
	}
	return &Random{
		seed:       seed,
		maxStreams: maxStreams,
		streams:    make(map[string]*rngStream),
	}
}

func (r *Random) Uint64(name string) (uint64, error) {
	stream, err := r.stream(name)
	if err != nil {
		return 0, err
	}
	if stream.algorithm != RNGXoshiro256StarStar {
		return 0, fmt.Errorf(
			"%w: random stream %q uses %s",
			ErrInvalidState,
			name,
			stream.algorithm,
		)
	}
	if stream.draws == math.MaxUint64 {
		return 0, fmt.Errorf("%w: random stream %q exhausted", ErrLimitExceeded, name)
	}
	result := rotateLeft(stream.state[1]*5, 7) * 9
	t := stream.state[1] << 17
	stream.state[2] ^= stream.state[0]
	stream.state[3] ^= stream.state[1]
	stream.state[1] ^= stream.state[2]
	stream.state[0] ^= stream.state[3]
	stream.state[2] ^= t
	stream.state[3] = rotateLeft(stream.state[3], 45)
	stream.draws++
	return result, nil
}

// SetJavaSeed creates or resets a stream with java.util.Random-compatible
// seed scrambling. Its state remains owned and serialized by Random.
func (r *Random) SetJavaSeed(name string, seed int64) error {
	stream, err := r.ensureStream(name, RNGJava48)
	if err != nil {
		return err
	}
	stream.algorithm = RNGJava48
	stream.state = [4]uint64{JavaRandomSeed(seed)}
	stream.draws = 0
	return nil
}

// JavaRandomSeed converts a public java.util.Random seed to its scrambled
// 48-bit state. Adapters with many short-lived Random objects can serialize
// that state themselves without consuming an unbounded number of named
// service streams.
func JavaRandomSeed(seed int64) uint64 {
	return (uint64(seed) ^ javaRandomMultiplier) & javaRandomMask
}

// JavaRandomBits advances a serialized java.util.Random state and returns the
// requested high bits.
func JavaRandomBits(state *uint64, bits uint8) (uint32, error) {
	if state == nil || bits == 0 || bits > 32 {
		return 0, fmt.Errorf(
			"%w: Java random state %p bit count %d",
			ErrInvalidArgument,
			state,
			bits,
		)
	}
	*state = (*state*javaRandomMultiplier + 0xb) & javaRandomMask
	return uint32(*state >> (48 - bits)), nil
}

// JavaInt advances a java.util.Random-compatible stream and returns next(32).
func (r *Random) JavaInt(name string) (int32, error) {
	value, err := r.JavaBits(name, 32)
	return int32(value), err
}

// JavaBits advances a java.util.Random-compatible stream and returns the
// requested high bits from its 48-bit state.
func (r *Random) JavaBits(name string, bits uint8) (uint32, error) {
	if bits == 0 || bits > 32 {
		return 0, fmt.Errorf(
			"%w: Java random bit count %d",
			ErrInvalidArgument,
			bits,
		)
	}
	stream, err := r.ensureStream(name, RNGJava48)
	if err != nil {
		return 0, err
	}
	if stream.algorithm != RNGJava48 {
		return 0, fmt.Errorf(
			"%w: random stream %q uses %s",
			ErrInvalidState,
			name,
			stream.algorithm,
		)
	}
	if stream.draws == math.MaxUint64 {
		return 0, fmt.Errorf("%w: random stream %q exhausted", ErrLimitExceeded, name)
	}
	value, err := JavaRandomBits(&stream.state[0], bits)
	if err != nil {
		return 0, err
	}
	stream.draws++
	return value, nil
}

func (r *Random) Uint32(name string) (uint32, error) {
	value, err := r.Uint64(name)
	return uint32(value >> 32), err
}

func (r *Random) Intn(name string, limit uint32) (uint32, error) {
	if limit == 0 {
		return 0, fmt.Errorf("%w: random limit is zero", ErrInvalidArgument)
	}
	threshold := -limit % limit
	for {
		value, err := r.Uint32(name)
		if err != nil {
			return 0, err
		}
		if value >= threshold {
			return value % limit, nil
		}
	}
}

func (r *Random) stream(name string) (*rngStream, error) {
	stream, err := r.ensureStream(name, RNGXoshiro256StarStar)
	if err != nil {
		return nil, err
	}
	if stream.algorithm != RNGXoshiro256StarStar {
		return nil, fmt.Errorf(
			"%w: random stream %q uses %s",
			ErrInvalidState,
			name,
			stream.algorithm,
		)
	}
	return stream, nil
}

func (r *Random) ensureStream(name string, algorithm RNGAlgorithm) (*rngStream, error) {
	if strings.TrimSpace(name) == "" || len(name) > 64 ||
		strings.IndexByte(name, 0) >= 0 {
		return nil, fmt.Errorf("%w: invalid random stream name %q", ErrInvalidArgument, name)
	}
	if !algorithm.valid() {
		return nil, fmt.Errorf("%w: invalid random algorithm %q", ErrInvalidArgument, algorithm)
	}
	if stream := r.streams[name]; stream != nil {
		return stream, nil
	}
	if uint32(len(r.streams)) >= r.maxStreams {
		return nil, fmt.Errorf("%w: random stream count reached %d", ErrLimitExceeded, r.maxStreams)
	}
	seed := r.seed ^ hashString64(name)
	stream := &rngStream{algorithm: algorithm}
	if algorithm == RNGJava48 {
		stream.state[0] = 0x5deece66d
		r.streams[name] = stream
		return stream, nil
	}
	for index := range stream.state {
		seed += 0x9e3779b97f4a7c15
		value := seed
		value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
		value = (value ^ (value >> 27)) * 0x94d049bb133111eb
		stream.state[index] = value ^ (value >> 31)
	}
	if stream.state == [4]uint64{} {
		stream.state[0] = 1
	}
	r.streams[name] = stream
	return stream, nil
}

func (r *Random) Snapshot() RandomState {
	state := RandomState{Seed: r.seed, MaxStreams: r.maxStreams}
	names := make([]string, 0, len(r.streams))
	for name := range r.streams {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		stream := r.streams[name]
		state.Streams = append(state.Streams, RNGStreamState{
			Name:      name,
			Algorithm: stream.algorithm,
			State:     stream.state,
			Draws:     stream.draws,
		})
	}
	return state
}

func (r *Random) Restore(state RandomState) error {
	if state.MaxStreams == 0 || len(state.Streams) > int(state.MaxStreams) {
		return fmt.Errorf("%w: invalid random stream limit", ErrInvalidState)
	}
	streams := make(map[string]*rngStream, len(state.Streams))
	previous := ""
	for index, saved := range state.Streams {
		if strings.TrimSpace(saved.Name) == "" || len(saved.Name) > 64 ||
			strings.IndexByte(saved.Name, 0) >= 0 ||
			(index != 0 && saved.Name <= previous) ||
			!saved.Algorithm.valid() ||
			(saved.Algorithm == RNGXoshiro256StarStar && saved.State == [4]uint64{}) ||
			(saved.Algorithm == RNGJava48 &&
				(saved.State[0] >= uint64(1)<<48 ||
					saved.State[1] != 0 ||
					saved.State[2] != 0 ||
					saved.State[3] != 0)) {
			return fmt.Errorf("%w: invalid random stream %d", ErrInvalidState, index)
		}
		streams[saved.Name] = &rngStream{
			algorithm: saved.Algorithm,
			state:     saved.State,
			draws:     saved.Draws,
		}
		previous = saved.Name
	}
	r.seed = state.Seed
	r.maxStreams = state.MaxStreams
	r.streams = streams
	return nil
}

func rotateLeft(value uint64, count int) uint64 {
	return value<<count | value>>(64-count)
}

func hashString64(value string) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for index := 0; index < len(value); index++ {
		hash ^= uint64(value[index])
		hash *= prime
	}
	return hash
}
