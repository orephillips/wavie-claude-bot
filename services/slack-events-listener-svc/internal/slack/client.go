package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type Client struct {
	botToken string
	logger   *slog.Logger
	client   *http.Client
}

func NewClient(botToken string, logger *slog.Logger) *Client {
	return &Client{
		botToken: botToken,
		logger:   logger,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) PostMessage(ctx context.Context, channel, text string, threadTS ...string) error {
	payload := MessageResponse{
		Channel: channel,
		Text:    text,
	}
	
	// Add thread_ts if provided
	if len(threadTS) > 0 && threadTS[0] != "" {
		payload.ThreadTS = threadTS[0]
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack API error: %d - %s", resp.StatusCode, string(body))
	}

	c.logger.Info("Message posted to Slack", "channel", channel)
	return nil
}
