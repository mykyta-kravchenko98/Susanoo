package helper

import (
	"io"
	"log/slog"
)

func Close(log *slog.Logger, c io.Closer) {
	if err := c.Close(); err != nil {
		log.Warn("failed to close resource", "error", err)
	}
}
