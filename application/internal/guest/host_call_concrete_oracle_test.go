package guest_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// These tests compare the public scalar/bulk dispatch contract, not the private
// representation of lazy flags. Exact New/NewJIT pointers are positive controls;
// wrappers deliberately remain distinct dynamic types.
var hostConcrete22Factories = []struct {
	name string
	new  func() *interpreter.Backend
}{
	{"New", interpreter.New},
	{"NewJIT", interpreter.NewJIT},
}

var hostConcrete22Fault = errors.New("host concrete oracle fault")

type hostConcrete22Outcome struct {
	ErrorType, ErrorText string
	Invalid, Closed      bool
	Permission, Fault    bool
	PanicType, PanicText string
}

func hostConcrete22Call(operation func() error) (out hostConcrete22Outcome) {
	defer func() {
		if value := recover(); value != nil {
			out.PanicType, out.PanicText = fmt.Sprintf("%T", value), fmt.Sprint(value)
		}
	}()
	if err := operation(); err != nil {
		out.ErrorType, out.ErrorText = fmt.Sprintf("%T", err), err.Error()
		out.Invalid, out.Closed = errors.Is(err, cpu.ErrInvalidAddress), errors.Is(err, cpu.ErrClosed)
		out.Permission, out.Fault = errors.Is(err, cpu.ErrPermissionDenied), errors.Is(err, hostConcrete22Fault)
	}
	return out
}

func hostConcrete22Check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func hostConcrete22Sentinel() cpu.HostCallFrame {
	var frame cpu.HostCallFrame
	for index := range frame.Registers {
		frame.Registers[index] = 0xa0000000 + uint32(index)
	}
	for index := range frame.Stack {
		frame.Stack[index] = 0xb0000000 + uint32(index)
		frame.Parameters[index] = 0xc0000000 + uint32(index)
	}
	frame.StackWords, frame.ParameterWords = 0xdead, 0xbeef
	return frame
}

type hostConcrete22Fixture struct {
	backend   *interpreter.Backend
	registers [17]uint32
	data      [512]byte
}

