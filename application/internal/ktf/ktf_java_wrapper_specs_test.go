package ktf

import "testing"

func TestKTFWrapperNativeOverridesCoverImplementedMethods(t *testing.T) {
	signatures := []string{
		"java/lang/Long.<init>(J)V",
		"java/lang/Long.longValue()J",
		"java/lang/Long.floatValue()F",
		"java/lang/Long.doubleValue()D",
		"java/lang/Long.toString()Ljava/lang/String;",
		"java/lang/Long.equals(Ljava/lang/Object;)Z",
		"java/lang/Long.hashCode()I",
		"java/lang/Long.parseLong(Ljava/lang/String;)J",
		"java/lang/Long.parseLong(Ljava/lang/String;I)J",
		"java/lang/Long.toString(J)Ljava/lang/String;",
		"java/lang/Long.toString(JI)Ljava/lang/String;",

		"java/lang/Float.<init>(F)V",
		"java/lang/Float.<init>(D)V",
		"java/lang/Float.byteValue()B",
		"java/lang/Float.shortValue()S",
		"java/lang/Float.intValue()I",
		"java/lang/Float.longValue()J",
		"java/lang/Float.floatValue()F",
		"java/lang/Float.doubleValue()D",
		"java/lang/Float.isNaN()Z",
		"java/lang/Float.isInfinite()Z",
		"java/lang/Float.toString()Ljava/lang/String;",
		"java/lang/Float.equals(Ljava/lang/Object;)Z",
		"java/lang/Float.hashCode()I",
		"java/lang/Float.floatToIntBits(F)I",
		"java/lang/Float.intBitsToFloat(I)F",
		"java/lang/Float.isNaN(F)Z",
		"java/lang/Float.isInfinite(F)Z",
		"java/lang/Float.parseFloat(Ljava/lang/String;)F",
		"java/lang/Float.toString(F)Ljava/lang/String;",
		"java/lang/Float.valueOf(Ljava/lang/String;)Ljava/lang/Float;",

		"java/lang/Double.<init>(D)V",
		"java/lang/Double.byteValue()B",
		"java/lang/Double.shortValue()S",
		"java/lang/Double.intValue()I",
		"java/lang/Double.longValue()J",
		"java/lang/Double.floatValue()F",
		"java/lang/Double.doubleValue()D",
		"java/lang/Double.isNaN()Z",
		"java/lang/Double.isInfinite()Z",
		"java/lang/Double.toString()Ljava/lang/String;",
		"java/lang/Double.equals(Ljava/lang/Object;)Z",
		"java/lang/Double.hashCode()I",
		"java/lang/Double.doubleToLongBits(D)J",
		"java/lang/Double.longBitsToDouble(J)D",
		"java/lang/Double.isNaN(D)Z",
		"java/lang/Double.isInfinite(D)Z",
		"java/lang/Double.parseDouble(Ljava/lang/String;)D",
		"java/lang/Double.parseDouble0(Ljava/lang/String;)D",
		"java/lang/Double.toString(D)Ljava/lang/String;",
		"java/lang/Double.valueOf(Ljava/lang/String;)Ljava/lang/Double;",
	}
	for _, signature := range signatures {
		if _, ok := ktfJavaNativeOverride(signature); !ok {
			t.Errorf("native override missing for %s", signature)
		}
	}
}

func TestKTFLongHostClassCanBeAllocated(t *testing.T) {
	runtime := newTestRuntime(t)
	instance, err := runtime.NewHostJavaObject("java/lang/Long")
	check(t, err)
	if instance == 0 {
		t.Fatal("Long allocation returned null")
	}
}
