package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orephillips/wavie-claude-bot/services/slack-events-listener-svc/internal/conversation"
	"github.com/orephillips/wavie-claude-bot/services/slack-events-listener-svc/internal/slack"
	"github.com/google/uuid"
	"github.com/BitwaveCorp/shared-svcs/shared/utils/idgen"
)

type Handler struct {
	slackClient         *slack.Client
	signingSecret       string
	gptProxyServiceURL  string
	broadcastServiceURL string
	logger              *slog.Logger
	processedEvents     map[string]bool
	eventsMutex         sync.RWMutex
	conversationStore   *conversation.Store
}

func NewHandler(slackClient *slack.Client, signingSecret, gptProxyServiceURL, broadcastServiceURL string, logger *slog.Logger) *Handler {
	// Create conversation store with 20 message limit and 1 hour max age
	conversationStore := conversation.NewStore(20, 1*time.Hour)

	return &Handler{
		slackClient:         slackClient,
		signingSecret:       signingSecret,
		gptProxyServiceURL:  gptProxyServiceURL,
		broadcastServiceURL: broadcastServiceURL,
		logger:              logger,
		processedEvents:     make(map[string]bool),
		conversationStore:   conversationStore,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.handleHealthCheck)
	mux.HandleFunc("POST /slack/events", h.ProcessEvent)
}

func (h *Handler) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{"status": "ok"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) verifySlackSignature(r *http.Request) error {
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")

	if timestamp == "" || signature == "" {
		return fmt.Errorf("missing timestamp or signature")
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse timestamp: %w", err)
	}

	if time.Now().Unix()-ts > 300 {
		return fmt.Errorf("timestamp is too old")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}

	baseString := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(h.signingSecret))
	mac.Write([]byte(baseString))
	expectedSignature := "v0=" + hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expectedSignature), []byte(signature)) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

func (h *Handler) isEventProcessed(eventID string) bool {
	h.eventsMutex.RLock()
	defer h.eventsMutex.RUnlock()
	return h.processedEvents[eventID]
}

func (h *Handler) markEventProcessed(eventID string) {
	h.eventsMutex.Lock()
	defer h.eventsMutex.Unlock()
	h.processedEvents[eventID] = true
}

