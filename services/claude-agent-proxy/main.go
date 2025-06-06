package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Port              string `envconfig:"PORT" default:"8080"`
	AnthropicAPIKey   string `envconfig:"ANTHROPIC_API_KEY" required:"true"`
	ClaudeModel       string `envconfig:"CLAUDE_MODEL" default:"claude-3-sonnet-20240229"`
	DocsZipPath       string `envconfig:"DOCS_ZIP_PATH" default:"./docs.zip"`
	MaxContextChunks  int    `envconfig:"MAX_CONTEXT_CHUNKS" default:"5"`
	ChunkSize         int    `envconfig:"CHUNK_SIZE" default:"1000"`
}

type Document struct {
	Path     string
	Title    string
	Content  string
	Metadata map[string]string
}

type Chunk struct {
	ID       string
	DocPath  string
	Title    string
	Content  string
	Keywords []string
	Score    float64
}

type DocumentService struct {
	documents []Document
	chunks    []Chunk
	keywords  map[string][]int
}

type ChatRequest struct {
	Message       string `json:"message"`
	User          string `json:"user"`
	Channel       string `json:"channel"`
	CorrelationID string `json:"correlation_id"`
}

type ChatResponse struct {
	Response      string   `json:"response"`
	CorrelationID string   `json:"correlation_id"`
	Error         string   `json:"error,omitempty"`
	SourceDocs    []string `json:"source_docs,omitempty"`
}

type ClaudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ClaudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []ClaudeMessage `json:"messages"`
	System    string          `json:"system,omitempty"`
}

type ClaudeResponse struct {
	Content []struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewDocumentService() *DocumentService {
	return &DocumentService{
		documents: make([]Document, 0),
		chunks:    make([]Chunk, 0),
		keywords:  make(map[string][]int),
	}
}

func (ds *DocumentService) LoadFromZip(zipPath string, chunkSize int) error {
	log.Printf("Loading documents from ZIP: %s", zipPath)
	
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open ZIP file: %v", err)
	}
	defer reader.Close()

	ds.documents = ds.documents[:0]
	ds.chunks = ds.chunks[:0]
	ds.keywords = make(map[string][]int)

	for _, file := range reader.File {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".md") {
			continue
		}

		content, err := ds.readZipFile(file)
		if err != nil {
			log.Printf("Warning: Failed to read %s: %v", file.Name, err)
			continue
		}

		doc := Document{
			Path:     file.Name,
			Title:    ds.extractTitle(content),
			Content:  content,
			Metadata: map[string]string{"size": fmt.Sprintf("%d", len(content))},
		}

		ds.documents = append(ds.documents, doc)
		ds.chunkDocument(doc, chunkSize)
	}

	ds.buildKeywordIndex()

	log.Printf("Loaded %d documents, created %d chunks", len(ds.documents), len(ds.chunks))
	return nil
}

func (ds *DocumentService) readZipFile(file *zip.File) (string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (ds *DocumentService) extractTitle(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return "Untitled"
}

func (ds *DocumentService) chunkDocument(doc Document, chunkSize int) {
	content := ds.cleanContent(doc.Content)
	sections := ds.splitBySections(content)
	
	for i, section := range sections {
		if len(section) <= chunkSize {
			chunk := Chunk{
				ID:       fmt.Sprintf("%s_chunk_%d", doc.Path, i),
				DocPath:  doc.Path,
				Title:    doc.Title,
				Content:  section,
				Keywords: ds.extractKeywords(section),
			}
			ds.chunks = append(ds.chunks, chunk)
		} else {
			subChunks := ds.splitIntoChunks(section, chunkSize)
			for j, subChunk := range subChunks {
				chunk := Chunk{
					ID:       fmt.Sprintf("%s_chunk_%d_%d", doc.Path, i, j),
					DocPath:  doc.Path,
					Title:    doc.Title,
					Content:  subChunk,
					Keywords: ds.extractKeywords(subChunk),
				}
				ds.chunks = append(ds.chunks, chunk)
			}
		}
	}
}

func (ds *DocumentService) cleanContent(content string) string {
	content = regexp.MustCompile(`\n\s*\n\s*\n`).ReplaceAllString(content, "\n\n")
	content = strings.TrimSpace(content)
	return content
}

func (ds *DocumentService) splitBySections(content string) []string {
	lines := strings.Split(content, "\n")
	sections := make([]string, 0)
	currentSection := strings.Builder{}
	
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") && currentSection.Len() > 0 {
			sections = append(sections, currentSection.String())
			currentSection.Reset()
		}
		currentSection.WriteString(line + "\n")
	}
	
	if currentSection.Len() > 0 {
		sections = append(sections, currentSection.String())
	}
	
	return sections
}

