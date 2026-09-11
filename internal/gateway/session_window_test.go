package gateway

import "testing"

func TestParseSessionPageDefaultsAndBounds(t *testing.T) {
	if got, before, after := parseSessionPage("", "", ""); got != 30 || before != 0 || after != 0 {
		t.Fatalf("got %d %d %d", got, before, after)
	}
	if got, _, _ := parseSessionPage("999", "", ""); got != 200 {
		t.Fatalf("limit=%d", got)
	}
	if got, before, after := parseSessionPage("10", "12", "8"); got != 10 || before != 12 || after != 0 {
		t.Fatalf("got %d %d %d", got, before, after)
	}
}
