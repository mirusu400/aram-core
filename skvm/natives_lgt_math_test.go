package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func mathFPVM(t *testing.T, policy NativePolicy) *VM {
	t.Helper()
	s, err := shared.NewServices(shared.Config{})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := NewWithNativePolicy(nil, s, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestLGTMathFPIntegerArithmetic(t *testing.T) {
	tests := []struct {
		name, method string
		args         []int32
		want         int32
		exception    string
	}{
		{"parse zero", "parseFP", []int32{0}, 0, ""},
		{"parse negative", "parseFP", []int32{-1}, -4096, ""},
		{"parse max", "parseFP", []int32{524287}, 2147479552, ""},
		{"parse min", "parseFP", []int32{-524288}, -2147483648, ""},
		{"parse high", "parseFP", []int32{524288}, 0, "java/lang/NumberFormatException"},
		{"parse low", "parseFP", []int32{-524289}, 0, "java/lang/NumberFormatException"},
		{"parse intmax", "parseFP", []int32{2147483647}, 0, "java/lang/NumberFormatException"},
		{"parse intmin", "parseFP", []int32{-2147483648}, 0, "java/lang/NumberFormatException"},
		{"below half", "toInt", []int32{2047}, 0, ""},
		{"half positive", "toInt", []int32{2048}, 1, ""},
		{"half negative", "toInt", []int32{-2048}, 0, ""},
		{"negative beyond half", "toInt", []int32{-2049}, -1, ""},
		{"negative one half", "toInt", []int32{-6144}, -1, ""},
		{"toInt max", "toInt", []int32{2147483647}, 524288, ""},
		{"toInt min", "toInt", []int32{-2147483648}, -524288, ""},
		{"round negative", "round", []int32{-6145}, -8192, ""},
		{"round tie", "round", []int32{-6144}, -4096, ""},
		{"round overflow wraps", "round", []int32{2147483647}, -2147483648, ""},
		{"abs negative", "abs", []int32{-4097}, 4097, ""},
		{"abs min wraps", "abs", []int32{-2147483648}, -2147483648, ""},
		{"add fractional", "add", []int32{4096, -1}, 4095, ""},
		{"add wraps", "add", []int32{2147483647, 1}, -2147483648, ""},
		{"sub negative", "sub", []int32{-4096, 2048}, -6144, ""},
		{"sub wraps", "sub", []int32{-2147483648, 1}, 2147483647, ""},
		{"min signed", "min", []int32{-2147483648, 2147483647}, -2147483648, ""},
		{"max signed", "max", []int32{-2147483648, 2147483647}, 2147483647, ""},
		{"multiply fractions", "multiply", []int32{6144, -6144}, -9216, ""},
		{"multiply truncates", "multiply", []int32{-1, 2048}, 0, ""},
		{"multiply max identity", "multiply", []int32{2147483647, 4096}, 2147483647, ""},
		{"multiply min identity", "multiply", []int32{-2147483648, 4096}, -2147483648, ""},
		{"multiply positive overflow", "multiply", []int32{2147483647, 4097}, 0, "java/lang/ArithmeticException"},
		{"multiply negative overflow", "multiply", []int32{-2147483648, 4097}, 0, "java/lang/ArithmeticException"},
		{"multiply negation overflow", "multiply", []int32{-2147483648, -4096}, 0, "java/lang/ArithmeticException"},
		{"multiply wide intermediate", "multiply", []int32{1073741824, 8192}, 0, "java/lang/ArithmeticException"},
		{"divide fraction", "divide", []int32{4096, 8192}, 2048, ""},
		{"divide negative truncates", "divide", []int32{-4096, 12288}, -1365, ""},
		{"divide two negatives", "divide", []int32{-6144, -2048}, 12288, ""},
		{"divide zero", "divide", []int32{1, 0}, 0, "java/lang/ArithmeticException"},
		{"divide zero zero", "divide", []int32{0, 0}, 0, "java/lang/ArithmeticException"},
		{"divide overflow wraps", "divide", []int32{-2147483648, -4096}, -2147483648, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm := mathFPVM(t, NativePolicy(2))
			args := make([]Value, len(tt.args))
			desc := "("
			for i, a := range tt.args {
				args[i] = IntValue(a)
				desc += "I"
			}
			desc += ")I"
			got, has, err := vm.InvokeStatic(context.Background(), "mmpp/lang/MathFP", tt.method, desc, args...)
			if tt.exception != "" {
				var thrownError *thrown
				if !errors.As(err, &thrownError) || thrownError.class != tt.exception {
					t.Fatalf("want guest %s, got %v", tt.exception, err)
				}
				return
			}
			if err != nil || !has {
				t.Fatalf("invocation has=%v err=%v", has, err)
			}
			n, err := got.Int()
			if err != nil || n != tt.want {
				t.Fatalf("got %d (%v), want %d", n, err, tt.want)
			}
		})
	}
}

func TestLGTMathFPPolicyAndUnsupported(t *testing.T) {
	for _, p := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME} {
		vm := mathFPVM(t, p)
		if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/lang/MathFP", "parseFP", "(I)I", IntValue(1)); err == nil {
			t.Fatalf("MathFP leaked into policy %d", p)
		}
	}
	vm := mathFPVM(t, NativePolicy(2))
	for _, name := range []string{"sin", "cos", "tan", "sqrt", "log", "exp", "asin", "acos", "atan"} {
		if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/lang/MathFP", name, "(I)I", IntValue(0)); err == nil {
			t.Fatalf("unsupported %s succeeded", name)
		}
	}
	if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/lang/MathFP", "parseFP", "(Ljava/lang/String;)I", ReferenceValue(vm.NewString("1.25"))); err == nil {
		t.Fatal("unsupported string parsing succeeded")
	}
	if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/lang/MathFP", "pow", "(II)I", IntValue(4096), IntValue(4096)); err == nil {
		t.Fatal("unsupported pow succeeded")
	}
	if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/lang/MathFP", "toString", "(I)Ljava/lang/String;", IntValue(4096)); err == nil {
		t.Fatal("unsupported string formatting succeeded")
	}
}

