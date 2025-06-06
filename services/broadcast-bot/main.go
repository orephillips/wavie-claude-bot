package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Port               string `envconfig:"PORT" default:"8080"`
	SlackBotToken      string `envconfig:"BROADCASTER_SLACK_BOT_TOKEN" required:"true"`
	BroadcastChannelID string `envconfig:"BROADCAST_CHANNEL_ID" required:"true"`
}

type BroadcastRequest struct {
	User          string `json:"user"`
	Channel       string `json:"channel"`
	Question      string `json:"question"`
	Response      string `json:"response"`
	Timestamp     string `json:"timestamp"`
	CorrelationID string `json:"correlation_id"`
}

type SlackBlock struct {
	Type   string                 `json:"type"`
	Text   map[string]interface{} `json:"text,omitempty"`
	Fields []map[string]interface{} `json:"fields,omitempty"`
}

type SlackMessage struct {
	Channel string       `json:"channel"`
	Blocks  []SlackBlock `json:"blocks"`
}

type BroadcastService struct {
	config            *Config
	httpClient        *http.Client
	processedMessages map[string]bool
	mu                sync.RWMutex
}

func NewBroadcastService(config *Config) *BroadcastService {
	return &BroadcastService{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		processedMessages: make(map[string]bool),
	}
}

func (s *BroadcastService) isMessageProcessed(correlationID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processedMessages[correlationID]
}

func (s *BroadcastService) markMessageProcessed(correlationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processedMessages[correlationID] = true
	
	if len(s.processedMessages) > 1000 {
		newMap := make(map[string]bool)
		count := 0
		for k, v := range s.processedMessages {
			if count < 500 {
				newMap[k] = v
				count++
			}
		}
		s.processedMessages = newMap
	}
}

func (s *BroadcastService) truncateText(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength-3] + "..."
}

func (s *BroadcastService) buildSlackMessage(req *BroadcastRequest) SlackMessage {
	timestamp, _ := time.Parse(time.RFC3339, req.Timestamp)
	timeStr := timestamp.Format("Mon Jan 2, 2006 at 3:04 PM MST")

	question := s.truncateText(req.Question, 300)
	response := s.truncateText(req.Response, 800)

	return SlackMessage{
		Channel: s.config.BroadcastChannelID,
		Blocks: []SlackBlock{
			{
				Type: "section",
				Text: map[string]interface{}{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*🤖 New Wavie Interaction*\n_%s_", timeStr),
				},
			},
			{
				Type: "section",
				Fields: []map[string]interface{}{
					{
						"type": "mrkdwn",
						"text": fmt.Sprintf("*User:*\n<@%s>", req.User),
					},
					{
						"type": "mrkdwn",
						"text": fmt.Sprintf("*Channel:*\n<#%s>", req.Channel),
					},
				},
			},
			{
				Type: "section",
				Text: map[string]interface{}{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*Question:*\n```%s```", question),
				},
			},
			{
				Type: "section",
				Text: map[string]interface{}{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*Response:*\n%s", response),
				},
			},
			{
				Type: "section",
				Text: map[string]interface{}{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*Correlation ID:* `%s`", req.CorrelationID),
				},
			},
			{
				Type: "divider",
			},
		},
	}
}

func (s *BroadcastService) sendSlackMessage(message SlackMessage) error {
	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.config.SlackBotToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	var slackResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&slackResp); err != nil {
		return fmt.Errorf("failed to decode response: %v", err)
	}

	if ok, exists := slackResp["ok"].(bool); !exists || !ok {
		errorMsg := "unknown error"
		if errStr, exists := slackResp["error"].(string); exists {
			errorMsg = errStr
		}
		return fmt.Errorf("slack API error: %s", errorMsg)
	}

	return nil
}

func (s *BroadcastService) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req BroadcastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.CorrelationID == "" || req.User == "" || req.Channel == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	if s.isMessageProcessed(req.CorrelationID) {
		log.Printf("Duplicate broadcast request ignored: %s", req.CorrelationID)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "duplicate_ignored"})
		return
	}

	s.markMessageProcessed(req.CorrelationID)

	log.Printf("Broadcasting interaction (ID: %s): User %s in Channel %s", 
		req.CorrelationID, req.User, req.Channel)

	message := s.buildSlackMessage(&req)
	if err := s.sendSlackMessage(message); err != nil {
		log.Printf("Failed to send broadcast message (ID: %s): %v", req.CorrelationID, err)
		http.Error(w, "Failed to send broadcast", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully broadcasted interaction (ID: %s)", req.CorrelationID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":         "success",
		"correlation_id": req.CorrelationID,
		"timestamp":      time.Now().Format(time.RFC3339),
	})
}

func (s *BroadcastService) healthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "healthy",
		"service":   "broadcast-bot",
		"channel":   s.config.BroadcastChannelID,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func main() {
	var config Config
	if err := envconfig.Process("", &config); err != nil {
		log.Fatalf("Failed to process environment variables: %v", err)
	}

	if !strings.HasPrefix(config.SlackBotToken, "xoxb-") {
		log.Fatalf("Invalid Slack bot token format. Expected to start with 'xoxb-'")
	}

	if !strings.HasPrefix(config.BroadcastChannelID, "C") && !strings.HasPrefix(config.BroadcastChannelID, "G") {
		log.Fatalf("Invalid channel ID format. Expected to start with 'C' or 'G'")
	}

	service := NewBroadcastService(&config)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", service.healthCheck)
	mux.HandleFunc("/api/broadcast", service.handleBroadcast)

	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("Broadcast Bot Service starting on port %s (Channel: %s)", config.Port, config.BroadcastChannelID)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}