func hostConcrete22NewFixture(t *testing.T, factory func() *interpreter.Backend, mode cpu.Mode, lazy bool, lr uint32) hostConcrete22Fixture {
	t.Helper()
	fixture := hostConcrete22Fixture{backend: factory()}
	t.Cleanup(func() { _ = fixture.backend.Close() })
	b := fixture.backend
	hostConcrete22Check(t, b.Map(0x1000, 64, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	hostConcrete22Check(t, b.Map(0x3000, uint32(len(fixture.data)), cpu.PermissionRead|cpu.PermissionWrite))
	for index := range fixture.data {
		fixture.data[index] = byte(index*37 + 11)
	}
	hostConcrete22Check(t, b.WriteMemory(0x3000, fixture.data[:]))
	fixture.registers[cpu.RegisterCPSR] = 0x6000001f
	if mode == cpu.ModeThumb {
		fixture.registers[cpu.RegisterCPSR] |= cpu.StatusThumb
	}
	hostConcrete22Check(t, b.WriteRegister(cpu.RegisterCPSR, fixture.registers[cpu.RegisterCPSR]))
	for register := uint32(0); register < cpu.RegisterCPSR; register++ {
		fixture.registers[register] = 0x10203000 + register*0x111
	}
	fixture.registers[cpu.RegisterR0] = 0x7fffffff
	fixture.registers[cpu.RegisterSP] = 0x3000
	fixture.registers[cpu.RegisterLR] = lr
	fixture.registers[cpu.RegisterPC] = 0x1000
	for register := uint32(0); register < cpu.RegisterCPSR; register++ {
		hostConcrete22Check(t, b.WriteRegister(register, fixture.registers[register]))
	}
	// Literal ADDS r0,r0,#1 / ADDS r0,#1. Stop before a branch or any public
	// flags reader so capture/return, rather than fixture setup, resolves NZCV.
	code := []byte{0x01, 0x00, 0x90, 0xe2}
	step := uint32(4)
	if mode == cpu.ModeThumb {
		code, step = []byte{0x01, 0x30}, 2
	}
	hostConcrete22Check(t, b.WriteMemory(0x1000, code))
	if lazy {
		result := b.Run(context.Background(), 0x1000, mode, 1)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 1 || result.PC != 0x1000+step {
			t.Fatalf("literal flag origin: %+v", result)
		}
		fixture.registers[cpu.RegisterR0] = 0x80000000
		fixture.registers[cpu.RegisterPC] += step
		fixture.registers[cpu.RegisterCPSR] = 0x9000001f
		if mode == cpu.ModeThumb {
			fixture.registers[cpu.RegisterCPSR] |= cpu.StatusThumb
		}
	}
	return fixture
}

func (fixture hostConcrete22Fixture) expectedFrame(request cpu.HostCallFrameRequest) cpu.HostCallFrame {
	frame := hostConcrete22Sentinel()
	frame.Registers = fixture.registers
	frame.StackWords, frame.ParameterWords = request.StackWords, request.ParameterWords
	for index := uint32(0); index < request.StackWords; index++ {
		frame.Stack[index] = binary.LittleEndian.Uint32(fixture.data[index*4:])
	}
	for index := uint32(0); index < request.ParameterWords; index++ {
		frame.Parameters[index] = binary.LittleEndian.Uint32(fixture.data[256+index*4:])
	}
	return frame
}

func hostConcrete22SameState(t *testing.T, old, current *interpreter.Backend) {
	t.Helper()
	a, err := old.SaveContext()
	hostConcrete22Check(t, err)
	b, err := current.SaveContext()
	hostConcrete22Check(t, err)
	if !bytes.Equal(a, b) {
		t.Fatal("public CPU contexts differ")
	}
	for _, region := range []struct{ address, size uint32 }{{0x1000, 64}, {0x3000, 512}} {
		a, b := make([]byte, region.size), make([]byte, region.size)
		hostConcrete22Check(t, old.ReadMemory(region.address, a))
		hostConcrete22Check(t, current.ReadMemory(region.address, b))
		if !bytes.Equal(a, b) {
			t.Fatalf("mapped bytes differ at %#x", region.address)
		}
	}
	if a, b := old.ExecutionStatistics(), current.ExecutionStatistics(); !reflect.DeepEqual(a, b) {
		t.Fatal("execution statistics differ")
	}
}

func hostConcrete22HostCounts(b *interpreter.Backend) [3]uint64 {
	s := b.ExecutionStatistics()
	return [3]uint64{s.HostFrameCaptures, s.HostRegisterCommits, s.HostCallReturnBatches}
}

func TestHostCallConcreteOracleExactBackend(t *testing.T) {
	for _, factory := range hostConcrete22Factories {
		for _, mode := range []cpu.Mode{cpu.ModeARM, cpu.ModeThumb} {
			for _, lazy := range []bool{false, true} {
				for _, lr := range []uint32{0x2346, 0x2347} {
					for _, words := range []uint32{0, 1, cpu.MaxHostCallWords} {
						t.Run(fmt.Sprintf("%s/mode%d/lazy%t/lr%x/words%d", factory.name, mode, lazy, lr, words), func(t *testing.T) {
							old := hostConcrete22NewFixture(t, factory.new, mode, lazy, lr)
							current := hostConcrete22NewFixture(t, factory.new, mode, lazy, lr)
							// These are exact concrete pointers, not the archived dispatch
							// fixtures whose dynamic backend types were all wrappers.
							request := cpu.HostCallFrameRequest{StackWords: words, ParameterAddress: 0x3100, ParameterWords: words}
							a, b := hostConcrete22Sentinel(), hostConcrete22Sentinel()
							hostConcrete22Check(t, cpu.CaptureHostCallFrame(old.backend, &a, request))
							hostConcrete22Check(t, guest.CaptureHostCallFrame(current.backend, &b, request))
							if want := old.expectedFrame(request); a != want || b != want {
								t.Fatal("capture differs from literal full-register/stack/parameter frame")
							}
							if hostConcrete22HostCounts(current.backend) != [3]uint64{1, 0, 0} {
								t.Fatal("capture host counters are not exactly 1/0/0")
							}
							hostConcrete22SameState(t, old.backend, current.backend)
						})
					}
					t.Run(fmt.Sprintf("%s/return/mode%d/lazy%t/lr%x", factory.name, mode, lazy, lr), func(t *testing.T) {
						old := hostConcrete22NewFixture(t, factory.new, mode, lazy, lr)
						current := hostConcrete22NewFixture(t, factory.new, mode, lazy, lr)
						a, b := hostConcrete22Sentinel(), hostConcrete22Sentinel()
						hostConcrete22Check(t, cpu.ReturnHostCallFrame(old.backend, &a, 0x89abcdef, 0x01234567))
						hostConcrete22Check(t, guest.ReturnHostCallFrame(current.backend, &b, 0x89abcdef, 0x01234567))
						if want := old.expectedFrame(cpu.HostCallFrameRequest{}); a != want || b != want {
							t.Fatal("return did not capture the full pre-return frame or retain unused arrays")
						}
						want := old.registers
						want[cpu.RegisterR0], want[cpu.RegisterR1], want[cpu.RegisterPC] = 0x89abcdef, 0x01234567, lr&^1
						want[cpu.RegisterCPSR] &^= cpu.StatusThumb
						if lr&1 != 0 {
							want[cpu.RegisterCPSR] |= cpu.StatusThumb
						}
						for register, expected := range want {
							value, err := current.backend.ReadRegister(uint32(register))
							hostConcrete22Check(t, err)
							if value != expected {
								t.Fatalf("returned register %d: %#x != %#x", register, value, expected)
							}
						}
						if hostConcrete22HostCounts(current.backend) != [3]uint64{1, 1, 1} {
							t.Fatal("atomic return host counters are not exactly 1/1/1")
						}
						hostConcrete22SameState(t, old.backend, current.backend)
					})
				}
			}
		}
	}
}

// Embedding only cpu.Backend hides all optional bulk methods. A slice-bearing
// value is intentionally not comparable, unlike the exact pointer fast path.
type hostConcrete22Scalar struct{ cpu.Backend }
type hostConcrete22NonComparable struct {
	cpu.Backend
	marker []byte
}
type hostConcrete22CaptureOnly struct {
	hostConcrete22Scalar
	concrete *interpreter.Backend
	events   []string
}

func (b *hostConcrete22CaptureOnly) CaptureHostCallFrame(frame *cpu.HostCallFrame, request cpu.HostCallFrameRequest) error {
	b.events = append(b.events, "capture")
	return b.concrete.CaptureHostCallFrame(frame, request)
}
func (b *hostConcrete22CaptureOnly) CommitHostCallRegisters(commit cpu.RegisterCommit) error {
	b.events = append(b.events, "commit")
	return b.concrete.CommitHostCallRegisters(commit)
}

func TestHostCallConcreteOracleFallbackDynamicTypes(t *testing.T) {
	for _, factory := range hostConcrete22Factories {
		for _, kind := range []string{"scalar", "non-comparable", "capture-only"} {
			for _, returning := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/return%t", factory.name, kind, returning), func(t *testing.T) {
					old := hostConcrete22NewFixture(t, factory.new, cpu.ModeThumb, true, 0x2346)
					current := hostConcrete22NewFixture(t, factory.new, cpu.ModeThumb, true, 0x2346)
					wrap := func(b *interpreter.Backend) cpu.Backend {
						switch kind {
						case "non-comparable":
							return hostConcrete22NonComparable{Backend: b, marker: []byte{1}}
						case "capture-only":
							return &hostConcrete22CaptureOnly{hostConcrete22Scalar: hostConcrete22Scalar{b}, concrete: b}
						default:
							return &hostConcrete22Scalar{b}
						}
					}
					aBackend, bBackend := wrap(old.backend), wrap(current.backend)
					a, b := hostConcrete22Sentinel(), hostConcrete22Sentinel()
					request := cpu.HostCallFrameRequest{StackWords: 3, ParameterAddress: 0x3100, ParameterWords: 4}
					if returning {
						request = cpu.HostCallFrameRequest{}
						hostConcrete22Check(t, cpu.ReturnHostCallFrame(aBackend, &a, 17, 19))
						hostConcrete22Check(t, guest.ReturnHostCallFrame(bBackend, &b, 17, 19))
					} else {
						hostConcrete22Check(t, cpu.CaptureHostCallFrame(aBackend, &a, request))
						hostConcrete22Check(t, guest.CaptureHostCallFrame(bBackend, &b, request))
					}
					if want := old.expectedFrame(request); a != want || b != want {
						t.Fatal("fallback frame differs from literal frame")
					}
					counts := [3]uint64{}
					if kind == "capture-only" {
						counts[0] = 1
						wantEvents := []string{"capture"}
						if returning {
							counts[1], wantEvents = 1, []string{"capture", "commit"}
						}
						if !reflect.DeepEqual(bBackend.(*hostConcrete22CaptureOnly).events, wantEvents) {
							t.Fatal("optional capture/commit fallback order changed")
						}
					}
					if hostConcrete22HostCounts(current.backend) != counts {
						t.Fatal("fallback acquired an unadvertised return capability")
					}
					hostConcrete22SameState(t, old.backend, current.backend)
				})
			}
		}
	}
}

