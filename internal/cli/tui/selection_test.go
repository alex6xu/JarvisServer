package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// TestRowRange checks the per-row column span for single- and multi-row
// selections, and that rows outside the range report ok=false.
func TestRowRange(t *testing.T) {
	start, end := point{3, 1}, point{7, 3}

	if _, _, ok := rowRange(start, end, 0); ok {
		t.Error("row above the selection should not be selected")
	}
	if c0, c1, ok := rowRange(start, end, 1); !ok || c0 != 3 || c1 != maxCol {
		t.Errorf("first row = (%d,%d,%v), want (3,maxCol,true)", c0, c1, ok)
	}
	if c0, c1, ok := rowRange(start, end, 2); !ok || c0 != 0 || c1 != maxCol {
		t.Errorf("interior row = (%d,%d,%v), want (0,maxCol,true)", c0, c1, ok)
	}
	if c0, c1, ok := rowRange(start, end, 3); !ok || c0 != 0 || c1 != 7 {
		t.Errorf("last row = (%d,%d,%v), want (0,7,true)", c0, c1, ok)
	}
	if _, _, ok := rowRange(start, end, 4); ok {
		t.Error("row below the selection should not be selected")
	}

	// A single-row selection uses [start.x, end.x).
	if c0, c1, ok := rowRange(point{2, 5}, point{9, 5}, 5); !ok || c0 != 2 || c1 != 9 {
		t.Errorf("single row = (%d,%d,%v), want (2,9,true)", c0, c1, ok)
	}
}

// TestSelectRowExtracts verifies the selected text is the column-clipped slice
// of the row, measured in display cells so CJK is never split, and that ANSI in
// the source row is stripped before slicing.
func TestSelectRowExtracts(t *testing.T) {
	hi := lipgloss.NewStyle().Reverse(true)

	if _, text := selectRow("hello world", 0, 5, hi); text != "hello" {
		t.Errorf("selected %q, want %q", text, "hello")
	}
	if _, text := selectRow("hello world", 6, maxCol, hi); text != "world" {
		t.Errorf("selected %q, want %q", text, "world")
	}

	// ANSI coloring in the source is stripped before the selection is measured.
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("hello")
	if _, text := selectRow(styled, 0, maxCol, hi); text != "hello" {
		t.Errorf("selected %q from styled row, want %q", text, "hello")
	}

	// CJK counts as two columns: selecting the first two cells yields one rune.
	if _, text := selectRow("你好ab", 0, 2, hi); text != "你" {
		t.Errorf("selected %q, want %q (double-width clipped on a cell boundary)", text, "你")
	}
}
