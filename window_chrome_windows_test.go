package main

import "testing"

func TestClampMaximizedRect(t *testing.T) {
	mon := winRect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
	work := winRect{Left: 0, Top: 0, Right: 1920, Bottom: 1032}

	got := clampMaximizedRect(work, mon)
	if got != work {
		t.Fatalf("clampMaximizedRect normal = %+v, want %+v", got, work)
	}

	autoHide := clampMaximizedRect(mon, mon)
	if autoHide.Bottom != mon.Bottom-1 {
		t.Fatalf("clampMaximizedRect auto-hide bottom = %d, want %d", autoHide.Bottom, mon.Bottom-1)
	}
}

func TestIsValidResizeHitTest(t *testing.T) {
	for dir := winHtLeft; dir <= winHtBottomRight; dir++ {
		if !isValidResizeHitTest(dir) {
			t.Fatalf("expected dir %d to be valid", dir)
		}
	}
	for _, invalid := range []int{0, 1, 2, 9, 18, -1} {
		if isValidResizeHitTest(invalid) {
			t.Fatalf("expected dir %d to be invalid", invalid)
		}
	}
}
