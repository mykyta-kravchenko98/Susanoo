package messages

import "fmt"

const (
	NoPhotoPrompt = "Send a photo of the letter and I'll process it. " +
		"If it has multiple pages, send them one by one, then press \"Done\"."

	ButtonAddPage = "📄 Add page"

	ButtonDone = "✅ Done"

	AddMorePrompt = "Send the next photo."

	NoPhotosYet = "I don't see any photos in the current session — " +
		"send at least one before pressing \"Done\"."

	ProcessingStarted = "Processing the letter, this may take a few seconds…"
)

func PhotoAdded(pageCount int) string {
	return fmt.Sprintf("Photo %d added. Another page, or done?", pageCount)
}