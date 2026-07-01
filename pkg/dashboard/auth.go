package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SessionStore manages in-memory session tokens with TTL expiry.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]time.Time // session token → expiry
	ttl      time.Duration
}

// NewSessionStore creates a session store with the given TTL for each session.
func NewSessionStore(ttl time.Duration) *SessionStore {
	store := &SessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
	}
	// Background cleanup every 5 minutes
	go store.cleanupLoop(5 * time.Minute)
	return store
}

// Create generates a new random session token and stores it with the configured TTL.
func (s *SessionStore) Create() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)

	s.mu.Lock()
	s.sessions[token] = time.Now().Add(s.ttl)
	s.mu.Unlock()

	return token
}

// Validate checks if a session token exists and has not expired.
func (s *SessionStore) Validate(token string) bool {
	if token == "" {
		return false
	}

	s.mu.RLock()
	expiry, ok := s.sessions[token]
	s.mu.RUnlock()

	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		s.Delete(token)
		return false
	}
	return true
}

// Delete removes a session token from the store.
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// cleanupLoop periodically removes expired sessions.
func (s *SessionStore) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for token, expiry := range s.sessions {
			if now.After(expiry) {
				delete(s.sessions, token)
			}
		}
		s.mu.Unlock()
	}
}

// VerifyPassword checks a plaintext password against a bcrypt hash.
func VerifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// AuthMiddleware returns an HTTP middleware that validates the session cookie.
func AuthMiddleware(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("session_token")
			if err != nil || !store.Validate(cookie.Value) {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SessionCookieName is the cookie name used for session tokens.
const SessionCookieName = "session_token"