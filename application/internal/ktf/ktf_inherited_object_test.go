package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFCardResolvesInheritedObjectMethods(t *testing.T) {
	for _, spec := range []struct{ name, descriptor string }{
		{"getClass", "()Ljava/lang/Class;"},
		{"wait", "(J)V"},
		{"notify", "()V"},
		{"notifyAll", "()V"},
	} {
		t.Run(spec.name, func(t *testing.T) {
			r := newTestRuntime(t)
			card := ensureClass(t, r, "org/kwis/msp/lcdui/Card")
			object := ensureClass(t, r, "java/lang/Object")
			address, err := r.resolveJavaMethod(card, spec.name, spec.descriptor)
			check(t, err)
			method, err := r.InspectJavaMethod(address)
			check(t, err)
			if method.DeclaringClass != object {
				t.Fatalf("inherited %s declared by %#x, want Object %#x", spec.name, method.DeclaringClass, object)
			}
			if method.Body == 0 || method.NativeBody != method.Body {
				t.Fatalf("inherited method must support cached Java and native calls: %+v", method)
			}
			if spec.name == "getClass" {
				instance, err := r.NewJavaInstanceForClass(inspectClass(t, r, card))
				check(t, err)
				parameters := allocWords(t, r, 2)
				check(t, r.WriteU32(parameters, instance))
				r.LastJavaMethod = "org/kwis/msp/lcdui/Jlet.<init>()V"
				check(t, r.CPU.WriteRegister(cpu.RegisterR0, method.NativeBody))
				check(t, r.CPU.WriteRegister(cpu.RegisterR1, parameters))
				value, err := ktfCallNative(context.Background(), r)
				check(t, err)
				value = readU32(t, r, value)
				expected, err := r.javaClassObject(card)
				check(t, err)
				if value == 0 || value != expected {
					t.Fatalf("cached Card.getClass() = %#x, want %#x", value, expected)
				}
			}
		})
	}
}

func TestKTFInheritedLookupPreservesHostDeclarationsAndFallbacks(t *testing.T) {
	for _, spec := range []struct{ class, name, descriptor string }{
		{"org/kwis/msp/lcdui/Card", "<init>", "()V"},
		{"org/kwis/msp/lcdui/Card", "<init>", "(I)V"},
		{"org/kwis/msp/lcdui/Card", "unknownSelector", "()V"},
		{"java/lang/String", "equals", "(Ljava/lang/Object;)Z"},
	} {
		t.Run(spec.class+"."+spec.name+spec.descriptor, func(t *testing.T) {
			r := newTestRuntime(t)
			class := ensureClass(t, r, spec.class)
			address, err := r.resolveJavaMethod(class, spec.name, spec.descriptor)
			check(t, err)
			method, err := r.InspectJavaMethod(address)
			check(t, err)
			if method.DeclaringClass != class {
				t.Fatalf("local method moved to parent: %+v", method)
			}
			again, err := r.resolveJavaMethod(class, spec.name, spec.descriptor)
			check(t, err)
			if again != address {
				t.Fatalf("repeated lookup = %#x, want %#x", again, address)
			}
		})
	}
}