func (ds *DocumentService) splitIntoChunks(text string, chunkSize int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}
	
	chunks := make([]string, 0)
	words := strings.Fields(text)
	currentChunk := strings.Builder{}
	
	for _, word := range words {
		if currentChunk.Len()+len(word)+1 > chunkSize && currentChunk.Len() > 0 {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
		}
		if currentChunk.Len() > 0 {
			currentChunk.WriteString(" ")
		}
		currentChunk.WriteString(word)
	}
	
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}
	
	return chunks
}

func (ds *DocumentService) extractKeywords(text string) []string {
	text = strings.ToLower(text)
	words := regexp.MustCompile(`\b[a-z]{3,}\b`).FindAllString(text, -1)
	
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "are": true, "but": true,
		"not": true, "you": true, "all": true, "can": true, "had": true,
		"her": true, "was": true, "one": true, "our": true, "out": true,
		"day": true, "get": true, "has": true, "him": true, "his": true,
		"how": true, "its": true, "may": true, "new": true, "now": true,
		"old": true, "see": true, "two": true, "way": true, "who": true,
		"this": true, "that": true, "with": true, "have": true, "from": true,
		"they": true, "know": true, "want": true, "been": true, "good": true,
		"much": true, "some": true, "time": true, "very": true, "when": true,
		"come": true, "here": true, "just": true, "like": true, "long": true,
		"make": true, "many": true, "over": true, "such": true, "take": true,
		"than": true, "them": true, "well": true, "were": true,
	}
	
	keywords := make([]string, 0)
	seen := make(map[string]bool)
	
	for _, word := range words {
		if !stopWords[word] && !seen[word] && len(word) > 3 {
			keywords = append(keywords, word)
			seen[word] = true
		}
	}
	
	return keywords
}

func (ds *DocumentService) buildKeywordIndex() {
	ds.keywords = make(map[string][]int)
	
	for i, chunk := range ds.chunks {
		for _, keyword := range chunk.Keywords {
			if _, exists := ds.keywords[keyword]; !exists {
				ds.keywords[keyword] = make([]int, 0)
			}
			ds.keywords[keyword] = append(ds.keywords[keyword], i)
		}
	}
}

func (ds *DocumentService) SearchRelevantChunks(query string, maxChunks int) []Chunk {
	if len(ds.chunks) == 0 {
		return nil
	}
	
	queryWords := ds.extractKeywords(strings.ToLower(query))
	if len(queryWords) == 0 {
		return nil
	}
	
	chunkScores := make(map[int]float64)
	
	for _, queryWord := range queryWords {
		if chunkIndices, exists := ds.keywords[queryWord]; exists {
			weight := math.Log(float64(len(ds.chunks))/float64(len(chunkIndices))) + 1
			for _, chunkIndex := range chunkIndices {
				chunkScores[chunkIndex] += weight
			}
		}
	}
	
	type scoredChunk struct {
		chunk Chunk
		score float64
	}
	
	scoredChunks := make([]scoredChunk, 0)
	for chunkIndex, score := range chunkScores {
		if chunkIndex < len(ds.chunks) {
			chunk := ds.chunks[chunkIndex]
			chunk.Score = score
			scoredChunks = append(scoredChunks, scoredChunk{chunk, score})
		}
	}
	
	sort.Slice(scoredChunks, func(i, j int) bool {
		return scoredChunks[i].score > scoredChunks[j].score
	})
	
	result := make([]Chunk, 0)
	for i, scored := range scoredChunks {
		if i >= maxChunks {
			break
		}
		result = append(result, scored.chunk)
	}
	
	return result
}

type ClaudeProxyService struct {
	config     *Config
	httpClient *http.Client
	docService *DocumentService
}

func NewClaudeProxyService(config *Config) *ClaudeProxyService {
	return &ClaudeProxyService{
		config:     config,
		httpClient: &http.Client{Timeout: 90 * time.Second},
		docService: NewDocumentService(),
	}
}

func (s *ClaudeProxyService) LoadDocuments() error {
	if s.config.DocsZipPath == "" {
		log.Println("No docs ZIP path configured, running without knowledge base")
		return nil
	}
	
	if _, err := os.Stat(s.config.DocsZipPath); os.IsNotExist(err) {
		log.Printf("Docs ZIP file not found at %s, running without knowledge base", s.config.DocsZipPath)
		return nil
	}
	
	return s.docService.LoadFromZip(s.config.DocsZipPath, s.config.ChunkSize)
}