type hostConcrete22Override struct {
	*interpreter.Backend
	events     []string
	panicValue any
}

func (b *hostConcrete22Override) CaptureHostCallFrame(frame *cpu.HostCallFrame, _ cpu.HostCallFrameRequest) error {
	b.events = append(b.events, "capture")
	frame.Registers[3] = 0x12345678
	if b.panicValue != nil {
		panic(b.panicValue)
	}
	return hostConcrete22Fault
}
func (b *hostConcrete22Override) CommitHostCallRegisters(cpu.RegisterCommit) error {
	b.events = append(b.events, "commit")
	return hostConcrete22Fault
}
func (b *hostConcrete22Override) ReturnHostCallFrame(frame *cpu.HostCallFrame, _, _ uint32) error {
	b.events = append(b.events, "return")
	frame.Registers[4] = 0x87654321
	if b.panicValue != nil {
		panic(b.panicValue)
	}
	return hostConcrete22Fault
}

func TestHostCallConcreteOracleEmbeddingOverrides(t *testing.T) {
	for _, returning := range []bool{false, true} {
		for _, panicking := range []bool{false, true} {
			t.Run(fmt.Sprintf("return%t/panic%t", returning, panicking), func(t *testing.T) {
				old := &hostConcrete22Override{Backend: interpreter.New()}
				current := &hostConcrete22Override{Backend: interpreter.New()}
				if panicking {
					old.panicValue, current.panicValue = "literal host override panic", "literal host override panic"
				}
				defer old.Close()
				defer current.Close()
				a, b := hostConcrete22Sentinel(), hostConcrete22Sentinel()
				invoke := func(backend cpu.Backend, frame *cpu.HostCallFrame, modern bool) error {
					if returning {
						if modern {
							return guest.ReturnHostCallFrame(backend, frame, 1, 2)
						}
						return cpu.ReturnHostCallFrame(backend, frame, 1, 2)
					}
					if modern {
						return guest.CaptureHostCallFrame(backend, frame, cpu.HostCallFrameRequest{})
					}
					return cpu.CaptureHostCallFrame(backend, frame, cpu.HostCallFrameRequest{})
				}
				x := hostConcrete22Call(func() error { return invoke(old, &a, false) })
				y := hostConcrete22Call(func() error { return invoke(current, &b, true) })
				if x != y || a != b || !reflect.DeepEqual(old.events, current.events) || len(current.events) != 1 {
					t.Fatal("embedding override was bypassed or its error/partial frame changed")
				}
				if panicking && (y.PanicType != "string" || y.PanicText != "literal host override panic") || !panicking && !y.Fault {
					t.Fatal("override error/panic value changed")
				}
				want := hostConcrete22Sentinel()
				if returning {
					want.Registers[4] = 0x87654321
				} else {
					want.Registers[3] = 0x12345678
				}
				if b != want {
					t.Fatal("override partial frame does not match literal writes")
				}
				if hostConcrete22HostCounts(current.Backend) != [3]uint64{} {
					t.Fatal("override touched underlying bulk backend")
				}
			})
		}
	}
}

