package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type timerRequest struct {
	interval int16
	selector uint16
}

type timerSink struct {
	requests []timerRequest
	err      error
}

func (s *timerSink) RequestGVMTimer(interval int16, selector uint16) error {
	if s.err != nil {
		return s.err
	}
	s.requests = append(s.requests, timerRequest{interval: interval, selector: selector})
	return nil
}

func timerVM(t *testing.T, code []byte, sink gvm.TimerRequestSink) *gvm.VM {
	t.Helper()
	v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{Timer: sink})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTimerRequestDispatch(t *testing.T) {
	sink := new(timerSink)
	code := []byte{
		0x06, 0, 9, 0x06, 0x12, 0x34, 0x9a,
		0x06, 0, 10, 0x06, 0xff, 0xff, 0x9a,
		0x06, 0x80, 0, 0x06, 0, 0, 0x9a,
		0xff,
	}
	v := timerVM(t, code, sink)
	if err := v.Run(9); err != gvm.ErrBudget {
		t.Fatalf("requests: %v", err)
	}
	want := []timerRequest{{9, 0x1234}, {10, 0xffff}, {-32768, 0}}
	if !reflect.DeepEqual(sink.requests, want) {
		t.Fatalf("requests %+v want %+v", sink.requests, want)
	}
	if len(v.Stack()) != 0 || v.PC() != len(code)-1 || v.Halted() {
		t.Fatalf("state pc=%d stack=%x halted=%v", v.PC(), v.Stack(), v.Halted())
	}
	if err := v.Step(); err != nil || !v.Halted() {
		t.Fatalf("halt: %v", err)
	}
}

func TestTimerRequestFaultsAreStickyAndTransactional(t *testing.T) {
	sinkErr := errors.New("timer sink rejected request")
	for _, tc := range []struct {
		name   string
		code   []byte
		sink   gvm.TimerRequestSink
		want   error
		before []uint16
	}{
		{"underflow-empty", []byte{0x9a}, new(timerSink), gvm.ErrStackUnderflow, nil},
		{"underflow-one", []byte{0x06, 0, 1, 0x9a}, new(timerSink), gvm.ErrStackUnderflow, []uint16{1}},
		{"unavailable", []byte{0x06, 0, 10, 0x06, 0, 1, 0x9a}, nil, gvm.ErrTimerUnavailable, []uint16{10, 1}},
		{"sink-error", []byte{0x06, 0, 10, 0x06, 0, 2, 0x9a}, &timerSink{err: sinkErr}, sinkErr, []uint16{10, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := timerVM(t, tc.code, tc.sink)
			for v.PC() < len(tc.code)-1 {
				if err := v.Step(); err != nil {
					t.Fatal(err)
				}
			}
			pc := v.PC()
			err := v.Step()
			var execution *gvm.ExecutionError
			if !errors.Is(err, tc.want) || !errors.As(err, &execution) || execution.Offset != pc {
				t.Fatalf("fault: %v", err)
			}
			if v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), tc.before) || v.Halted() {
				t.Fatalf("changed state pc=%d stack=%x", v.PC(), v.Stack())
			}
			if v.Step() != err || v.Run(0) != err || v.Run(99) != err {
				t.Fatal("fault not sticky")
			}
		})
	}
}

func TestTimerRequestLegacyConstructorsRemainUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  []byte
		setup uint64
	}{{"depth0", []byte{0x9a}, 0}, {"depth1", []byte{0x06, 0, 10, 0x9a}, 1}, {"depth2", []byte{0x06, 0, 10, 0x06, 0, 1, 0x9a}, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := gvm.NewAt(tc.code, 0)
			if err != nil {
				t.Fatal(err)
			}
			symbols, err := gvm.NewWithSymbols(tc.code, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			address, err := gvm.NewWithAddressSpace(tc.code, 0, gvm.AddressSpace{})
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []*gvm.VM{gvm.New(tc.code), at, symbols, address} {
				if tc.setup > 0 {
					if err := v.Run(tc.setup); err != gvm.ErrBudget {
						t.Fatal(err)
					}
				}
				stack := v.Stack()
				pc := v.PC()
				err := v.Step()
				var unsupported *gvm.UnsupportedOpcodeError
				if !errors.As(err, &unsupported) || unsupported.Opcode != 0x9a || unsupported.Offset != pc || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), stack) {
					t.Fatalf("legacy: %v", err)
				}
			}
		})
	}
}

func TestTimerRequestAcceptsDepth65(t *testing.T) {
	code := []byte{0x06, 0, 10}
	for range 64 {
		code = append(code, 0x0f)
	}
	code = append(code, 0x9a, 0xff)
	sink := new(timerSink)
	v := timerVM(t, code, sink)
	if err := v.Run(65); err != gvm.ErrBudget || len(v.Stack()) != 65 {
		t.Fatalf("setup: %v depth=%d", err, len(v.Stack()))
	}
	if err := v.Step(); err != nil || len(v.Stack()) != 63 {
		t.Fatalf("request: %v depth=%d", err, len(v.Stack()))
	}
	if !reflect.DeepEqual(sink.requests, []timerRequest{{10, 10}}) {
		t.Fatalf("requests %+v", sink.requests)
	}
	for _, word := range v.Stack() {
		if word != 10 {
			t.Fatalf("remaining stack %x", v.Stack())
		}
	}
}

func TestTimerRequestBudgetAndNoInlineOperands(t *testing.T) {
	sink := new(timerSink)
	v := timerVM(t, []byte{0x06, 0, 10, 0x06, 0, 7, 0x9a}, sink)
	if err := v.Run(2); err != gvm.ErrBudget || v.PC() != 6 {
		t.Fatalf("before request: %v pc=%d", err, v.PC())
	}
	if err := v.Run(1); err != gvm.ErrBudget || v.PC() != 7 || len(v.Stack()) != 0 {
		t.Fatalf("request: %v pc=%d stack=%x", err, v.PC(), v.Stack())
	}
	if !reflect.DeepEqual(sink.requests, []timerRequest{{10, 7}}) {
		t.Fatalf("requests %+v", sink.requests)
	}
	if err := v.Step(); !errors.Is(err, gvm.ErrTruncated) {
		t.Fatalf("next fetch: %v", err)
	}
}
