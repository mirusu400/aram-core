package skvm

import (
	"context"
	shared "github.com/mirusu400/aram-core/runtime"
	"testing"
)

func policyRegressionVM(t *testing.T, policy NativePolicy) *VM {
	t.Helper()
	services, err := shared.NewServices(shared.Config{})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := NewWithNativePolicy(nil, services, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestJ2MECompatibilityInvocations(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyJ2ME)
	ctx := context.Background()
	call := func(class, name, desc string, receiver uint32, args ...Value) Value {
		t.Helper()
		native := vm.natives[nativeKey{class, name, desc}]
		if native == nil {
			t.Errorf("missing %s.%s%s", class, name, desc)
			return Value{}
		}
		value, _, err := native(ctx, vm, receiver, args)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		return value
	}
	canvas := vm.NewObject("javax/microedition/lcdui/Canvas", nil)
	for key, want := range map[int32]int32{'2': 1, '4': 2, '6': 5, '8': 6, '5': 8, '0': 0} {
		v := call("javax/microedition/lcdui/Canvas", "getGameAction", "(I)I", canvas, IntValue(key))
		if got, err := v.Int(); err != nil || got != want {
			t.Errorf("key %d: %d %v, want %d", key, got, err, want)
		}
	}
	for _, shown := range []bool{false, true} {
		if shown {
			vm.currentDisplay = canvas
		}
		v := call("javax/microedition/lcdui/Displayable", "isShown", "()Z", canvas)
		got, err := v.Int()
		if err != nil || (got != 0) != shown {
			t.Errorf("isShown: %d %v", got, err)
		}
	}
	fontValue := call("javax/microedition/lcdui/Font", "getDefaultFont", "()Ljavax/microedition/lcdui/Font;", 0)
	font, _ := fontValue.Reference()
	text := ReferenceValue(vm.NewString("ABC"))
	chars := ReferenceValue(vm.newArray("[C", []Value{IntValue('A'), IntValue('B'), IntValue('C')}))
	expected := call("javax/microedition/lcdui/Font", "charWidth", "(C)I", font, IntValue('B'))
	want, _ := expected.Int()
	for _, tc := range []struct {
		name, desc string
		arg        Value
	}{{"charsWidth", "([CII)I", chars}, {"substringWidth", "(Ljava/lang/String;II)I", text}} {
		v := call("javax/microedition/lcdui/Font", tc.name, tc.desc, font, tc.arg, IntValue(1), IntValue(1))
		if got, err := v.Int(); err != nil || got != want || got <= 0 {
			t.Errorf("%s: %d %v, want %d", tc.name, got, err, want)
		}
	}
	graphics := vm.ScreenGraphics()
	call("javax/microedition/lcdui/Graphics", "drawChars", "([CIIIII)V", graphics, chars, IntValue(1), IntValue(1), IntValue(0), IntValue(0), IntValue(20))
	call("javax/microedition/lcdui/Graphics", "drawSubstring", "(Ljava/lang/String;IIIII)V", graphics, text, IntValue(1), IntValue(1), IntValue(8), IntValue(0), IntValue(20))
	for _, name := range []string{"drawArc", "fillArc", "drawRoundRect", "fillRoundRect"} {
		call("javax/microedition/lcdui/Graphics", name, "(IIIIII)V", graphics, IntValue(0), IntValue(0), IntValue(8), IntValue(8), IntValue(0), IntValue(360))
	}
	legacy := policyRegressionVM(t, NativePolicySKT)
	for _, key := range []nativeKey{{"javax/microedition/lcdui/Displayable", "repaintIM", "()V"}, {"javax/microedition/lcdui/Graphics", "reset", "()V"}, {"com/skt/m/Graphics2D", "setPixel", "(III)V"}, {"org/kwis/msp/lcdui/Display", "getGameAction", "(I)I"}} {
		if vm.natives[key] != nil {
			t.Errorf("OEM leaked: %+v", key)
		}
		if legacy.natives[key] == nil {
			t.Errorf("legacy lost: %+v", key)
		}
	}
	// Every standard registration in the legacy registry must survive, except
	// the two known handset extensions to standard classes.
	for key := range legacy.natives {
		if !standardJavaClass(key.class) || key.name == "repaintIM" || key == (nativeKey{"javax/microedition/lcdui/Graphics", "reset", "()V"}) {
			continue
		}
		if vm.natives[key] == nil {
			t.Errorf("standard registration lost: %+v", key)
		}
	}
}

func TestJ2MEAppSystemPropertyIsolation(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT} {
		vm := policyRegressionVM(t, policy)
		for key, metadata := range map[string]string{"microedition.configuration": "spoof-cldc", "microedition.profiles": "spoof-midp", "app.only": "application-value"} {
			vm.properties[key] = metadata
			arg := ReferenceValue(vm.NewString(key))
			app := vm.natives[nativeKey{"javax/microedition/midlet/MIDlet", "getAppProperty", "(Ljava/lang/String;)Ljava/lang/String;"}]
			value, has, err := app(context.Background(), vm, 0, []Value{arg})
			ref, _ := value.Reference()
			got, _ := vm.String(ref)
			if err != nil || !has || got != metadata {
				t.Fatalf("app property %s: %q %v", key, got, err)
			}
			value, has, err = vm.InvokeStatic(context.Background(), "java/lang/System", "getProperty", "(Ljava/lang/String;)Ljava/lang/String;", arg)
			if err != nil || !has {
				t.Fatalf("system property: %v", err)
			}
			ref, _ = value.Reference()
			want := metadata
			if policy == NativePolicyJ2ME {
				want = map[string]string{"microedition.configuration": "CLDC-1.0", "microedition.profiles": "MIDP-1.0"}[key]
			}
			if want == "" {
				if ref != 0 {
					t.Errorf("app-only property exposed by System")
				}
				continue
			}
			got, err = vm.String(ref)
			if err != nil || got != want {
				t.Errorf("policy %v system %s: %q %v, want %q", policy, key, got, err, want)
			}
		}
	}
}
