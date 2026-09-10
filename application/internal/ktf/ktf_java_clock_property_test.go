package ktf

import (
	"strconv"
	"testing"
)

func TestKTFJavaHandsetPropertiesAnswerTheWholeTable(t *testing.T) {
	runtime := newTestRuntime(t)
	// HandsetProperty.getSystemProperty and MC_knlGetSystemProperty share the
	// specification's id set. Answering only part of it left a title parsing
	// the empty string with Integer.parseInt, which throws: several titles
	// died at boot on "VOLUMELEVEL".
	for _, name := range []string{
		"VOLUMELEVEL", "VIBRATORLEVEL", "MAXRSSILEVEL", "RSSILEVEL",
		"MAXBATTLEVEL", "BATTERYLEVEL", "MAXSOCKETNUM", "MAXSERIALNUM",
	} {
		value := runtime.handsetSystemProperty(name)
		if value == "" {
			t.Fatalf("%s is unanswered", name)
		}
		if _, err := strconv.Atoi(value); err != nil {
			t.Fatalf("%s = %q, want a number", name, value)
		}
		shared, ok := runtime.wipicSystemProperty(name)
		if !ok || shared != value {
			t.Fatalf("%s = %q for Java and %q for WIPI-C", name, value, shared)
		}
	}
	if got := runtime.handsetSystemProperty("ZZ-NOT-A-PROPERTY"); got != "" {
		t.Fatalf("unknown property = %q, want the empty string", got)
	}
}

func TestKTFJavaClockCountsFromTheUnixEpoch(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.TickMS = 1_500
	// java.lang.System.currentTimeMillis and MC_knlCurrentTime both count from
	// 1970-01-01. A title that formats the value and indexes into its digits
	// needs the full width; milliseconds since boot alone are a couple of
	// digits at startup.
	now := runtime.wallReadMS()
	if digits := len(strconv.FormatUint(now, 10)); digits < 13 {
		t.Fatalf("currentTimeMillis = %d, only %d digits", now, digits)
	}
	if wall := runtime.wallTickMS(); wall <= 0 ||
		uint64(wall) != uint64(runtime.wallEpochMS())+runtime.TickMS {
		t.Fatalf("Date() time = %d, want the epoch plus %d", wall, runtime.TickMS)
	}
}
