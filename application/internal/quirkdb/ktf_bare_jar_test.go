package quirkdb

import "testing"

func TestKTFBareJARProfileRequiresExactDigest(t *testing.T) {
	const digest = "45ed5e7ad00607fe13894b26dc9a5e0657befa811304899f052f84e1b65dc9ef"
	profile, ok := LookupKTFBareJARProfile(digest)
	if !ok || profile.AID != "010100D2" || profile.PID != "PD003813" ||
		profile.MainClass != "DeathGame" || profile.DisplayWidth != 240 ||
		profile.DisplayHeight != 320 {
		t.Fatalf("bare JAR profile = %+v, found=%t", profile, ok)
	}
	if _, ok := LookupKTFBareJARProfile("0" + digest[1:]); ok {
		t.Fatal("lookalike digest selected a bare JAR profile")
	}
}
