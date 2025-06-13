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

// PostFeedbackMessage sends a feedback message to the specified channel
func (c *Client) PostFeedbackMessage(ctx context.Context, channelID string, req FeedbackRequest) error {
	// Create different blocks based on feedback type
	blocks := []MessageBlock{
		{
			Type: "section",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*Wavie Feedback*\n*User:* <@%s>\n*Channel:* <#%s>\n*Time:* %s",
					req.UserID,
					req.ChannelID,
					req.Timestamp.Format("2006-01-02 15:04:05 UTC")),
			},
		},
	}

	// Add feedback type-specific blocks
	switch req.FeedbackType {
	case "positive":
		blocks = append(blocks, MessageBlock{
			Type: "section",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: ":thumbsup: *Positive Feedback*",
			},
		})
	case "negative":
		blocks = append(blocks, MessageBlock{
			Type: "section",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: ":thumbsdown: *Negative Feedback*",
			},
		})
	case "text":
		blocks = append(blocks, MessageBlock{
			Type: "section",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*Detailed Feedback:*\n%s", req.FeedbackText),
			},
		})
	}

	// Add context information
	blocks = append(blocks, MessageBlock{
		Type: "context",
		Text: &TextObject{
			Type: "mrkdwn",
			Text: fmt.Sprintf("Correlation ID: `%s`", req.CorrelationID),
		},
	})

	message := SlackMessage{
		Channel: channelID,
		Blocks:  blocks,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack API error: %d - %s", resp.StatusCode, string(body))
	}

	c.logger.Info("Feedback message posted to Slack",
		"channel", channelID,
		"feedback_type", req.FeedbackType,
		"correlation_id", req.CorrelationID)
	return nil
}

func (c *Client) PostBroadcastMessage(ctx context.Context, channelID string, req BroadcastRequest) error {
	blocks := []MessageBlock{
		{
			Type: "section",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*Wavie Interaction*\n*User:* <@%s>\n*Channel:* <#%s>\n*Time:* %s",
					req.UserID,
					req.ChannelID,
					req.Timestamp.Format("2006-01-02 15:04:05 UTC")),
			},
		},
		{
			Type: "section",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*Question:*\n%s", req.Question),
			},
		},
		{
			Type: "section",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*Response:*\n%s", req.Response),
			},
		},
		{
			Type: "context",
			Text: &TextObject{
				Type: "mrkdwn",
				Text: fmt.Sprintf("Correlation ID: `%s`", req.CorrelationID),
			},
		},
	}

	message := SlackMessage{
		Channel: channelID,
		Blocks:  blocks,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack API error: %d - %s", resp.StatusCode, string(body))
	}

	c.logger.Info("Broadcast message posted to Slack",
		"channel", channelID,
		"correlation_id", req.CorrelationID)
	return nil
}
