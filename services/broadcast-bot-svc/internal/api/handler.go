package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/BitwaveCorp/shared-svcs/services/broadcast-bot-svc/internal/slack"
)

type Handler struct {
	slackClient        *slack.Client
	broadcastChannelID string
	logger             *slog.Logger
	processedMessages  map[string]bool
	messagesMutex      sync.RWMutex
}

func NewHandler(slackClient *slack.Client, broadcastChannelID string, logger *slog.Logger) *Handler {
	return &Handler{
		slackClient:        slackClient,
		broadcastChannelID: broadcastChannelID,
		logger:             logger,
		processedMessages:  make(map[string]bool),
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.handleHealthCheck)
	mux.HandleFunc("POST /api/broadcast", h.handleBroadcast)
	mux.HandleFunc("POST /api/feedback", h.handleFeedback)
}

func (h *Handler) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{"status": "ok"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) isMessageProcessed(correlationID string) bool {
	h.messagesMutex.RLock()
	defer h.messagesMutex.RUnlock()
	return h.processedMessages[correlationID]
}

func (h *Handler) markMessageProcessed(correlationID string) {
	h.messagesMutex.Lock()
	defer h.messagesMutex.Unlock()
	h.processedMessages[correlationID] = true
}

func (h *Handler) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var req slack.FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Failed to decode feedback request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.CorrelationID == "" {
		h.logger.Error("Missing correlation ID in feedback request")
		http.Error(w, "Correlation ID is required", http.StatusBadRequest)
		return
	}

	if h.isMessageProcessed(req.CorrelationID) {
		h.logger.Info("Feedback message already processed", "correlation_id", req.CorrelationID)
		w.WriteHeader(http.StatusOK)
		return
	}

	h.markMessageProcessed(req.CorrelationID)

	h.logger.Info("Processing feedback request",
		"correlation_id", req.CorrelationID,
		"user_id", req.UserID,
		"feedback_type", req.FeedbackType)

	err := h.slackClient.PostFeedbackMessage(r.Context(), h.broadcastChannelID, req)
	if err != nil {
		h.logger.Error("Failed to post feedback message", "error", err, "correlation_id", req.CorrelationID)
		http.Error(w, "Failed to post feedback message", http.StatusInternalServerError)
		return
	}

	response := map[string]string{
		"status":         "success",
		"correlation_id": req.CorrelationID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	h.logger.Info("Successfully processed feedback request", "correlation_id", req.CorrelationID)
}

func (h *Handler) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	var req slack.BroadcastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Failed to decode broadcast request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.CorrelationID == "" {
		h.logger.Error("Missing correlation ID in broadcast request")
		http.Error(w, "Correlation ID is required", http.StatusBadRequest)
		return
	}

	if h.isMessageProcessed(req.CorrelationID) {
		h.logger.Info("Broadcast message already processed", "correlation_id", req.CorrelationID)
		w.WriteHeader(http.StatusOK)
		return
	}

	h.markMessageProcessed(req.CorrelationID)

	h.logger.Info("Processing broadcast request",
		"correlation_id", req.CorrelationID,
		"user_id", req.UserID,
		"channel_id", req.ChannelID)

	err := h.slackClient.PostBroadcastMessage(r.Context(), h.broadcastChannelID, req)
	if err != nil {
		h.logger.Error("Failed to post broadcast message", "error", err, "correlation_id", req.CorrelationID)
		http.Error(w, "Failed to post broadcast message", http.StatusInternalServerError)
		return
	}

	response := map[string]string{
		"status":         "success",
		"correlation_id": req.CorrelationID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	h.logger.Info("Successfully processed broadcast request", "correlation_id", req.CorrelationID)
}
