package skvmhost

import (
	"reflect"
	"testing"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
)

func TestSKVMXFileResourcesSelectsSupplementaryPackageFiles(t *testing.T) {
	pkg := skloader.Package{
		JARName: "app/game.jar",
		MSDName: "app/game.msd",
		MODName: "app/game.mod",
		WMRName: "app/game.wmr",
		Files: map[string][]byte{
			"app/game.jar":   {1},
			"app/game.msd":   {2},
			"app/game.mod":   {3},
			"app/game.wmr":   {4},
			"app/o":          {5},
			"app/m7.jar":     {6},
			"app/rs/save.sb": {7},
			"app/rs/save.db": {8},
			"other/foreign":  {9},
		},
	}
	want := map[string][]byte{"o": {5}, "m7.jar": {6}}
	if got := skvmXFileResources(pkg); !reflect.DeepEqual(got, want) {
		t.Fatalf("SKVM installed files = %#v, want %#v", got, want)
	}
}
