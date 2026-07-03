package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const maxOutputTokens = 1024

type ExtractedFields struct {
	NeedsRotation            bool    `json:"needs_rotation"`
	RotationClockwiseDegrees int     `json:"rotation_clockwise_degrees"`
	Organization             string  `json:"organization"`
	DocType                  string  `json:"doc_type"`
	Filename                 string  `json:"filename"`
	Summary                  string  `json:"summary"`
	SummaryRU                string  `json:"summary_ru"`
	Deadline                 *string `json:"deadline"` // ISO 8601 (YYYY-MM-DD) или null
	ActionRequired           *string `json:"action_required"`
	ActionRequiredRU         *string `json:"action_required_ru"`
	Urgency                  string  `json:"urgency"` // high|medium|low
}

type Client struct {
	client anthropic.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
	}
}

const maxRotationRetries = 1

func (c *Client) ClassifyLetter(
	ctx context.Context,
	images [][]byte,
	receivedDate string,
	rotateFn func(images [][]byte, clockwiseDegrees int) ([][]byte, error),
) (*ExtractedFields, [][]byte, error) {
	if len(images) == 0 {
		return nil, nil, fmt.Errorf("no images provided")
	}

	current := images
	for attempt := 0; ; attempt++ {
		fields, err := c.classifyOnce(ctx, current, receivedDate)
		if err != nil {
			return nil, nil, err
		}

		if !fields.NeedsRotation {
			return fields, current, nil
		}

		if attempt >= maxRotationRetries {
			return nil, nil, fmt.Errorf("model still reports needs_rotation after %d retries, giving up", maxRotationRetries)
		}

		rotated, err := rotateFn(current, fields.RotationClockwiseDegrees)
		if err != nil {
			return nil, nil, fmt.Errorf("rotate images by %d degrees: %w", fields.RotationClockwiseDegrees, err)
		}
		current = rotated
	}
}

func (c *Client) classifyOnce(ctx context.Context, images [][]byte, receivedDate string) (*ExtractedFields, error) {
	contentBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(images)+1)
	for _, img := range images {
		encoded := base64.StdEncoding.EncodeToString(img)
		contentBlocks = append(contentBlocks, anthropic.NewImageBlockBase64("image/jpeg", encoded))
	}
	contentBlocks = append(contentBlocks, anthropic.NewTextBlock(fmt.Sprintf(userPromptTemplate, receivedDate)))

	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:        anthropic.ModelClaudeHaiku4_5,
		MaxTokens:    maxOutputTokens,
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(contentBlocks...),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("call anthropic api: %w", err)
	}

	var rawText string
	for _, block := range resp.Content {
		if text := block.AsText(); text.Text != "" {
			rawText = text.Text
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
