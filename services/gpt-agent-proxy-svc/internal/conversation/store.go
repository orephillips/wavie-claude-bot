package conversation

import (
	"sync"
	"time"
)

// Message represents a single message in a conversation
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// ConversationContext holds the conversation history for a specific thread
type ConversationContext struct {
	ThreadID     string    `json:"thread_id"`
	Messages     []Message `json:"messages"`
	LastAccessed time.Time `json:"last_accessed"`
}

// Store manages conversation contexts with thread-based storage
type Store struct {
	conversations map[string]*ConversationContext
	mutex         sync.RWMutex
	maxMessages   int
	maxAge        time.Duration
}

// NewStore creates a new conversation store with specified limits
func NewStore(maxMessages int, maxAge time.Duration) *Store {
	store := &Store{
		conversations: make(map[string]*ConversationContext),
		maxMessages:   maxMessages,
		maxAge:        maxAge,
	}

	// Start cleanup routine
	go store.cleanupRoutine()

	return store
}

// GetOrCreate retrieves an existing conversation context or creates a new one
func (s *Store) GetOrCreate(threadID string) *ConversationContext {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	context, exists := s.conversations[threadID]
	if !exists {
		context = &ConversationContext{
			ThreadID:     threadID,
			Messages:     []Message{},
			LastAccessed: time.Now(),
		}
		s.conversations[threadID] = context
	}

	// Check if context is too old
	if time.Since(context.LastAccessed) > s.maxAge {
		context.Messages = []Message{} // Reset if older than max age
	}

	context.LastAccessed = time.Now()
	return context
}

// AddMessage adds a message to a conversation context
func (s *Store) AddMessage(threadID, role, content string) {
	context := s.GetOrCreate(threadID)

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Add new message
	context.Messages = append(context.Messages, Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	})

	// Limit to max messages
	if len(context.Messages) > s.maxMessages {
		context.Messages = context.Messages[len(context.Messages)-s.maxMessages:]
	}
}

// GetMessages returns all messages for a thread, or empty slice if not found or expired
func (s *Store) GetMessages(threadID string) []Message {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	context, exists := s.conversations[threadID]
	if !exists {
		return []Message{}
	}

	// Check if context is too old
	if time.Since(context.LastAccessed) > s.maxAge {
		return []Message{}
	}

	return context.Messages
}

// cleanupRoutine periodically removes old conversations
func (s *Store) cleanupRoutine() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanup()
	}
}

// cleanup removes conversations older than maxAge
func (s *Store) cleanup() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for threadID, context := range s.conversations {
		if time.Since(context.LastAccessed) > s.maxAge {
			delete(s.conversations, threadID)
		}
	}
}