func TestHostCallConcreteOracleValidationPrecedence(t *testing.T) {
	for _, state := range []string{"nil", "typed-nil", "live", "closed", "live-jit", "closed-jit"} {
		for _, returning := range []bool{false, true} {
			for _, nilDestination := range []bool{false, true} {
				requests := []cpu.HostCallFrameRequest{{}, {StackWords: cpu.MaxHostCallWords + 1}, {ParameterWords: cpu.MaxHostCallWords + 1, ParameterAddress: 1}, {ParameterWords: 1}}
				if returning {
					requests = requests[:1]
				}
				for index, request := range requests {
					t.Run(fmt.Sprintf("%s/return%t/nil%t/request%d", state, returning, nilDestination, index), func(t *testing.T) {
						makeBackend := func() cpu.Backend {
							switch state {
							case "nil":
								return nil
							case "typed-nil":
								var b *interpreter.Backend
								return b
							default:
								b := interpreter.New()
								if state == "live-jit" || state == "closed-jit" {
									_ = b.Close()
									b = interpreter.NewJIT()
								}
								t.Cleanup(func() { _ = b.Close() })
								if state == "closed" || state == "closed-jit" {
									hostConcrete22Check(t, b.Close())
								}
								return b
							}
						}
						a, b := hostConcrete22Sentinel(), hostConcrete22Sentinel()
						ad, bd := &a, &b
						if nilDestination {
							ad, bd = nil, nil
						}
						oldBackend, currentBackend := makeBackend(), makeBackend()
						x := hostConcrete22Call(func() error {
							if returning {
								return cpu.ReturnHostCallFrame(oldBackend, ad, 1, 2)
							}
							return cpu.CaptureHostCallFrame(oldBackend, ad, request)
						})
						y := hostConcrete22Call(func() error {
							if returning {
								return guest.ReturnHostCallFrame(currentBackend, bd, 1, 2)
							}
							return guest.CaptureHostCallFrame(currentBackend, bd, request)
						})
						if x != y || a != b {
							t.Fatalf("validation/panic precedence changed: old=%+v new=%+v", x, y)
						}
						invalid := state == "nil" || nilDestination || index != 0
						if invalid && (!y.Invalid || y.PanicType != "" || b != hostConcrete22Sentinel()) {
							t.Fatal("wrapper validation did not precede receiver access")
						}
						if invalid {
							wantText := "host-call frame request: " + cpu.ErrInvalidAddress.Error()
							if state == "nil" || nilDestination {
								wantText = "host-call frame: " + cpu.ErrInvalidAddress.Error()
							}
							if y.ErrorText != wantText {
								t.Fatal("invalid-call error prefix/precedence changed")
							}
						}
						if !invalid && state == "typed-nil" && y.PanicType == "" {
							t.Fatal("typed nil valid call no longer preserves original panic")
						}
						if !invalid && (state == "closed" || state == "closed-jit") && (!y.Closed || b != hostConcrete22Sentinel()) {
							t.Fatal("closed valid call did not preserve ErrClosed")
						}
					})
				}
			}
		}
	}
}

