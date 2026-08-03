package api

import (
	"strings"
	"testing"
)

func TestStripCuePositioning(t *testing.T) {
	in := "WEBVTT\n\n" +
		"00:00:02.720 --> 00:00:05.829 align:start position:0%\n" +
		"Hello there\n\n" +
		"00:00:06.000 --> 00:00:07.000 line:90% size:80%\n" +
		"Second line\n"

	got := string(stripCuePositioning([]byte(in)))

	if strings.Contains(got, "align:") || strings.Contains(got, "position:") {
		t.Fatalf("settings survived: %q", got)
	}
	if strings.Contains(got, "line:") || strings.Contains(got, "size:") {
		t.Fatalf("placement survived: %q", got)
	}
	if !strings.Contains(got, "00:00:02.720 --> 00:00:05.829\n") {
		t.Fatalf("timing damaged: %q", got)
	}
	if !strings.Contains(got, "Hello there") || !strings.Contains(got, "Second line") {
		t.Fatalf("text lost: %q", got)
	}
}

func TestStripCuePositioningLeavesCleanFilesAlone(t *testing.T) {
	// The machine translation this player writes carries no settings. It must
	// come through byte for byte, or the two would disagree again.
	in := "WEBVTT\n\n00:00:02.720 --> 00:00:10.240\nXin chào\n"
	if got := string(stripCuePositioning([]byte(in))); got != in {
		t.Fatalf("rewrote a clean file:\n%q\n%q", in, got)
	}
}

func TestStripCuePositioningLeavesTextContainingArrowsAlone(t *testing.T) {
	// Only timing lines are touched. A subtitle may legitimately say "-->".
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\na --> b align:start\n"
	if got := string(stripCuePositioning([]byte(in))); got != in {
		t.Fatalf("dialogue damaged:\n%q\n%q", in, got)
	}
}
