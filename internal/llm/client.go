package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	apiURL        = "https://api.anthropic.com/v1/messages"
	apiVersion    = "2023-06-01"
	model         = "claude-haiku-4-5-20251001"
	maxOutputTokens = 1024
)

type ExtractedFields struct {
	Organization        string  `json:"organization"`
	DocType             string  `json:"doc_type"`
	Filename            string  `json:"filename"`
	Summary             string  `json:"summary"`
	SummaryRU           string  `json:"summary_ru"`
	Deadline            *string `json:"deadline"`          // ISO 8601 (YYYY-MM-DD) или null
	ActionRequired       *string `json:"action_required"`
	ActionRequiredRU     *string `json:"action_required_ru"`
	Urgency             string  `json:"urgency"` // high|medium|low
}

type Client struct {
	apiKey     string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
}


type contentBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *imageSource `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type messageRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type messageResponse struct {
	Content []contentBlock `json:"content"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) ClassifyLetter(ctx context.Context, images [][]byte, receivedDate string) (*ExtractedFields, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("no images provided")
	}

	content := make([]contentBlock, 0, len(images)+1)
	for _, img := range images {
		content = append(content, contentBlock{
			Type: "image",
			Source: &imageSource{
				Type:      "base64",
				MediaType: "image/jpeg",
				Data:      base64.StdEncoding.EncodeToString(img),
			},
		})
	}
	content = append(content, contentBlock{
		Type: "text",
		Text: fmt.Sprintf(userPromptTemplate, receivedDate),
	})

	reqBody := messageRequest{
		Model:     model,
		MaxTokens: maxOutputTokens,
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: content},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call anthropic api: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	var parsed messageResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, fmt.Errorf("decode response (status %d): %w, body: %s", resp.StatusCode, err, string(respBytes))
	}

	if parsed.Error != nil {
		return nil, fmt.Errorf("anthropic api error (%s): %s", parsed.Error.Type, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic api unexpected status %d, body: %s", resp.StatusCode, string(respBytes))
	}

	var rawText string
	for _, block := range parsed.Content {
		if block.Type == "text" {
			rawText = block.Text
			break
		}
	}
	if rawText == "" {
		return nil, fmt.Errorf("no text content in anthropic response")
	}

	fields, err := parseJSONResponse(rawText)
	if err != nil {
		return nil, fmt.Errorf("parse model output as JSON: %w (raw: %s)", err, rawText)
	}
	return fields, nil
}

func parseJSONResponse(raw string) (*ExtractedFields, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	var fields ExtractedFields
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil, err
	}
	return &fields, nil
}