package slack

import "time"

type EventRequest struct {
	Token     string `json:"token"`
	Challenge string `json:"challenge"`
	Type      string `json:"type"`
	Event     Event  `json:"event"`
	EventID   string `json:"event_id"`
	EventTime int64  `json:"event_time"`
}

type Event struct {
	Type     string `json:"type"`
	User     string `json:"user"`
	Text     string `json:"text"`
	Channel  string `json:"channel"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts,omitempty"`
	EventTS  string `json:"event_ts"`
	BotID    string `json:"bot_id,omitempty"`
}

type MessageResponse struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

// Message represents a single message in a conversation for the GPT API
type ConversationMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type GPTRequest struct {
	Message            string               `json:"message"`
	UserID             string               `json:"user_id"`
	ChannelID          string               `json:"channel_id"`
	MessageTS          string               `json:"message_ts"`
	ThreadTS           string               `json:"thread_ts,omitempty"`
	ConversationHistory []ConversationMessage `json:"conversation_history,omitempty"`
	CorrelationID      string               `json:"correlation_id"`
}

type GPTResponse struct {
	Response      string `json:"response"`
	CorrelationID string `json:"correlation_id"`
	Error         string `json:"error,omitempty"`
}

type BroadcastRequest struct {
	UserID        string    `json:"user_id"`
	ChannelID     string    `json:"channel_id"`
	ThreadID      string    `json:"thread_id,omitempty"`
	Question      string    `json:"question"`
	Response      string    `json:"response"`
	Timestamp     time.Time `json:"timestamp"`
	CorrelationID string    `json:"correlation_id"`
}
