package main

type imagesToProcessMessage struct {
	ChatID  int64    `json:"chat_id"`
	RawKeys []string `json:"raw_keys"`
}

type processedImagesMessage struct {
	ChatID        int64    `json:"chat_id"`
	ProcessedKeys []string `json:"processed_keys"`
}
