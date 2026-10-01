package raptor

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorJavaCheckTypeRejectsSiblingClasses(t *testing.T) {
	public := newPublicRuntime(t)
	item := &raptorJavaClass{
		Holder: 0x01400000,
		Name:   "game/Item",
	}
	equipment := &raptorJavaClass{
		Holder:     0x01400040,
		Name:       "game/Equipment",
		parentName: item.Name,
	}
	potion := &raptorJavaClass{
		Holder:     0x01400080,
		Name:       "game/Potion",
		parentName: item.Name,
	}
	java := &JavaRuntime{
		classes: map[uint32]*raptorJavaClass{
			item.Holder:      item,
			equipment.Holder: equipment,
			potion.Holder:    potion,
		},
		ClassByName: map[string]*raptorJavaClass{
			item.Name:      item,
			equipment.Name: equipment,
			potion.Name:    potion,
		},
	}
	runtime := &Runtime{CPU: public.CPU, Public: public, Java: java}

	tests := []struct {
		name   string
		target uint32
		actual uint32
		want   uint32
	}{
		{name: "same type", target: equipment.Holder, actual: equipment.Holder, want: 1},
		{name: "subclass is item", target: item.Holder, actual: equipment.Holder, want: 1},
		{name: "item is not equipment", target: equipment.Holder, actual: item.Holder},
		{name: "potion is not equipment", target: equipment.Holder, actual: potion.Holder},
		{name: "null actual", target: equipment.Holder},
		{name: "legacy null target", actual: 0x43, want: 1},
		{name: "unresolved compact tokens", target: 0x41, actual: 0x43, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, test.target))
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, test.actual))
			result, name, handled, err := runtime.checkRaptorJavaType()
			check(t, err)
			if name != "RAPTOR.java.checkType" || !handled {
				t.Fatalf("dispatch metadata = (%q, %t), want (%q, true)", name, handled, "RAPTOR.java.checkType")
			}
			if result.Low != test.want {
				t.Fatalf("checkType(0x%08x, 0x%08x) = %d, want %d", test.target, test.actual, result.Low, test.want)
			}
		})
	}
}
