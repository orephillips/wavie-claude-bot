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

	"github.com/BitwaveCorp/shared-svcs/services/slack-events-listener-svc/internal/conversation"
	"github.com/BitwaveCorp/shared-svcs/services/slack-events-listener-svc/internal/slack"
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
	mux.HandleFunc("POST /slack/events", h.handleSlackEvents)
}

func (h *Handler) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{"status": "ok"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) verifySlackSignature(body []byte, timestamp, signature string) bool {
	if timestamp == "" || signature == "" {
		return false
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}

	if time.Now().Unix()-ts > 300 {
		return false
	}

	baseString := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(h.signingSecret))
	mac.Write([]byte(baseString))
	expectedSignature := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedSignature), []byte(signature))
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

func (h *Handler) handleSlackEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("Failed to read request body", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")

	if !h.verifySlackSignature(body, timestamp, signature) {
		h.logger.Error("Invalid Slack signature")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var eventReq slack.EventRequest
	if err := json.Unmarshal(body, &eventReq); err != nil {
		h.logger.Error("Failed to parse event request", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if eventReq.Type == "url_verification" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(eventReq.Challenge))
		return
	}

	if eventReq.Type == "event_callback" {
		// Process both app_mention and message events (for thread replies)
		if eventReq.Event.Type == "app_mention" || 
		   (eventReq.Event.Type == "message" && eventReq.Event.ThreadTS != "" && !eventReq.Event.BotID) {
			
			if h.isEventProcessed(eventReq.EventID) {
				h.logger.Info("Event already processed", "event_id", eventReq.EventID)
				w.WriteHeader(http.StatusOK)
				return
			}

			h.markEventProcessed(eventReq.EventID)

			// Check if this is a direct mention or a thread reply to the bot
			isMention := strings.Contains(eventReq.Event.Text, "@wavie")
			isThreadReply := eventReq.Event.ThreadTS != ""

			if isMention || isThreadReply {
				go h.processWavieMessage(eventReq)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) processWavieMessage(eventReq slack.EventRequest) {
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

	// For new conversations (not in a thread), add a hint to continue in thread
	responseText := gptResp.Response
	if !isThreadReply {
		responseText += "\n\n_To continue the conversation, reply in this thread._"
	}

	// Always reply in the thread if there is one
	err = h.slackClient.PostMessage(context.Background(), eventReq.Event.Channel, responseText, threadID)
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
