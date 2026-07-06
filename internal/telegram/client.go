package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mykyta-kravchenko98/Susanoo/internal/helper"
)

const apiBaseURL = "https://api.telegram.org"

type Client struct {
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(token string, logger *slog.Logger) *Client {
	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
		logger: logger,
	}
}

func (c *Client) apiURL(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", apiBaseURL, c.token, method)
}

type getFileResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
	Description string `json:"description,omitempty"`
}

func (c *Client) GetFilePath(ctx context.Context, fileID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.apiURL("getFile")+"?file_id="+fileID, nil)
	if err != nil {
		return "", fmt.Errorf("build getFile request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call getFile: %w", err)
	}
	defer helper.Close(c.logger, resp.Body)

	var parsed getFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode getFile response: %w", err)
	}
	if !parsed.OK {
		return "", fmt.Errorf("getFile failed: %s", parsed.Description)
	}
	return parsed.Result.FilePath, nil
}

func (c *Client) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	url := fmt.Sprintf("%s/file/bot%s/%s", apiBaseURL, c.token, filePath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer helper.Close(c.logger, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download file: unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read file body: %w", err)
	}
	return data, nil
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type sendMessageRequest struct {
	ChatID      int64  `json:"chat_id"`
	Text        string `json:"text"`
	ReplyMarkup *struct {
		InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
	} `json:"reply_markup,omitempty"`
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, buttons ...InlineButton) error {
	reqBody := sendMessageRequest{ChatID: chatID, Text: text}

	if len(buttons) > 0 {
		reqBody.ReplyMarkup = &struct {
			InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
		}{
			InlineKeyboard: [][]InlineButton{buttons},
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal sendMessage body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiURL("sendMessage"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build sendMessage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call sendMessage: %w", err)
	}
	defer helper.Close(c.logger, resp.Body)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendMessage failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackQueryID string) error {
	body, _ := json.Marshal(map[string]string{"callback_query_id": callbackQueryID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiURL("answerCallbackQuery"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build answerCallbackQuery request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call answerCallbackQuery: %w", err)
	}
	defer helper.Close(c.logger, resp.Body)
	return nil
}
