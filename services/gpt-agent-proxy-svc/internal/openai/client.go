package openai

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
	apiKey string
	model  string
	logger *slog.Logger
	client *http.Client
}

func NewClient(apiKey, model string, logger *slog.Logger) *Client {
	return &Client{
		apiKey: apiKey,
		model:  model,
		logger: logger,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ChatCompletion sends a single message to OpenAI without conversation history
func (c *Client) ChatCompletion(ctx context.Context, userMessage, correlationID string) (string, error) {
	messages := []Message{
		{
			Role:    "system",
			Content: "You are Wavie, a helpful AI assistant for Bitwave. You provide clear, concise, and helpful responses to user questions. Keep your responses professional but friendly.",
		},
		{
			Role:    "user",
			Content: userMessage,
		},
	}

	return c.sendChatRequest(ctx, messages, correlationID)
}

// ChatCompletionWithHistory sends a message to OpenAI with conversation history
func (c *Client) ChatCompletionWithHistory(ctx context.Context, userMessage string, history []Message, correlationID string) (string, error) {
	// Start with system message
	messages := []Message{
		{
			Role:    "system",
			Content: "You are Wavie, a helpful AI assistant for Bitwave. You provide clear, concise, and helpful responses to user questions. Keep your responses professional but friendly.",
		},
	}

	// Add conversation history if available
	if len(history) > 0 {
		c.logger.Info("Adding conversation history", "history_length", len(history))
		messages = append(messages, history...)
	}

	// Add the current user message
	messages = append(messages, Message{
		Role:    "user",
		Content: userMessage,
	})

	return c.sendChatRequest(ctx, messages, correlationID)
}

// sendChatRequest handles the actual API call to OpenAI
func (c *Client) sendChatRequest(ctx context.Context, messages []Message, correlationID string) (string, error) {

	request := ChatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   1000,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	c.logger.Info("Sending request to OpenAI", "correlation_id", correlationID, "model", c.model)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errorResp ErrorResponse
		if err := json.Unmarshal(body, &errorResp); err != nil {
			return "", fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(body))
		}
		return "", fmt.Errorf("OpenAI API error: %s", errorResp.Error.Message)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	response := chatResp.Choices[0].Message.Content
	c.logger.Info("Received response from OpenAI",
		"correlation_id", correlationID,
		"tokens_used", chatResp.Usage.TotalTokens,
		"response_length", len(response))

	return response, nil
}
