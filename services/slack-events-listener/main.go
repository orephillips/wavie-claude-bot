package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	Port                string `envconfig:"PORT" default:"8080"`
	SlackBotToken       string `envconfig:"WAVIE_SLACK_BOT_TOKEN" required:"true"`
	SlackSigningSecret  string `envconfig:"WAVIE_SLACK_SIGNING_SECRET" required:"true"`
	ClaudeProxyURL      string `envconfig:"CLAUDE_PROXY_URL" required:"true"`
	BroadcastServiceURL string `envconfig:"BROADCAST_SERVICE_URL" required:"true"`
}

type SlackEvent struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge,omitempty"`
	Event     struct {
		Type    string `json:"type"`
		User    string `json:"user"`
		Text    string `json:"text"`
		Channel string `json:"channel"`
		Ts      string `json:"ts"`
	} `json:"event"`
}

type ClaudeRequest struct {
	Message       string `json:"message"`
	User          string `json:"user"`
	Channel       string `json:"channel"`
	CorrelationID string `json:"correlation_id"`
}

type ClaudeResponse struct {
	Response      string `json:"response"`
	CorrelationID string `json:"correlation_id"`
	Error         string `json:"error,omitempty"`
}

type BroadcastRequest struct {
	User          string `json:"user"`
	Channel       string `json:"channel"`
	Question      string `json:"question"`
	Response      string `json:"response"`
	Timestamp     string `json:"timestamp"`
	CorrelationID string `json:"correlation_id"`
}

type SlackEventsService struct {
	config          *Config
	httpClient      *http.Client
	processedEvents map[string]bool
	mu              sync.RWMutex
}

func NewSlackEventsService(config *Config) *SlackEventsService {
	return &SlackEventsService{
		config: config,
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
		processedEvents: make(map[string]bool),
	}
}

func (s *SlackEventsService) verifySlackRequest(r *http.Request, body []byte) bool {
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")

	if timestamp == "" || signature == "" {
		return false
	}

	baseString := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	h := hmac.New(sha256.New, []byte(s.config.SlackSigningSecret))
	h.Write([]byte(baseString))
	expectedSignature := "v0=" + hex.EncodeToString(h.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

func (s *SlackEventsService) isEventProcessed(eventID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processedEvents[eventID]
}

func (s *SlackEventsService) markEventProcessed(eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processedEvents[eventID] = true
	
	if len(s.processedEvents) > 1000 {
		newMap := make(map[string]bool)
		count := 0
		for k, v := range s.processedEvents {
			if count < 500 {
				newMap[k] = v
				count++
			}
		}
		s.processedEvents = newMap
	}
}

func (s *SlackEventsService) generateCorrelationID() string {
	return fmt.Sprintf("wavie_%d", time.Now().UnixNano())
}

func (s *SlackEventsService) sendToClaudeProxy(message, user, channel, correlationID string) (*ClaudeResponse, error) {
	request := ClaudeRequest{
		Message:       message,
		User:          user,
		Channel:       channel,
		CorrelationID: correlationID,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Post(s.config.ClaudeProxyURL+"/api/chat", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var claudeResp ClaudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return nil, err
	}

	return &claudeResp, nil
}

func (s *SlackEventsService) sendToBroadcastBot(user, channel, question, response, correlationID string) {
	broadcastReq := BroadcastRequest{
		User:          user,
		Channel:       channel,
		Question:      question,
		Response:      response,
		Timestamp:     time.Now().Format(time.RFC3339),
		CorrelationID: correlationID,
	}

	jsonData, _ := json.Marshal(broadcastReq)
	
	go func() {
		_, err := s.httpClient.Post(s.config.BroadcastServiceURL+"/api/broadcast", "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("Failed to send to broadcast bot: %v", err)
		}
	}()
}

func (s *SlackEventsService) sendSlackMessage(channel, message string) error {
	payload := map[string]interface{}{
		"channel": channel,
		"text":    message,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+s.config.SlackBotToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var slackResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&slackResp); err != nil {
		return err
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

func (s *SlackEventsService) handleSlackEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	if !s.verifySlackRequest(r, body) {
		http.Error(w, "Invalid request signature", http.StatusUnauthorized)
		return
	}

	var event SlackEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if event.Type == "url_verification" {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(event.Challenge))
		return
	}

	if event.Type == "event_callback" && event.Event.Type == "app_mention" {
		eventID := fmt.Sprintf("%s_%s", event.Event.Channel, event.Event.Ts)
		
		if s.isEventProcessed(eventID) {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		s.markEventProcessed(eventID)

		message := strings.TrimSpace(strings.ReplaceAll(event.Event.Text, "<@U08VAS7SKJ8>", ""))
		if message == "" {
			message = "Hello! How can I help you?"
		}

		correlationID := s.generateCorrelationID()
		
		log.Printf("Processing message from user %s in channel %s: %s (ID: %s)", 
			event.Event.User, event.Event.Channel, message, correlationID)

		claudeResp, err := s.sendToClaudeProxy(message, event.Event.User, event.Event.Channel, correlationID)
		if err != nil {
			log.Printf("Error calling Claude proxy: %v", err)
			s.sendSlackMessage(event.Event.Channel, "Sorry, I'm having trouble processing your request right now. Please try again later.")
			w.WriteHeader(http.StatusOK)
			return
		}

		if claudeResp.Error != "" {
			log.Printf("Claude proxy returned error: %s", claudeResp.Error)
			s.sendSlackMessage(event.Event.Channel, "Sorry, I encountered an error while processing your request.")
			w.WriteHeader(http.StatusOK)
			return
		}

		if err := s.sendSlackMessage(event.Event.Channel, claudeResp.Response); err != nil {
			log.Printf("Error sending message to Slack: %v", err)
		}

		s.sendToBroadcastBot(event.Event.User, event.Event.Channel, message, claudeResp.Response, correlationID)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *SlackEventsService) healthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "healthy",
		"service":   "slack-events-listener",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func main() {
	var config Config
	if err := envconfig.Process("", &config); err != nil {
		log.Fatalf("Failed to process environment variables: %v", err)
	}

	service := NewSlackEventsService(&config)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", service.healthCheck)
	mux.HandleFunc("/slack/events", service.handleSlackEvents)

	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
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

	log.Printf("Slack Events Listener Service starting on port %s", config.Port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}