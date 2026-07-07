package reminders

import (
	"strings"
	"time"

	// Blank-imported so the IANA timezone database is embedded in the binary.
	// AWS Lambda's provided.al2023 runtime does not ship system tzdata, so
	// time.LoadLocation("Europe/Berlin") would otherwise fail at cold start.
	_ "time/tzdata"
)

type ReminderKind string

const (
	KindAdvance7Days ReminderKind = "advance_7d"
	KindAdvance3Days ReminderKind = "advance_3d"
	KindAdvance1Day  ReminderKind = "advance_1d"
	KindDueDay       ReminderKind = "due_day"
)

type Reminder struct {
	Kind ReminderKind
	At   time.Time
}

const reminderHour = 8

var berlin = mustLoadBerlin()

func mustLoadBerlin() *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		// Should be unreachable given the time/tzdata blank import above;
		// falling back to UTC is safer than panicking a Lambda cold start
		// over what is at most a 1-2 hour (DST-only) skew in reminder time.
		return time.UTC
	}
	return loc
}

type offset struct {
	kind ReminderKind
	days int
}

// offsetsFor returns the days-before-deadline offsets for a given urgency,
// per the project's notes: high gets two reminders (a week out, then a day
// out), medium gets one three days out, and low (or anything unrecognized)
// gets a single same-day reminder.
func offsetsFor(urgency string) []offset {
	switch strings.ToLower(strings.TrimSpace(urgency)) {
	case "high":
		return []offset{
			{KindAdvance7Days, 7},
			{KindAdvance1Day, 1},
		}
	case "medium":
		return []offset{
			{KindAdvance3Days, 3},
		}
	default:
		return []offset{
			{KindDueDay, 0},
		}
	}
}

func Plan(deadline, now time.Time, urgency string) []Reminder {
	offsets := offsetsFor(urgency)

	out := make([]Reminder, 0, len(offsets))
	for _, o := range offsets {
		fireDate := deadline.AddDate(0, 0, -o.days)
		fireAt := time.Date(fireDate.Year(), fireDate.Month(), fireDate.Day(), reminderHour, 0, 0, 0, berlin)
		if fireAt.After(now) {
			out = append(out, Reminder{Kind: o.kind, At: fireAt})
		}
	}
	return out
}