func (h *Handler) ProcessEvent(w http.ResponseWriter, r *http.Request) {
	// Verify Slack signature
	if err := h.verifySlackSignature(r); err != nil {
		h.logger.Error("Failed to verify Slack signature", "error", err)
		http.Error(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("Failed to read request body", "error", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Parse event
	var eventReq slack.EventRequest
	if err := json.Unmarshal(body, &eventReq); err != nil {
		h.logger.Error("Failed to parse event request", "error", err)
		http.Error(w, "Failed to parse event request", http.StatusBadRequest)
		return
	}

	// Handle URL verification challenge
	if eventReq.Type == "url_verification" {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(eventReq.Challenge))
		return
	}

	// Deduplicate events
	if h.isEventProcessed(eventReq.EventID) {
		h.logger.Info("Duplicate event received, ignoring", "event_id", eventReq.EventID)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Process event asynchronously
	go func() {
		switch eventReq.Event.Type {
		case "app_mention":
			h.handleAppMention(eventReq)
		case "reaction_added":
			h.handleReactionAdded(eventReq)
		case "message":
			// Only process messages in threads that might contain feedback
			if eventReq.Event.ThreadTS != "" && strings.HasPrefix(eventReq.Event.Text, "***") {
				h.handleTextFeedback(eventReq)
			}
		}
		h.markEventProcessed(eventReq.EventID)
	}()

	// Respond immediately to Slack
	w.WriteHeader(http.StatusOK)
}

// handleReactionAdded processes reaction events for feedback
func (h *Handler) handleReactionAdded(eventReq slack.EventRequest) {
	// Only process thumbs up/down reactions
	if eventReq.Event.Reaction != "+1" && eventReq.Event.Reaction != "-1" {
		return
	}

	// Get the message that was reacted to
	messageTS := eventReq.Event.Item.TS
	channel := eventReq.Event.Item.Channel

	// Create a correlation ID for this feedback
	correlationID := "fb_" + uuid.New().String()

	// Determine feedback type
	feedbackType := "positive"
	if eventReq.Event.Reaction == "-1" {
		feedbackType = "negative"
	}

	// Create feedback request
	feedbackReq := slack.FeedbackRequest{
		UserID:        eventReq.Event.User,
		ChannelID:     channel,
		MessageTS:     messageTS,
		FeedbackType:  feedbackType,
		Timestamp:     time.Now(),
		CorrelationID: correlationID,
	}

	// Send feedback to broadcast service
	h.sendFeedbackToBroadcast(feedbackReq)

	h.logger.Info("Processed reaction feedback",
		"feedback_type", feedbackType,
		"user", eventReq.Event.User,
		"channel", channel,
		"correlation_id", correlationID)
}

// handleTextFeedback processes text feedback from thread replies
func (h *Handler) handleTextFeedback(eventReq slack.EventRequest) {
	// Extract feedback text (remove the *** prefix)
	feedbackText := strings.TrimPrefix(eventReq.Event.Text, "***")
	feedbackText = strings.TrimSpace(feedbackText)

	// If no actual feedback text, ignore
	if feedbackText == "" {
		return
	}

	// Create a correlation ID for this feedback
	correlationID := "fb_" + uuid.New().String()

	// Create feedback request
	feedbackReq := slack.FeedbackRequest{
		UserID:        eventReq.Event.User,
		ChannelID:     eventReq.Event.Channel,
		MessageTS:     eventReq.Event.TS,
		ThreadTS:      eventReq.Event.ThreadTS,
		FeedbackType:  "text",
		FeedbackText:  feedbackText,
		Timestamp:     time.Now(),
		CorrelationID: correlationID,
	}

	// Send feedback to broadcast service
	h.sendFeedbackToBroadcast(feedbackReq)

	h.logger.Info("Processed text feedback",
		"user", eventReq.Event.User,
		"channel", eventReq.Event.Channel,
		"correlation_id", correlationID)
}

// sendFeedbackToBroadcast sends feedback to the broadcast service
func (h *Handler) sendFeedbackToBroadcast(feedback slack.FeedbackRequest) {
	// Marshal feedback to JSON
	feedbackJSON, err := json.Marshal(feedback)
	if err != nil {
		h.logger.Error("Failed to marshal feedback", "error", err)
		return
	}

	// Send to broadcast service
	resp, err := http.Post(h.broadcastServiceURL+"/api/feedback", "application/json", bytes.NewReader(feedbackJSON))
	if err != nil {
		h.logger.Error("Failed to send feedback to broadcast service", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.logger.Error("Broadcast service returned non-OK status", "status", resp.Status)
		return
	}

	h.logger.Info("Successfully sent feedback to broadcast service", "correlation_id", feedback.CorrelationID)
}

func (h *Handler) handleAppMention(eventReq slack.EventRequest) {
	correlationID, err := idgen.GenerateId("wv", 16)
	if err != nil {
		h.logger.Error("Failed to generate correlation ID", "error", err)
		return
	}

	// Determine if this is a thread reply or a new message
	isThreadReply := eventReq.Event.ThreadTS != ""
	threadID := eventReq.Event.ThreadTS
	if threadID == "" {
		threadID = eventReq.Event.TS // Use message timestamp as thread ID for new messages
	}

	h.logger.Info("Processing wavie message", 
		"correlation_id", correlationID, 
		"user", eventReq.Event.User, 
		"channel", eventReq.Event.Channel,
		"is_thread", isThreadReply,
		"thread_id", threadID)

	// Clean the message text
	message := strings.ReplaceAll(eventReq.Event.Text, "<@", "")
	message = strings.ReplaceAll(message, ">", "")
	message = strings.ReplaceAll(message, "@wavie", "")
	message = strings.TrimSpace(message)

	// Add user message to conversation context
	h.conversationStore.AddMessage(threadID, "user", message)

	// Get conversation history for this thread
	conversationHistory := h.conversationStore.GetMessages(threadID)

	gptReq := slack.GPTRequest{
		Message:            message,
		UserID:             eventReq.Event.User,
		ChannelID:          eventReq.Event.Channel,
		MessageTS:          eventReq.Event.TS,
		ThreadTS:           threadID,
		ConversationHistory: conversationHistory,
		CorrelationID:      correlationID,
	}

	gptResp, err := h.callGPTService(gptReq)
	if err != nil {
		h.logger.Error("Failed to call GPT service", "error", err, "correlation_id", correlationID)
		h.slackClient.PostMessage(context.Background(), eventReq.Event.Channel, "Sorry, I'm having trouble processing your request right now.", threadID)
		return
	}

	if gptResp.Error != "" {
		h.logger.Error("GPT service returned error", "error", gptResp.Error, "correlation_id", correlationID)
		h.slackClient.PostMessage(context.Background(), eventReq.Event.Channel, "Sorry, I encountered an error processing your request.", threadID)
		return
	}

	// Add bot response to conversation context
	h.conversationStore.AddMessage(threadID, "assistant", gptResp.Response)

	// For new conversations (not in a thread), append a hint to continue conversation in thread for new messages
	if eventReq.Event.ThreadTS == "" {
		gptResp.Response += "\n\n_Reply in this thread to continue our conversation. React with 👍 or 👎 to provide feedback, or start your message with *** to leave detailed feedback._"
	}

	// Always reply in the thread if there is one
	err = h.slackClient.PostMessage(context.Background(), eventReq.Event.Channel, gptResp.Response, threadID)
	if err != nil {
		h.logger.Error("Failed to post response to Slack", "error", err, "correlation_id", correlationID)
		return
	}

	broadcastReq := slack.BroadcastRequest{
		UserID:        eventReq.Event.User,
		ChannelID:     eventReq.Event.Channel,
		ThreadID:      threadID,
		Question:      message,
		Response:      gptResp.Response,
		Timestamp:     time.Now(),
		CorrelationID: correlationID,
	}

	go h.callBroadcastService(broadcastReq)
}

func (h *Handler) callGPTService(req slack.GPTRequest) (*slack.GPTResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GPT request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", h.gptProxyServiceURL+"/api/chat", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create GPT request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call GPT service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GPT service error: %d - %s", resp.StatusCode, string(body))
	}

	var gptResp slack.GPTResponse
	if err := json.NewDecoder(resp.Body).Decode(&gptResp); err != nil {
		return nil, fmt.Errorf("failed to decode GPT response: %w", err)
	}

	return &gptResp, nil
}

func (h *Handler) callBroadcastService(req slack.BroadcastRequest) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		h.logger.Error("Failed to marshal broadcast request", "error", err, "correlation_id", req.CorrelationID)
		return
	}

	httpReq, err := http.NewRequest("POST", h.broadcastServiceURL+"/api/broadcast", bytes.NewBuffer(jsonData))
	if err != nil {
		h.logger.Error("Failed to create broadcast request", "error", err, "correlation_id", req.CorrelationID)
		return
	}

	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		h.logger.Error("Failed to call broadcast service", "error", err, "correlation_id", req.CorrelationID)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		h.logger.Error("Broadcast service error", "status", resp.StatusCode, "body", string(body), "correlation_id", req.CorrelationID)
		return
	}

	h.logger.Info("Successfully sent to broadcast service", "correlation_id", req.CorrelationID)
}
