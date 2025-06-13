package slack

import "time"

type BroadcastRequest struct {
	UserID        string    `json:"user_id"`
	ChannelID     string    `json:"channel_id"`
	Question      string    `json:"question"`
	Response      string    `json:"response"`
	Timestamp     time.Time `json:"timestamp"`
	CorrelationID string    `json:"correlation_id"`
}

// FeedbackRequest represents a request to broadcast user feedback
type FeedbackRequest struct {
	UserID        string    `json:"user_id"`
	ChannelID     string    `json:"channel_id"`
	MessageTS     string    `json:"message_ts"`
	ThreadTS      string    `json:"thread_ts,omitempty"`
	Question      string    `json:"question,omitempty"`
	Response      string    `json:"response,omitempty"`
	FeedbackType  string    `json:"feedback_type"` // "positive", "negative", or "text"
	FeedbackText  string    `json:"feedback_text,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	CorrelationID string    `json:"correlation_id"`
}

type MessageBlock struct {
	Type string      `json:"type"`
	Text *TextObject `json:"text,omitempty"`
}

type TextObject struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type SlackMessage struct {
	Channel string         `json:"channel"`
	Blocks  []MessageBlock `json:"blocks"`
}
