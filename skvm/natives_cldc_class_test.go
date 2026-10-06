package skvm

import (
	"bytes"
	"testing"
)

func TestCLDCClassIntrospectionForHostTypes(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	class := vm.NewObject("java/lang/Class", "java/io/DataInput")
	value := invokeTestNative(t, vm, "java/lang/Class", "isInterface", "()Z", class)
	got, err := value.Int()
	check(t, err)
	if got != 1 {
		t.Fatal("DataInput was not reported as an interface")
	}
	name := invokeTestNative(t, vm, "java/lang/Class", "getName", "()Ljava/lang/String;", class)
	nameReference, err := name.Reference()
	check(t, err)
	text, err := vm.String(nameReference)
	check(t, err)
	if text != "java.io.DataInput" {
		t.Fatalf("Class.getName() = %q", text)
	}
}

func TestClassResourceLookupFallsBackToJarRoot(t *testing.T) {
	for _, test := range []struct {
		name         string
		className    string
		resourceName string
		resources    map[string][]byte
		want         []byte
	}{
		{
			name:      "host class root fallback",
			className: "java/lang/Runtime",
			resources: map[string][]byte{"table.gft": []byte("root")},
			want:      []byte("root"),
		},
		{
			name:      "host package resource takes priority",
			className: "java/lang/Runtime",
			resources: map[string][]byte{
				"table.gft":           []byte("root"),
				"java/lang/table.gft": []byte("package"),
			},
			want: []byte("package"),
		},
		{
			name:      "guest class keeps package lookup",
			className: "game/Main",
			resources: map[string][]byte{"table.gft": []byte("root")},
		},
		{
			name:         "guest root path fallback",
			className:    "common/FontCreator",
			resourceName: "res/font/lbm/m1.lbm",
			resources:    map[string][]byte{"res/font/lbm/m1.lbm": []byte("root")},
			want:         []byte("root"),
		},
		{
			name:         "guest package path takes priority",
			className:    "common/FontCreator",
			resourceName: "res/font/lbm/m1.lbm",
			resources: map[string][]byte{
				"res/font/lbm/m1.lbm":        []byte("root"),
				"common/res/font/lbm/m1.lbm": []byte("package"),
			},
			want: []byte("package"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			vm, err := New(map[string][]byte{})
			check(t, err)
			check(t, vm.SetResourcesChecked(test.resources))
			class := vm.NewObject("java/lang/Class", test.className)
			resourceName := test.resourceName
			if resourceName == "" {
				resourceName = "table.gft"
			}
			name := vm.NewString(resourceName)
			result := invokeTestNative(t, vm, "java/lang/Class", "getResourceAsStream",
				"(Ljava/lang/String;)Ljava/io/InputStream;", class, ReferenceValue(name))
			stream, err := result.Reference()
			check(t, err)
			if test.want == nil {
				if stream != 0 {
					t.Fatalf("stream = %d, want null", stream)
				}
				return
			}
			object, ok := vm.Object(stream)
			if !ok {
				t.Fatalf("missing stream object %d", stream)
			}
			state, ok := object.Native.(*inputStreamState)
			if !ok || !bytes.Equal(state.data, test.want) {
				t.Fatalf("stream data = %v, want %q", object.Native, test.want)
			}
		})
	}
}