// This backend advertises only scalar CPU operations. The literal trace makes
// partial frame/register writes independent of either wrapper's implementation.
type hostConcrete22FaultBackend struct {
	cpu.Backend
	registers           [17]uint32
	data                [32]byte
	readFail, writeFail uint32
	memoryFail          uint32
	trace               []string
}

func hostConcrete22NewFaultBackend() *hostConcrete22FaultBackend {
	b := &hostConcrete22FaultBackend{readFail: ^uint32(0), writeFail: ^uint32(0)}
	for index := range b.registers {
		b.registers[index] = 0x11000000 + uint32(index)
	}
	b.registers[cpu.RegisterSP], b.registers[cpu.RegisterLR], b.registers[cpu.RegisterCPSR] = 0x3000, 0x2467, 0x9000001f
	for index := range b.data {
		b.data[index] = byte(index + 1)
	}
	return b
}
func (b *hostConcrete22FaultBackend) ReadRegister(register uint32) (uint32, error) {
	b.trace = append(b.trace, fmt.Sprintf("r%d", register))
	if register == b.readFail {
		return 0, hostConcrete22Fault
	}
	return b.registers[register], nil
}
func (b *hostConcrete22FaultBackend) WriteRegister(register, value uint32) error {
	b.trace = append(b.trace, fmt.Sprintf("w%d", register))
	if register == b.writeFail {
		return hostConcrete22Fault
	}
	b.registers[register] = value
	return nil
}
func (b *hostConcrete22FaultBackend) ReadMemory(address uint32, destination []byte) error {
	b.trace = append(b.trace, fmt.Sprintf("m%x/%d", address, len(destination)))
	for index := range destination {
		current := address + uint32(index)
		if current == b.memoryFail || current < 0x3000 || current >= 0x3020 {
			return hostConcrete22Fault
		}
		destination[index] = b.data[current-0x3000]
	}
	return nil
}

