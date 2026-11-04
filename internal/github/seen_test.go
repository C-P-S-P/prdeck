package github

import (
	"slices"
	"testing"
	"time"
)

func TestSinceSeen(t *testing.T) {
	seen := time.Now().Add(-time.Hour)
	before, after := seen.Add(-time.Minute), seen.Add(time.Minute)
	d := &Detail{Timeline: []Event{
		{Kind: "review", Author: "mlin", At: before},
		{Kind: "thread", Author: "mlin", At: before, Replies: []Reply{{Author: "You", At: after}, {Author: "mlin", At: after}}},
		{Kind: "comment", Author: "rdavis", At: after},
		{Kind: "comment", Author: "you", At: after},
	}}
	got := d.SinceSeen(seen, "you")
	var marks []bool
	for _, e := range got.Timeline {
		marks = append(marks, e.New)
	}
	if want := []bool{false, false, true, false}; !slices.Equal(marks, want) {
		t.Errorf("events new = %v, want %v (yours never are)", marks, want)
	}
	if r := got.Timeline[1].Replies; r[0].New || !r[1].New {
		t.Errorf("an old thread's new reply from someone else is new: %+v", r)
	}
	if !got.LastSeen.Equal(seen) {
		t.Errorf("LastSeen = %v", got.LastSeen)
	}
	if d.Timeline[2].New || d.Timeline[1].Replies[1].New {
		t.Error("the saved detail was changed")
	}
	if d.SinceSeen(time.Time{}, "you").Timeline[2].New {
		t.Error("without a last visit nothing is new")
	}
}