// This fixture is authored from scratch. Its run method uses real JVM opcodes,
// not a direct call to a registered Go native or a copied handset class.
func mathFPBytecode(t *testing.T, target, descriptor string, field bool, prefix []byte, catch string) []byte {
	t.Helper()
	var b bytes.Buffer
	u2 := func(n uint16) {
		if err := binary.Write(&b, binary.BigEndian, n); err != nil {
			t.Fatal(err)
		}
	}
	u4 := func(n uint32) {
		if err := binary.Write(&b, binary.BigEndian, n); err != nil {
			t.Fatal(err)
		}
	}
	utf := func(s string) { b.WriteByte(constantUTF8); u2(uint16(len(s))); b.WriteString(s) }
	cls := func(n uint16) { b.WriteByte(constantClass); u2(n) }
	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(16)
	utf("MathFPProbe")
	cls(1) // 1,2
	utf("java/lang/Object")
	cls(3) // 3,4
	utf("run")
	utf("()I")
	utf("Code") // 5,6,7
	utf("mmpp/lang/MathFP")
	cls(8) // 8,9
	utf(target)
	utf(descriptor) // 10,11
	b.WriteByte(constantNameAndType)
	u2(10)
	u2(11) // 12
	opcode := byte(0xb8)
	if field {
		b.WriteByte(constantFieldref)
		opcode = 0xb2
	} else {
		b.WriteByte(constantMethodref)
	}
	u2(9)
	u2(12) // 13
	catchName := catch
	if catchName == "" {
		catchName = "java/lang/ArithmeticException"
	}
	utf(catchName)
	cls(14) // 14,15
	u2(0x21)
	u2(2)
	u2(4)
	u2(0)
	u2(0)
	u2(1)
	u2(0x0009)
	u2(5)
	u2(6)
	u2(1)
	u2(7)
	code := append(append([]byte{}, prefix...), opcode, 0, 13, 0xac)
	end := len(code)
	extra := 0
	if catch != "" {
		code = append(code, 0x57, 0x10, 99, 0xac)
		extra = 8
	}
	u4(uint32(12 + len(code) + extra))
	u2(3)
	u2(0)
	u4(uint32(len(code)))
	b.Write(code)
	if catch != "" {
		u2(1)
		u2(0)
		u2(uint16(end - 1))
		u2(uint16(end))
		u2(15)
	} else {
		u2(0)
	}
	u2(0)
	u2(0)
	return b.Bytes()
}

func TestLGTMathFPBytecode(t *testing.T) {
	tests := []struct {
		name, target, desc string
		field              bool
		prefix             []byte
		catch              string
		want               int32
	}{
		{"E", "E", "I", true, nil, "", 11134},
		{"PI", "PI", "I", true, nil, "", 12868},
		{"MAX_VALUE", "MAX_VALUE", "I", true, nil, "", 2147483647},
		{"MIN_VALUE", "MIN_VALUE", "I", true, nil, "", -2147483648},
		{"MAX_VALUE_INT", "MAX_VALUE_INT", "I", true, nil, "", 524287},
		{"MIN_VALUE_INT", "MIN_VALUE_INT", "I", true, nil, "", -524288},
		{"parse negative", "parseFP", "(I)I", false, []byte{0x10, 0xfd}, "", -12288},
		// 1 << 19 is the first out-of-range positive integer.
		{"catch parse", "parseFP", "(I)I", false, []byte{0x04, 0x10, 19, 0x78}, "java/lang/NumberFormatException", 99},
		{"catch divide", "divide", "(II)I", false, []byte{0x04, 0x03}, "java/lang/ArithmeticException", 99},
		// (1 << 30) * FP(2) overflows the signed fixed-point word.
		{"catch multiply", "multiply", "(II)I", false, []byte{0x04, 0x10, 30, 0x78, 0x11, 0x20, 0x00}, "java/lang/ArithmeticException", 99},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := shared.NewServices(shared.Config{})
			if err != nil {
				t.Fatal(err)
			}
			vm, err := NewWithNativePolicy(map[string][]byte{"MathFPProbe": mathFPBytecode(t, tt.target, tt.desc, tt.field, tt.prefix, tt.catch)}, s, 1, NativePolicy(2))
			if err != nil {
				t.Fatal(err)
			}
			value, has, err := vm.InvokeStatic(context.Background(), "MathFPProbe", "run", "()I")
			if err != nil || !has {
				t.Fatalf("has=%v err=%v", has, err)
			}
			got, err := value.Int()
			if err != nil || got != tt.want {
				t.Fatalf("got=%d err=%v want=%d", got, err, tt.want)
			}
			if vm.Instructions < 2 {
				t.Fatal("fixture did not execute bytecode")
			}
		})
	}
}