func TestHostCallConcreteOracleScalarFaultOrder(t *testing.T) {
	for _, scenario := range []string{"register", "stack", "parameter", "return-read", "return-r0", "return-r1", "return-pc", "return-cpsr"} {
		t.Run(scenario, func(t *testing.T) {
			old, current := hostConcrete22NewFaultBackend(), hostConcrete22NewFaultBackend()
			request := cpu.HostCallFrameRequest{StackWords: 2, ParameterAddress: 0x3010, ParameterWords: 2}
			configure := func(b *hostConcrete22FaultBackend) {
				switch scenario {
				case "register", "return-read":
					b.readFail = 3
				case "stack":
					b.memoryFail = 0x3004
				case "parameter":
					b.memoryFail = 0x3014
				case "return-r0":
					b.writeFail = cpu.RegisterR0
				case "return-r1":
					b.writeFail = cpu.RegisterR1
				case "return-pc":
					b.writeFail = cpu.RegisterPC
				case "return-cpsr":
					b.writeFail = cpu.RegisterCPSR
				}
			}
			configure(old)
			configure(current)
			a, b := hostConcrete22Sentinel(), hostConcrete22Sentinel()
			returning := len(scenario) >= 7 && scenario[:7] == "return-"
			x := hostConcrete22Call(func() error {
				if returning {
					return cpu.ReturnHostCallFrame(old, &a, 0x71, 0x73)
				}
				return cpu.CaptureHostCallFrame(old, &a, request)
			})
			y := hostConcrete22Call(func() error {
				if returning {
					return guest.ReturnHostCallFrame(current, &b, 0x71, 0x73)
				}
				return guest.CaptureHostCallFrame(current, &b, request)
			})
			if x != y || !y.Fault || a != b || current.registers != old.registers || !reflect.DeepEqual(current.trace, old.trace) {
				t.Fatal("scalar partial frame/register state or operation order differs")
			}
			if scenario == "register" || scenario == "return-read" {
				want := hostConcrete22Sentinel()
				copy(want.Registers[:3], old.registers[:3])
				if b != want || !reflect.DeepEqual(current.trace, []string{"r0", "r1", "r2", "r3"}) {
					t.Fatal("register fault did not preserve exact partial frame")
				}
			} else if !returning {
				want := hostConcrete22Sentinel()
				want.Registers, want.StackWords, want.ParameterWords = old.registers, 2, 2
				want.Stack[0] = 0x04030201
				if scenario == "parameter" {
					want.Stack[1], want.Parameters[0] = 0x08070605, 0x14131211
				}
				if b != want {
					t.Fatal("scalar memory fault lost/added decoded words")
				}
			} else {
				before := hostConcrete22NewFaultBackend().registers
				wantTrace := make([]string, 17)
				for register := range wantTrace {
					wantTrace[register] = fmt.Sprintf("r%d", register)
				}
				for _, write := range []struct{ register, value uint32 }{{0, 0x71}, {1, 0x73}, {15, 0x2466}, {16, 0x9000003f}} {
					wantTrace = append(wantTrace, fmt.Sprintf("w%d", write.register))
					if write.register == current.writeFail {
						break
					}
					before[write.register] = write.value
				}
				if current.registers != before || !reflect.DeepEqual(current.trace, wantTrace) {
					t.Fatal("return scalar write order/partial commit is not R0,R1,PC,CPSR")
				}
			}
		})
	}
}

