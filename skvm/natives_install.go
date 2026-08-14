package skvm

import (
	"context"
	"fmt"
	"path"
	"strings"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (vm *VM) installCoreNatives() {
	vm.RegisterNative("java/lang/Object", "<init>", "()V", nativeVoid)
	vm.RegisterNative(
		"java/lang/Object",
		"getClass",
		"()Ljava/lang/Class;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid receiver %d", receiver)
			}
			return ReferenceValue(vm.NewObject("java/lang/Class", object.Class)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Class",
		"getResourceAsStream",
		"(Ljava/lang/String;)Ljava/io/InputStream;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			classObject, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Class receiver")
			}
			className, _ := classObject.Native.(string)
			resourceName := strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
			if !strings.HasPrefix(name, "/") && strings.Contains(className, "/") {
				resourceName = path.Join(path.Dir(className), resourceName)
			}
			data, ok := vm.resource(resourceName)
			if !ok {
				return ReferenceValue(0), true, nil
			}
			stream := &inputStreamState{data: append([]byte(nil), data...)}
			return ReferenceValue(vm.NewObject("java/io/InputStream", stream)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Class",
		"forName",
		"(Ljava/lang/String;)Ljava/lang/Class;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			name = strings.ReplaceAll(name, ".", "/")
			return ReferenceValue(vm.NewObject("java/lang/Class", name)), true, nil
		},
	)

	vm.RegisterNative("java/lang/String", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, "")
	})
	vm.RegisterNative("java/lang/String", "<init>", "([B)V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		args []Value,
	) (Value, bool, error) {
		reference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		data, err := vm.ByteArray(reference)
		if err != nil {
			return Value{}, false, err
		}
		value, err := vm.services.Text.Decode(data, shared.EncodingEUCKR)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, value)
	})
	vm.RegisterNative("java/lang/String", "<init>", "([BII)V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		args []Value,
	) (Value, bool, error) {
		reference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		offset, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		length, err := intArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
		data, err := vm.ByteArray(reference)
		if err != nil {
			return Value{}, false, err
		}
		if offset < 0 || length < 0 ||
			int64(offset)+int64(length) > int64(len(data)) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		value, err := vm.services.Text.Decode(
			data[offset:offset+length],
			shared.EncodingEUCKR,
		)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, value)
	})
	vm.RegisterNative(
		"java/lang/String",
		"length",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(utf16.Encode([]rune(value))))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"charAt",
		"(I)C",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			index, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			units := utf16.Encode([]rune(value))
			if index < 0 || int(index) >= len(units) {
				return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
			}
			return IntValue(int32(units[index])), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"substring",
		"(II)Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			start, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			end, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			units := utf16.Encode([]rune(value))
			if start < 0 || end < start || int(end) > len(units) {
				return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
			}
			return ReferenceValue(vm.NewString(string(utf16.Decode(units[start:end])))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"toString",
		"()Ljava/lang/String;",
		func(_ context.Context, _ *VM, receiver uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(receiver), true, nil
		},
	)

	vm.RegisterNative("java/lang/StringBuffer", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &stringBufferState{})
	})
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"append",
		"(I)Ljava/lang/StringBuffer;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state.value += fmt.Sprintf("%d", value)
			return ReferenceValue(receiver), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"append",
		"(Ljava/lang/String;)Ljava/lang/StringBuffer;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value := "null"
			if reference != 0 {
				value, err = vm.String(reference)
				if err != nil {
					return Value{}, false, err
				}
			}
			state.value += value
			return ReferenceValue(receiver), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"toString",
		"()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewString(state.value)), true, nil
		},
	)

	vm.RegisterNative(
		"java/lang/Math",
		"abs",
		"(I)I",
		func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if value < 0 {
				value = -value
			}
			return IntValue(value), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Math",
		"abs",
		"(J)J",
		func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
			if value < 0 {
				value = -value
			}
			return LongValue(value), true, nil
		},
	)
	for _, method := range []struct {
		name       string
		descriptor string
		native     NativeFunc
	}{
		{
			"min",
			"(II)I",
			func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
				left, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				right, err := intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
				return IntValue(min(left, right)), true, nil
			},
		},
		{
			"max",
			"(II)I",
			func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
				left, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				right, err := intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
				return IntValue(max(left, right)), true, nil
			},
		},
		{
			"max",
			"(JJ)J",
			func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
				left, err := args[0].Long()
				if err != nil {
					return Value{}, false, err
				}
				right, err := args[1].Long()
				if err != nil {
					return Value{}, false, err
				}
				return LongValue(max(left, right)), true, nil
			},
		},
	} {
		vm.RegisterNative("java/lang/Math", method.name, method.descriptor, method.native)
	}
	vm.RegisterNative(
		"java/lang/System",
		"currentTimeMillis",
		"()J",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return LongValue(vm.services.Clock.WallMillis()), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/System",
		"gc",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.collectGarbage()
		},
	)
	vm.RegisterNative("java/lang/System", "exit", "(I)V", nativeVoid)
	vm.RegisterNative(
		"java/lang/System",
		"getProperty",
		"(Ljava/lang/String;)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value, ok := vm.properties[name]
			if !ok {
				value, ok = vm.services.Device.Property(name)
			}
			if !ok {
				config := vm.services.Device.Config()
				switch name {
				case "MIN":
					value = config.PhoneNumber
					if value == "" {
						value = "MIN0000000000"
					}
					ok = true
				case "com.xce.wipi.version":
					value, ok = config.WIPIVersion, true
				case "microedition.platform":
					value = config.Model
					if value == "" {
						value = "SKVM"
					}
					ok = true
				case "microedition.configuration":
					value, ok = "M_Configuration-1.0", true
				case "microedition.profiles":
					value, ok = "M_Profile-1.0 SKTP-1.0", true
				case "microedition.locale":
					value, ok = config.Locale, true
				case "microedition.encoding":
					value, ok = "EUC-KR", true
				default:
					value, ok = "0", true
				}
			}
			if !ok {
				return ReferenceValue(0), true, nil
			}
			return ReferenceValue(vm.NewString(value)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/System",
		"arraycopy",
		"(Ljava/lang/Object;ILjava/lang/Object;II)V",
		nativeArrayCopy,
	)

	vm.installInputStreamNatives()
	vm.installOutputStreamNatives()
	vm.installDataIONatives()
	vm.installConnectionNatives()
	vm.installThreadNatives()
	vm.installRandomNatives()
	vm.installMIDletNatives()
	vm.installDisplayNatives()
	vm.installGraphicsNatives()
	vm.installRecordStoreNatives()
	vm.installSKTNatives()
	vm.installHostStaticFields()
	vm.installExtendedCoreNatives()
	vm.installCompatibilityNatives()
}

