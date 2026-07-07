package reminders

import (
	"testing"
	"time"
)

// berlinDate builds a Europe/Berlin calendar date at reminderHour, matching
// how Plan's own AddDate/time.Date arithmetic constructs fire times, so tests
// compare against exactly the same kind of value Plan produces.
func berlinDate(t *testing.T, day int) time.Time {
	t.Helper()
	return time.Date(2026, time.July, day, reminderHour, 0, 0, 0, berlin)
}

func TestPlan_HighUrgency_FarFutureDeadline(t *testing.T) {
	now := berlinDate(t, 1)
	deadline := berlinDate(t, 21) // 20 days out

	got := Plan(deadline, now, "high")

	want := []Reminder{
		{Kind: KindAdvance7Days, At: berlinDate(t, 14)},
		{Kind: KindAdvance1Day, At: berlinDate(t, 20)},
	}
	assertReminders(t, want, got)
}

func TestPlan_HighUrgency_OnlyOneOffsetStillFuture(t *testing.T) {
	// Deadline in 3 days: the -7d offset is already in the past and must be
	// dropped, but -1d is still ahead and must survive.
	now := berlinDate(t, 1)
	deadline := berlinDate(t, 4)

	got := Plan(deadline, now, "high")

	want := []Reminder{
		{Kind: KindAdvance1Day, At: berlinDate(t, 3)},
	}
	assertReminders(t, want, got)
}

func TestPlan_HighUrgency_DeadlineAlreadyPassed(t *testing.T) {
	now := berlinDate(t, 10)
	deadline := berlinDate(t, 1) // 9 days ago

	got := Plan(deadline, now, "high")

	if len(got) != 0 {
		t.Fatalf("expected no reminders for a deadline already in the past, got %+v", got)
	}
}

func TestPlan_MediumUrgency(t *testing.T) {
	now := berlinDate(t, 1)
	deadline := berlinDate(t, 11)

	got := Plan(deadline, now, "medium")

	want := []Reminder{
		{Kind: KindAdvance3Days, At: berlinDate(t, 8)},
	}
	assertReminders(t, want, got)
}

func TestPlan_LowUrgency(t *testing.T) {
	now := berlinDate(t, 1)
	deadline := berlinDate(t, 6)

	got := Plan(deadline, now, "low")

	want := []Reminder{
		{Kind: KindDueDay, At: berlinDate(t, 6)},
	}
	assertReminders(t, want, got)
}

func TestPlan_UnknownUrgency_FallsBackToDueDay(t *testing.T) {
	now := berlinDate(t, 1)
	deadline := berlinDate(t, 6)

	got := Plan(deadline, now, "")

	want := []Reminder{
		{Kind: KindDueDay, At: berlinDate(t, 6)},
	}
	assertReminders(t, want, got)
}

func TestPlan_LowUrgency_ReminderHourAlreadyPassedToday(t *testing.T) {
	// Deadline is today, but "now" is already past the 08:00 reminder hour —
	// the due-day reminder must not fire in the past, even same-day.
	deadline := berlinDate(t, 6)
	now := time.Date(2026, time.July, 6, 15, 0, 0, 0, berlin)

	got := Plan(deadline, now, "low")

	if len(got) != 0 {
		t.Fatalf("expected no reminders once today's reminder hour has already passed, got %+v", got)
	}
}

func assertReminders(t *testing.T, want, got []Reminder) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d reminders, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind {
			t.Errorf("reminder %d: expected kind %q, got %q", i, want[i].Kind, got[i].Kind)
		}
		if !got[i].At.Equal(want[i].At) {
			t.Errorf("reminder %d (%s): expected fire time %v, got %v", i, want[i].Kind, want[i].At, got[i].At)
		}
	}
}
