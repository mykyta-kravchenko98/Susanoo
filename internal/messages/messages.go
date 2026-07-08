package messages

import (
	"fmt"
	"strings"
)

const (
	NoPhotoPrompt = "Send a photo of the letter and I'll process it. " +
		"If it has multiple pages, send them one by one, then press \"Done\"."

	ButtonAddPage = "📄 Add page"

	ButtonDone = "✅ Done"

	ButtonStartOver = "🔄 Start over"

	AddMorePrompt = "Send the next photo."

	NoPhotosYet = "I don't see any photos in the current session — " +
		"send at least one before pressing \"Done\"."

	ProcessingStarted = "Processing the letter, this may take a few seconds…"

	ProcessingFailed = "Something went wrong while processing the letter. " +
		"Please try pressing \"Done\" again in a moment."

	SessionCleared = "Session cleared. Send a new photo to start again."

	ButtonSave = "✅ Save"

	ButtonFix = "✏️ Fix"

	SavingStarted = "Saving…"
	// RequestFixPrompt is sent after clicking ButtonFix. The MVP does not support
	// editing individual fields—it is easier to ask for the letter to be re-photographed.
	RequestFixPrompt = "No problem — please resend the photos of the letter and I'll try again."

	// NothingToConfirm is sent if the user clicked Save, but the session is no longer
	// in the state awaiting confirmation (e.g., it expired due to TTL).
	NothingToConfirm = "There's nothing to confirm right now — the session may have expired. " +
		"Please send the photos again."

	LetterSaved = "Saved to the archive. ✅"
	SaveFailed  = "Something went wrong while saving. Please press \"🔄 Start over\" and resend the photos."

	// ArchiveEmpty is sent by /archive when a chat has no saved letters
	// (either none confirmed yet, or all currently pending deletion).
	ArchiveEmpty = "You don't have any saved letters yet."

	// ArchiveHeader precedes the list of one-button-per-letter rows sent by
	// /archive.
	ArchiveHeader = "Your saved letters:"

	// LetterNotFound covers both a genuinely missing letter_id and a letter
	// that's currently pending deletion - from the user's point of view
	// those look the same ("I can't see it anymore"), so there's no need to
	// leak the distinction.
	LetterNotFound = "Couldn't find that letter — it may have been deleted."

	ButtonRequestPDF   = "📎 Request PDF"
	ButtonDeleteLetter = "🗑 Delete"
)

func PhotoAdded(pageCount int) string {
	return fmt.Sprintf("Photo %d added. Another page, or done?", pageCount)
}

func ClassificationPreview(organization, docType, summaryRU string, actionRequiredRU, deadline *string, urgency string, isOverdue bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "📄 %s\n", docType)
	fmt.Fprintf(&b, "From: %s\n\n", organization)
	fmt.Fprintf(&b, "%s\n\n", summaryRU)

	switch {
	case deadline != nil && isOverdue:
		fmt.Fprintf(&b, "⚠️ Deadline OVERDUE: %s (already passed)\n", *deadline)
	case deadline != nil:
		fmt.Fprintf(&b, "⏰ Deadline: %s\n", *deadline)
	default:
		b.WriteString("⏰ Deadline: not detected\n")
	}

	if actionRequiredRU != nil {
		fmt.Fprintf(&b, "☑️ Action required: %s\n", *actionRequiredRU)
	}

	fmt.Fprintf(&b, "\nUrgency: %s", strings.ToUpper(urgency))

	return b.String()
}

// reminderLeadIn maps a reminders.ReminderKind (passed as a plain string so
// this package doesn't need to import internal/reminders) to the headline
// shown at the top of a deadline reminder message. Keys match the ReminderKind
// constants in internal/reminders/plan.go — keep them in sync.
var reminderLeadIn = map[string]string{
	"advance_7d": "📅 Deadline in a week",
	"advance_3d": "📅 The deadline is in three days.",
	"advance_1d": "⏰ The deadline is tomorrow.",
	"due_day":    "🔴 Today is the deadline.",
}

// DeadlineReminder formats the message sent by the reminder-sender Lambda
// when one of the scheduled reminders fires. kind is the wire value of
// reminders.ReminderKind (e.g. "advance_7d"); an unrecognized kind falls back
// to a generic headline rather than failing, since a slightly worse headline
// is much better than silently dropping the reminder.
func DeadlineReminder(kind, organization, docType, deadline string, actionRequiredRU *string) string {
	var b strings.Builder

	leadIn, ok := reminderLeadIn[kind]
	if !ok {
		leadIn = "⏰ Deadline reminder"
	}

	fmt.Fprintf(&b, "%s: %s\n", leadIn, deadline)
	fmt.Fprintf(&b, "📄 %s — %s\n", docType, organization)

	if actionRequiredRU != nil && *actionRequiredRU != "" {
		fmt.Fprintf(&b, "\n☑️ %s", *actionRequiredRU)
	}

	return b.String()
}

// CommandInfo is one entry in the bot's command menu - shared between the
// /help text and the payload sent to Telegram's setMyCommands so there's a
// single source of truth (see cmd/processor/commands.go).
type CommandInfo struct {
	Name        string
	Description string
}

// HelpText renders the /help message listing every registered command.
func HelpText(cmds []CommandInfo) string {
	var b strings.Builder
	b.WriteString("Available commands:\n\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "/%s — %s\n", c.Name, c.Description)
	}
	return b.String()
}

// UnknownCommand is sent when the user types a "/"-prefixed message that
// doesn't match any registered command.
func UnknownCommand(name string) string {
	return fmt.Sprintf("Unknown command: /%s. Send /help to see what I understand.", name)
}

// maxButtonLabelRunes keeps /archive's per-letter button labels well under
// Telegram's inline button text limit, leaving headroom for the date/dash
// formatting around the organization and doc type.
const maxButtonLabelRunes = 60

// LetterButtonLabel formats the label for one /archive list row, e.g.
// "2026-07-01 · Finanzamt Berlin — Steuerbescheid", truncated if the
// organization/doc type combination runs long.
func LetterButtonLabel(receivedDate, organization, docType string) string {
	label := fmt.Sprintf("%s · %s — %s", receivedDate, organization, docType)

	runes := []rune(label)
	if len(runes) > maxButtonLabelRunes {
		label = string(runes[:maxButtonLabelRunes-1]) + "…"
	}
	return label
}

// LetterDetail renders the message shown when a letter is opened from
// /archive's list (see handleViewLetter). deadline and actionRequiredRU are
// plain strings here (not *string, unlike ClassificationPreview) because
// they come from the already-saved letters.Letter, where an empty string -
// not a nil pointer - means "none".
func LetterDetail(receivedDate, organization, docType, summaryRU, deadline, actionRequiredRU string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "📄 %s\n", docType)
	fmt.Fprintf(&b, "From: %s\n", organization)
	fmt.Fprintf(&b, "Received: %s\n\n", receivedDate)

	if summaryRU != "" {
		fmt.Fprintf(&b, "%s\n\n", summaryRU)
	}

	if deadline != "" {
		fmt.Fprintf(&b, "⏰ Deadline: %s\n", deadline)
	}

	if actionRequiredRU != "" {
		fmt.Fprintf(&b, "☑️ Action required: %s\n", actionRequiredRU)
	}

	return b.String()
}
