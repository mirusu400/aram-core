package skvm

import (
	"context"
	shared "github.com/mirusu400/aram-core/runtime"
	"strings"
	"testing"
)

func TestJ2MENativePolicyAndState(t *testing.T) {
	makeVM := func(policy NativePolicy) *VM {
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
	java := makeVM(NativePolicyJ2ME)
	checkPolicy := func() {
		t.Helper()
		for key := range java.natives {
			if !standardJavaClass(key.class) {
				t.Fatalf("OEM native leaked: %+v", key)
			}
		}
		for class := range java.hostSupers {
			if !standardJavaClass(class) {
				t.Fatalf("OEM host class leaked: %s", class)
			}
		}
		for name := range java.hostStatic {
			if strings.HasPrefix(name, "com/") || strings.HasPrefix(name, "org/") {
				t.Fatalf("OEM static leaked: %s", name)
			}
		}
		for _, key := range []nativeKey{{"javax/microedition/midlet/MIDlet", "getAppProperty", "(Ljava/lang/String;)Ljava/lang/String;"}, {"javax/microedition/lcdui/Graphics", "setColor", "(I)V"}, {"javax/microedition/rms/RecordStore", "openRecordStore", "(Ljava/lang/String;Z)Ljavax/microedition/rms/RecordStore;"}} {
			if java.natives[key] == nil {
				t.Fatalf("standard native missing: %+v", key)
			}
		}
	}
	checkPolicy()
	for key, want := range map[string]string{"microedition.configuration": "CLDC-1.0", "microedition.profiles": "MIDP-1.0", "unknown.property": "", "com.xce.wipi.version": "", "MIN": ""} {
		value, has, err := java.InvokeStatic(context.Background(), "java/lang/System", "getProperty", "(Ljava/lang/String;)Ljava/lang/String;", ReferenceValue(java.NewString(key)))
		if err != nil || !has {
			t.Fatalf("property %s: %v", key, err)
		}
		ref, err := value.Reference()
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			if ref != 0 {
				t.Fatalf("unsupported property %s returned a value", key)
			}
			continue
		}
		got, err := java.String(ref)
		if err != nil || got != want {
			t.Fatalf("property %s=%q, %v", key, got, err)
		}
	}
	if _, _, err := java.InvokeStatic(context.Background(), "mmpp/media/MediaPlayer", "play", "()V"); err == nil {
		t.Fatal("unsupported mmpp API faked success")
	}
	state, err := java.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err = java.UnmarshalBinary(state); err != nil {
		t.Fatal(err)
	}
	checkPolicy()
	if java.nativePolicy != NativePolicyJ2ME {
		t.Fatal("state lost native policy")
	}
	legacy := makeVM(NativePolicySKT)
	if len(legacy.natives) <= len(java.natives) {
		t.Fatal("legacy native registry unexpectedly reduced")
	}
	if err = legacy.UnmarshalBinary(state); err == nil {
		t.Fatal("J2ME state accepted by SKT VM")
	}
	legacyState, err := legacy.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err = java.UnmarshalBinary(legacyState); err == nil {
		t.Fatal("SKT state accepted by J2ME VM")
	}
}