func TestHostCallConcreteOracleConcreteReadFaults(t *testing.T) {
	for _, factory := range hostConcrete22Factories {
		for _, scenario := range []string{"short-stack", "short-parameter", "permission", "stack-wrap", "parameter-wrap"} {
			t.Run(factory.name+"/"+scenario, func(t *testing.T) {
				newBackend := func() *interpreter.Backend {
					b := factory.new()
					t.Cleanup(func() { _ = b.Close() })
					hostConcrete22Check(t, b.WriteRegister(cpu.RegisterCPSR, 0x9000001f))
					hostConcrete22Check(t, b.WriteRegister(cpu.RegisterSP, 0x3000))
					stackSize := uint32(8)
					permission := cpu.PermissionRead | cpu.PermissionWrite
					if scenario == "short-stack" {
						stackSize = 6
					}
					if scenario == "permission" {
						permission = cpu.PermissionWrite
					}
					hostConcrete22Check(t, b.Map(0x3000, stackSize, permission))
					hostConcrete22Check(t, b.WriteMemory(0x3000, []byte{1, 2, 3, 4, 5, 6, 7, 8}[:stackSize]))
					hostConcrete22Check(t, b.Map(0x4000, 6, cpu.PermissionRead|cpu.PermissionWrite))
					hostConcrete22Check(t, b.WriteMemory(0x4000, []byte{9, 10, 11, 12, 13, 14}))
					if scenario == "stack-wrap" {
						hostConcrete22Check(t, b.WriteRegister(cpu.RegisterSP, 0xfffffffc))
					}
					return b
				}
				old, current := newBackend(), newBackend()
				request := cpu.HostCallFrameRequest{StackWords: 2}
				if scenario == "short-parameter" || scenario == "parameter-wrap" {
					request.ParameterAddress, request.ParameterWords = 0x4000, 2
				}
				if scenario == "parameter-wrap" {
					request.ParameterAddress = 0xfffffffc
				}
				a, b := hostConcrete22Sentinel(), hostConcrete22Sentinel()
				x := hostConcrete22Call(func() error { return cpu.CaptureHostCallFrame(old, &a, request) })
				y := hostConcrete22Call(func() error { return guest.CaptureHostCallFrame(current, &b, request) })
				if x != y || a != b || y.PanicType != "" || (!y.Invalid && !y.Permission) {
					t.Fatalf("concrete fault differs: old=%+v new=%+v", x, y)
				}
				want := hostConcrete22Sentinel()
				want.Registers = [17]uint32{}
				want.Registers[cpu.RegisterSP], want.Registers[cpu.RegisterCPSR] = 0x3000, 0x9000001f
				if scenario == "stack-wrap" {
					want.Registers[cpu.RegisterSP] = 0xfffffffc
				}
				want.StackWords, want.ParameterWords = request.StackWords, request.ParameterWords
				if request.ParameterWords != 0 {
					want.Stack[0], want.Stack[1] = 0x04030201, 0x08070605
				}
				if b != want || hostConcrete22HostCounts(current) != [3]uint64{} {
					t.Fatal("atomic word capture fault changed decode boundary/counters")
				}
				oldContext, err := old.SaveContext()
				hostConcrete22Check(t, err)
				currentContext, err := current.SaveContext()
				hostConcrete22Check(t, err)
				if !bytes.Equal(oldContext, currentContext) || !reflect.DeepEqual(old.ExecutionStatistics(), current.ExecutionStatistics()) {
					t.Fatal("concrete failed capture changed CPU context/statistics")
				}
			})
		}
	}
}
