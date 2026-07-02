package telegram

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	MessageID int64      `json:"message_id"`
	Chat      Chat       `json:"chat"`
	Text      string     `json:"text,omitempty"`
	Photo     []PhotoSize `json:"photo,omitempty"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size,omitempty"`
}

type CallbackQuery struct {
	ID      string  `json:"id"`
	Data    string  `json:"data"`
	Message Message `json:"message"`
}

func (m *Message) LargestPhoto() (PhotoSize, bool) {
	if len(m.Photo) == 0 {
		return PhotoSize{}, false
	}
	best := m.Photo[0]
	for _, p := range m.Photo[1:] {
		if p.Width*p.Height > best.Width*best.Height {
			best = p
		}
	}
	return best, true
}