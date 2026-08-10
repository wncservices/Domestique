package api

import "testing"

// Thirty routes failing is almost always one cause thirty times. The log
// wants the cause, once.
func TestDistinctReasonsCollapseRepetition(t *testing.T) {
	messages := []string{
		"garmin:wilant kluisbergen: create failed: garmin: the course import returned 401",
		"garmin:wilant kemmelberg: create failed: garmin: the course import returned 401",
		"garmin:wilant paterberg: update failed: garmin: the course import returned 401",
	}

	got := distinctReasons(messages)
	if len(got) != 1 {
		t.Fatalf("reasons = %v, want the one cause", got)
	}
	if got[0] != "garmin: the course import returned 401" {
		t.Errorf("reason = %q, want the cause without the route prefix", got[0])
	}
}

// Different causes are different lines: collapsing them would hide the one
// account that is failing for its own reason.
func TestDistinctReasonsKeepDifferentCauses(t *testing.T) {
	got := distinctReasons([]string{
		"garmin:wilant a: create failed: expired session",
		"garmin:friend b: create failed: has not connected Garmin",
	})
	if len(got) != 2 {
		t.Errorf("reasons = %v, want both causes", got)
	}
}

// A message that does not match the expected shape is still worth logging.
// Dropping it would be the one case where the log says nothing at all.
func TestDistinctReasonsKeepUnparseableMessages(t *testing.T) {
	got := distinctReasons([]string{"something nobody predicted"})
	if len(got) != 1 || got[0] != "something nobody predicted" {
		t.Errorf("reasons = %v, want the message as-is", got)
	}
}

// A push against a large library must not put a hundred lines in one log
// entry.
func TestDistinctReasonsAreBounded(t *testing.T) {
	var messages []string
	for _, cause := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		messages = append(messages, "acct slug: create failed: "+cause)
	}
	if got := distinctReasons(messages); len(got) != 5 {
		t.Errorf("reasons = %v, want at most 5", got)
	}
}

func TestDistinctReasonsHandlesNone(t *testing.T) {
	if got := distinctReasons(nil); len(got) != 0 {
		t.Errorf("reasons = %v, want none", got)
	}
}
