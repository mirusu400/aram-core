package ktf

import (
	"reflect"
	"testing"
)

func TestJavaClassResourceCandidates(t *testing.T) {
	tests := []struct {
		name, class, requested string
		want                   []string
	}{
		{
			name:  "class-relative sound",
			class: "sook/Utils/CSound", requested: "sound/title1.mmf",
			want: []string{"sook/Utils/sound/title1.mmf", "sound/title1.mmf"},
		},
		{
			name:  "absolute resource",
			class: "sook/Utils/CSound", requested: "/sound/title1.mmf",
			want: []string{"sound/title1.mmf"},
		},
		{
			name:  "default package",
			class: "CSound", requested: "sound/title1.mmf",
			want: []string{"sound/title1.mmf"},
		},
		{
			name:  "escaping root",
			class: "sook/Utils/CSound", requested: "../secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := javaClassResourceCandidates(tt.class, tt.requested)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("candidates = %v, want %v", got, tt.want)
			}
		})
	}
}
