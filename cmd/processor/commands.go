package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

// commandSpec is one registered "/" command: its Telegram-menu name and
// description, and the handler that runs it. This is the single source of
// truth for three things that must otherwise stay in sync by hand: the
// dispatch table below, the /help text, and the payload sent to Telegram's
// setMyCommands (see buildApp) - add an entry here and all three update
// together.
//
// Future PRs (letters, reminders) add their own entries to the commands
// slice below rather than inventing a second dispatch mechanism.
type commandSpec struct {
	name        string
	description string
	handler     func(ctx context.Context, a *App, msg *telegram.Message, args []string) error
}

func allCommands() []commandSpec {
	return []commandSpec{
		{
			name:        "help",
			description: "Show available commands",
			handler:     handleHelpCommand,
		},
		{
			name:        "start",
			description: "Show a short intro",
			handler:     handleHelpCommand,
		},
		{
			name:        "archive",
			description: "List your saved letters",
			handler:     handleArchiveCommand,
		},
	}
}

const callbackViewLetterPrefix = "view:"

// callbackArchiveOrgPrefix marks callback data as "show years for this
// organization" - followed by the organization's slug (see
// letters.SanitizeForKey / letters.OrganizationSummary.Slug), e.g.
// "archorg:finanzamt-berlin". Slugs are already lowercase ASCII with no
// colons, so appending one directly after the prefix is unambiguous to
// parse back out (see handleArchiveOrg in handlers.go).
const callbackArchiveOrgPrefix = "archorg:"

// callbackArchiveYearPrefix marks callback data as "show letters for this
// organization+year" - followed by "{org_slug}:{year}", e.g.
// "archyr:finanzamt-berlin:2026". The colon separator is safe because a
// slug never contains one (see letters.SanitizeForKey).
const callbackArchiveYearPrefix = "archyr:"

// handleArchiveCommand is the top of /archive's organization -> year ->
// letter drill-down: it lists the chat's distinct organizations. Tapping one
// leads to handleArchiveOrg, then handleArchiveYear, then the existing
// letter-view flow (callbackViewLetterPrefix). All three levels are
// stateless - each callback carries everything needed to answer it, nothing
// is remembered between taps.
func handleArchiveCommand(ctx context.Context, a *App, msg *telegram.Message, _ []string) error {
	orgs, err := a.letters.QueryOrganizations(ctx, msg.Chat.ID)
	if err != nil {
		return err
	}

	if len(orgs) == 0 {
		return a.telegramClient.SendMessage(ctx, msg.Chat.ID, messages.ArchiveEmpty)
	}

	rows := make([][]telegram.InlineButton, 0, len(orgs))
	for _, org := range orgs {
		label := messages.OrganizationButtonLabel(org.Name, org.Count)
		rows = append(rows, []telegram.InlineButton{
			{Text: label, CallbackData: callbackArchiveOrgPrefix + org.Slug},
		})
	}

	return a.telegramClient.SendMessageWithRows(ctx, msg.Chat.ID, messages.ArchiveHeader, rows)
}

// botCommands converts the registered commands into the shape Telegram's
// setMyCommands API expects.
func botCommands() []telegram.BotCommand {
	cmds := allCommands()
	out := make([]telegram.BotCommand, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, telegram.BotCommand{Command: c.name, Description: c.description})
	}
	return out
}

// commandInfos converts the registered commands into the shape
// messages.HelpText expects, keeping cmd/processor's commandSpec out of the
// messages package.
func commandInfos() []messages.CommandInfo {
	cmds := allCommands()
	out := make([]messages.CommandInfo, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, messages.CommandInfo{Name: c.name, Description: c.description})
	}
	return out
}

// parseCommand extracts the command name (lowercased, without the leading
// "/" or a trailing "@BotName" - Telegram clients sometimes append the bot's
// username, especially in groups) and any whitespace-separated arguments
// from a message's Text. ok is false if text isn't a "/"-prefixed command at
// all.
func parseCommand(text string) (name string, args []string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", nil, false
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", nil, false
	}

	first := strings.TrimPrefix(fields[0], "/")
	if at := strings.IndexByte(first, '@'); at >= 0 {
		first = first[:at]
	}
	if first == "" {
		return "", nil, false
	}

	return strings.ToLower(first), fields[1:], true
}

// dispatchCommand routes a "/"-prefixed message to its registered handler.
// Called from handleTelegramUpdate once a message's Text has already been
// identified as looking like a command.
func (a *App) dispatchCommand(ctx context.Context, msg *telegram.Message) error {
	name, args, ok := parseCommand(msg.Text)
	if !ok {
		// Shouldn't happen given the caller already checked the "/" prefix,
		// but fall back to the old default rather than erroring.
		return a.telegramClient.SendMessage(ctx, msg.Chat.ID, messages.NoPhotoPrompt)
	}

	for _, spec := range allCommands() {
		if spec.name == name {
			return spec.handler(ctx, a, msg, args)
		}
	}

	a.logger.InfoContext(ctx, "unknown command", slog.String("command", name))
	return a.telegramClient.SendMessage(ctx, msg.Chat.ID, messages.UnknownCommand(name))
}

func handleHelpCommand(ctx context.Context, a *App, msg *telegram.Message, _ []string) error {
	return a.telegramClient.SendMessage(ctx, msg.Chat.ID, messages.HelpText(commandInfos()))
}