func (vm *VM) installHostStaticFields() {
	vm.RegisterStaticField(
		applicationRootClass,
		applicationRootField,
		applicationRootDescriptor,
		ReferenceValue(0),
	)
	font := ReferenceValue(vm.NewObject("javax/microedition/lcdui/Font", nil))
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"DEFAULT_FONT",
		"Ljavax/microedition/lcdui/Font;",
		font,
	)
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"FONT_HEIGHT",
		"I",
		IntValue(8),
	)
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"MAX_CHARWIDTH",
		"I",
		IntValue(6),
	)
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"graphics",
		"Ljavax/microedition/lcdui/Graphics;",
		ReferenceValue(vm.ScreenGraphics()),
	)
	vm.RegisterStaticField("com/xce/lcdui/XDisplay", "width", "I", IntValue(int32(vm.ScreenWidth)))
	vm.RegisterStaticField("com/xce/lcdui/XDisplay", "height", "I", IntValue(int32(vm.ScreenHeight)))
	vm.RegisterStaticField("com/xce/lcdui/XDisplay", "height2", "I", IntValue(int32(vm.ScreenHeight)))
	vm.RegisterStaticField(
		"com/xce/lcdui/XEventHandler",
		"eventHandler",
		"Lcom/xce/lcdui/XEventHandler;",
		ReferenceValue(vm.NewObject("com/xce/lcdui/XEventHandler", nil)),
	)
	vm.RegisterStaticField(
		"java/lang/System",
		"out",
		"Ljava/io/PrintStream;",
		ReferenceValue(vm.NewObject("java/io/PrintStream", nil)),
	)
	vm.RegisterStaticField(
		"javax/microedition/lcdui/TextField",
		"CONSTRAINT_MASK",
		"I",
		IntValue(0xffff),
	)
	vm.RegisterStaticField(
		"javax/microedition/lcdui/TextField",
		"PASSWORD",
		"I",
		IntValue(0x10000),
	)
	for name, value := range map[string]int32{
		"FACE_MONOSPACE": 32,
		"SIZE_SMALL":     8,
		"STYLE_PLAIN":    0,
	} {
		vm.RegisterStaticField("org/kwis/msp/lcdui/Font", name, "I", IntValue(value))
	}
}

// installCompatibilityNatives contains the less common APIs exposed by SKT,
// XCE, and KWIS handsets. Keeping them here makes the main J2ME surface easy to
// review while still giving every method referenced by the reference corpus a
// concrete host implementation.
func (vm *VM) installCompatibilityNatives() {
	vm.installDisplayCompatibilityNatives()
	vm.installGraphicsCompatibilityNatives()
	vm.installXCECompatibilityNatives()
	vm.installKWISCompatibilityNatives()
}