func (s *ClaudeProxyService) buildSystemPrompt(relevantChunks []Chunk) string {
	basePrompt := `You are Wavie, a helpful AI assistant integrated into Slack for Bitwave. You help users with questions about Bitwave products, documentation, and general assistance.

Key guidelines:
- Be helpful, friendly, and professional
- Provide clear, concise answers
- If you're unsure about something, say so
- For complex questions, break down your response into digestible parts
- Always prioritize information from the Bitwave documentation when available
- If asked about Bitwave-specific features, refer to the provided documentation
- Remember this is a Slack environment, so keep responses conversational but informative`

	if len(relevantChunks) == 0 {
		return basePrompt
	}

	contextPrompt := basePrompt + "\n\nRELEVANT BITWAVE DOCUMENTATION:\n"
	for i, chunk := range relevantChunks {
		contextPrompt += fmt.Sprintf("\n--- Document %d: %s ---\n%s\n", i+1, chunk.Title, chunk.Content)
	}
	
	contextPrompt += "\nUse the above documentation to inform your responses when relevant. If the documentation doesn't contain the answer, say so clearly."
	
	return contextPrompt
}

func (s *ClaudeProxyService) callClaudeAPI(message string, relevantChunks []Chunk) (string, error) {
	systemPrompt := s.buildSystemPrompt(relevantChunks)
	
	claudeReq := ClaudeRequest{
		Model:     s.config.ClaudeModel,
		MaxTokens: 4000,
		System:    systemPrompt,
		Messages: []ClaudeMessage{
			{
				Role:    "user",
				Content: message,
			},
		},
	}

	jsonData, err := json.Marshal(claudeReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.config.AnthropicAPIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Claude API: %v", err)
	}
	defer resp.Body.Close()

	var claudeResp ClaudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	if claudeResp.Error.Type != "" {
		return "", fmt.Errorf("claude API error: %s - %s", claudeResp.Error.Type, claudeResp.Error.Message)
	}

	if len(claudeResp.Content) == 0 {
		return "", fmt.Errorf("no content in Claude response")
	}

	var response string
	for _, content := range claudeResp.Content {
		if content.Type == "text" {
			response += content.Text
		}
	}

	if response == "" {
		return "", fmt.Errorf("no text content found in response")
	}

	log.Printf("Claude API usage - Input tokens: %d, Output tokens: %d", 
		claudeResp.Usage.InputTokens, claudeResp.Usage.OutputTokens)

	return response, nil
}

func (s *ClaudeProxyService) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}

	log.Printf("Processing chat request (ID: %s): %s", req.CorrelationID, req.Message)

	relevantChunks := s.docService.SearchRelevantChunks(req.Message, s.config.MaxContextChunks)
	
	sourceDocs := make([]string, 0)
	if len(relevantChunks) > 0 {
		log.Printf("Found %d relevant documentation chunks", len(relevantChunks))
		for _, chunk := range relevantChunks {
			sourceDocs = append(sourceDocs, chunk.Title)
		}
	}

	response, err := s.callClaudeAPI(req.Message, relevantChunks)
	if err != nil {
		log.Printf("Error calling Claude API (ID: %s): %v", req.CorrelationID, err)
		
		resp := ChatResponse{
			CorrelationID: req.CorrelationID,
			Error:         "Failed to process your request. Please try again.",
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(resp)
		return
	}

	if len(response) > 4000 {
		response = response[:3900] + "\n\n... (response truncated due to length)"
	}

	resp := ChatResponse{
		Response:      response,
		CorrelationID: req.CorrelationID,
		SourceDocs:    sourceDocs,
	}

	log.Printf("Sending response (ID: %s): %d characters, %d source docs", 
		req.CorrelationID, len(response), len(sourceDocs))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *ClaudeProxyService) handleRefreshDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Println("Refreshing documentation...")
	if err := s.LoadDocuments(); err != nil {
		log.Printf("Error refreshing docs: %v", err)
		http.Error(w, "Failed to refresh documents", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "success",
		"documents": len(s.docService.documents),
		"chunks":    len(s.docService.chunks),
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func (s *ClaudeProxyService) healthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"service":   "claude-agent-proxy",
		"model":     s.config.ClaudeModel,
		"documents": len(s.docService.documents),
		"chunks":    len(s.docService.chunks),
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func main() {
	var config Config
	if err := envconfig.Process("", &config); err != nil {
		log.Fatalf("Failed to process environment variables: %v", err)
	}

	service := NewClaudeProxyService(&config)

	if err := service.LoadDocuments(); err != nil {
		log.Printf("Warning: Failed to load documents: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", service.healthCheck)
	mux.HandleFunc("/api/chat", service.handleChat)
	mux.HandleFunc("/api/refresh-docs", service.handleRefreshDocs)

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

	log.Printf("Claude Agent Proxy Service starting on port %s (Model: %s, Docs: %d)", 
		config.Port, config.ClaudeModel, len(service.docService.documents))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}