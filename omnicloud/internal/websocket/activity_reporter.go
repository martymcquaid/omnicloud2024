package websocket

import (
	"context"
	"log"
	"sync"
	"time"
)

// ActivityCollector is a function that returns current activities for a category
type ActivityCollector func() []ActivityItem

// ActivityReporter collects activities from various subsystems and sends them over WebSocket
type ActivityReporter struct {
	connector  *ClientConnector
	collectors map[string]ActivityCollector
	mu         sync.RWMutex
	interval   time.Duration
}

// NewActivityReporter creates a new activity reporter
func NewActivityReporter(connector *ClientConnector, interval time.Duration) *ActivityReporter {
	return &ActivityReporter{
		connector:  connector,
		collectors: make(map[string]ActivityCollector),
		interval:   interval,
	}
}

// RegisterCollector registers an activity collector for a category
func (ar *ActivityReporter) RegisterCollector(category string, collector ActivityCollector) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.collectors[category] = collector
}

// Start begins periodic activity reporting
func (ar *ActivityReporter) Start(ctx context.Context) {
	log.Printf("[Activity Reporter] Starting activity reporter (interval: %v)", ar.interval)

	ticker := time.NewTicker(ar.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !ar.connector.IsConnected() {
				continue
			}
			ar.sendActivityReport()
		}
	}
}

// sendActivityReport collects all activities and sends them as a single message
func (ar *ActivityReporter) sendActivityReport() {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	var allActivities []ActivityItem

	for _, collector := range ar.collectors {
		items := collector()
		if len(items) > 0 {
			allActivities = append(allActivities, items...)
		}
	}

	// Always send (even if empty - means server is idle)
	if len(allActivities) == 0 {
		allActivities = []ActivityItem{
			{
				Category: "system",
				Action:   "idle",
				Title:    "Server idle",
			},
		}
	}

	msg := NewMessage(MessageTypeActivity, map[string]interface{}{
		"server_id":  ar.connector.serverID.String(),
		"activities": allActivities,
	})

	if err := ar.connector.SendMessage(msg); err != nil {
		// Don't log buffer-full errors during normal operation
		if err.Error() != "send buffer full" && err.Error() != "not connected to server" {
			log.Printf("[Activity Reporter] Failed to send activity report: %v", err)
		}
	}
}
