package main

import "testing"

func TestAppendActivityEntryMergesToolResultLines(t *testing.T) {
	m := chatTUIModel{}

	m.appendActivityEntry("tools", `[1/1] run_command echo "hi"`)
	m.appendActivityEntry("steps", "ok in 12ms")
	m.appendActivityEntry("steps", "| 1 line, first: hi")

	if len(m.entries) != 1 {
		t.Fatalf("unexpected entry count: %d", len(m.entries))
	}
	got := m.entries[0]
	if got.role != "activity" {
		t.Fatalf("unexpected role: %q", got.role)
	}
	want := "[tool] [1/1] run_command echo \"hi\"\nok in 12ms\n1 line, first: hi"
	if got.text != want {
		t.Fatalf("unexpected merged activity:\n%s", got.text)
	}
}

func TestNextStepStatusSkipsSummaryLines(t *testing.T) {
	current := "run_command echo hi"
	got := nextStepStatus(current, "steps", "| 1 line, first: hi")
	if got != current {
		t.Fatalf("summary line should not replace status: got %q want %q", got, current)
	}
}
