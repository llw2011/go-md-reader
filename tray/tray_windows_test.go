package tray

import "testing"

func TestShouldShowOnToggle(t *testing.T) {
	cases := []struct {
		visible bool
		iconic  bool
		want    bool
	}{
		{visible: false, iconic: false, want: true},
		{visible: false, iconic: true, want: true},
		{visible: true, iconic: true, want: true},
		{visible: true, iconic: false, want: false},
	}

	for _, tc := range cases {
		got := shouldShowOnToggle(tc.visible, tc.iconic)
		if got != tc.want {
			t.Fatalf("shouldShowOnToggle(%v, %v) = %v, want %v", tc.visible, tc.iconic, got, tc.want)
		}
	}
}

func TestForceForegroundWindowZeroHandleIsSafe(t *testing.T) {
	ForceForegroundWindow(0)
}
