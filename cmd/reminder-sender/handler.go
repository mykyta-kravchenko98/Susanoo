package main

import (
	"context"
	"fmt"

	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/reminders"
)

func (a *App) Handle(ctx context.Context, payload reminders.Payload) error {
	text := messages.DeadlineReminder(payload.Kind, payload.Organization, payload.DocType, payload.Deadline, payload.ActionRequiredRU)

	if err := a.telegramClient.SendMessage(ctx, payload.ChatID, text); err != nil {
		return fmt.Errorf("send reminder for letter %s (%s): %w", payload.LetterID, payload.Kind, err)
	}

	a.logger.InfoContext(ctx, "deadline reminder sent",
		"chat_id", payload.ChatID,
		"letter_id", payload.LetterID,
		"kind", payload.Kind,
	)
	return nil
}
