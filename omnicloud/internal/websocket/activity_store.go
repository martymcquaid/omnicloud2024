package websocket

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// ServerActivity holds the latest activity report for a server
type ServerActivity struct {
	ServerID   uuid.UUID      `json:"server_id"`
	ServerName string         `json:"server_name"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Activities []ActivityItem `json:"activities"`
}

// ActivityStore stores the latest activity reports from all servers in memory
type ActivityStore struct {
	mu         sync.RWMutex
	activities map[uuid.UUID]*ServerActivity
}

// NewActivityStore creates a new activity store
func NewActivityStore() *ActivityStore {
	return &ActivityStore{
		activities: make(map[uuid.UUID]*ServerActivity),
	}
}

// Update stores the latest activity report for a server
func (s *ActivityStore) Update(serverID uuid.UUID, serverName string, items []ActivityItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.activities[serverID] = &ServerActivity{
		ServerID:   serverID,
		ServerName: serverName,
		UpdatedAt:  time.Now(),
		Activities: items,
	}
}

// Get returns the latest activity for a specific server
func (s *ActivityStore) Get(serverID uuid.UUID) *ServerActivity {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.activities[serverID]
}

// GetAll returns the latest activities for all servers
func (s *ActivityStore) GetAll() map[uuid.UUID]*ServerActivity {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[uuid.UUID]*ServerActivity, len(s.activities))
	for k, v := range s.activities {
		result[k] = v
	}
	return result
}

// Remove removes a server's activity (e.g. when disconnected)
func (s *ActivityStore) Remove(serverID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.activities, serverID)
}

// Cleanup removes stale entries older than the given duration
func (s *ActivityStore) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for id, activity := range s.activities {
		if activity.UpdatedAt.Before(cutoff) {
			delete(s.activities, id)
		}
	}
}
