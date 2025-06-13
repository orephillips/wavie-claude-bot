package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/BitwaveCorp/shared-svcs/services/gpt-agent-proxy-svc/internal/openai"
)

type ConversationMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp,omitempty"`
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

type Handler struct {
	openaiClient *openai.Client
	logger       *slog.Logger
}

func NewHandler(openaiClient *openai.Client, logger *slog.Logger) *Handler {
	return &Handler{
		openaiClient: openaiClient,
		logger:       logger,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.handleHealthCheck)
	mux.HandleFunc("POST /api/chat", h.handleChatCompletion)
}

func (h *Handler) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{"status": "ok"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var req GPTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Failed to decode request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		h.logger.Error("Empty message in request", "correlation_id", req.CorrelationID)
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}

	h.logger.Info("Processing chat completion request",
		"correlation_id", req.CorrelationID,
		"user_id", req.UserID,
		"channel_id", req.ChannelID,
		"thread_ts", req.ThreadTS,
		"has_history", len(req.ConversationHistory) > 0)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	// Use conversation history if available
	response, err := h.openaiClient.ChatCompletionWithHistory(ctx, req.Message, req.ConversationHistory, req.CorrelationID)
	if err != nil {
		h.logger.Error("Failed to get chat completion", "error", err, "correlation_id", req.CorrelationID)

		gptResp := GPTResponse{
			CorrelationID: req.CorrelationID,
			Error:         err.Error(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(gptResp)
		return
	}

	gptResp := GPTResponse{
		Response:      response,
		CorrelationID: req.CorrelationID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(gptResp)

	h.logger.Info("Successfully processed chat completion", "correlation_id", req.CorrelationID)
}
