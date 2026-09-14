package gateway

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectRestoredMessagesBoundsPresentationOnly(t *testing.T) {
	original := strings.Repeat("秘密内容 ", 20000)
	got, truncated := projectRestoredMessages([]RestoredMessage{{ID: "entry-1", Seq: 7, Role: "assistant", Content: original, ToolSteps: []ToolStep{{ID: "call-1", Tool: "read", Args: "{}", Result: original}}}})
	if !truncated || len(got) != 1 || got[0].ID != "entry-1" || got[0].Seq != 7 {
		t.Fatalf("bad projection: %+v truncated=%v", got, truncated)
	}
	if len(got[0].Content) > maxHistoryMessageBytes || len(got[0].ToolSteps[0].Result) > maxHistoryToolBytes {
		t.Fatalf("projection exceeded bounds")
	}
	if !got[0].ContentTruncated || !got[0].ToolSteps[0].ResultTruncated {
		t.Fatalf("missing truncation markers")
	}
	if got[0].Content == original {
		t.Fatalf("content was not projected")
	}
}

func TestTruncateHistoryTextDoesNotSplitUTF8(t *testing.T) {
	got, changed := truncateHistoryText(strings.Repeat("中文", 100), 80)
	if !changed || !utf8.ValidString(got) || !strings.HasSuffix(got, "…]") {
		t.Fatalf("got %q", got)
	}
}
