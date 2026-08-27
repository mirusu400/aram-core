package application

import (
	"testing"
	"time"
)

func TestHostVibration(t *testing.T) {
	cases := []struct {
		name      string
		level     uint8
		until     time.Duration
		now       time.Duration
		wantLevel uint8
		wantLeft  time.Duration
	}{
		{name: "inactive", level: 0, until: 0, now: 0, wantLevel: 0, wantLeft: 0},
		{name: "level without deadline", level: 80, until: 0, now: 0, wantLevel: 0, wantLeft: 0},
		{name: "expired", level: 80, until: 100, now: 100, wantLevel: 0, wantLeft: 0},
		{name: "already past", level: 80, until: 100, now: 250, wantLevel: 0, wantLeft: 0},
		{
			name: "active", level: 60,
			until: 500 * time.Millisecond, now: 200 * time.Millisecond,
			wantLevel: 60, wantLeft: 300 * time.Millisecond,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			level, remaining := hostVibration(tc.level, tc.until, tc.now)
			if level != tc.wantLevel || remaining != tc.wantLeft {
				t.Fatalf("hostVibration(%d,%v,%v) = (%d,%v), want (%d,%v)",
					tc.level, tc.until, tc.now, level, remaining, tc.wantLevel, tc.wantLeft)
			}
		})
	}
}
