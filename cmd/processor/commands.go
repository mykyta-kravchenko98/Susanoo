package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

type commandSpec struct {
	name        string
	description string
	handler     func(ctx context.Context, a *App, msg *telegram.Message, args []string) error
}

var commands = []commandSpec{
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
}

// botCommands converts the registered commands into the shape Telegram's
// setMyCommands API expects.
func botCommands() []telegram.BotCommand {
	out := make([]telegram.BotCommand, 0, len(commands))
	for _, c := range commands {
		out = append(out, telegram.BotCommand{Command: c.name, Description: c.description})
	}
	return out
}

// commandInfos converts the registered commands into the shape
// messages.HelpText expects, keeping cmd/processor's commandSpec out of the
// messages package.
func commandInfos() []messages.CommandInfo {
	out := make([]messages.CommandInfo, 0, len(commands))
	for _, c := range commands {
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

	for _, spec := range commands {
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
