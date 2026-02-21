package auth

import (
	"log"
	"sync"
	"time"
)

// OperationsController controls whether the client can run operations
// It halts operations if the client is not authorized
type OperationsController struct {
	authManager *AuthorizationManager
	isPaused    bool
	mu          sync.RWMutex

	// Callbacks for lifecycle events
	onAuthGranted  func()
	onAuthRevoked  func()
	pausedOperations map[string]bool
}

// NewOperationsController creates a new operations controller
func NewOperationsController(authManager *AuthorizationManager) *OperationsController {
	return &OperationsController{
		authManager:      authManager,
		pausedOperations: make(map[string]bool),
	}
}

// OnAuthGranted sets the callback for when authorization is granted
func (oc *OperationsController) OnAuthGranted(callback func()) {
	oc.onAuthGranted = callback
}

// OnAuthRevoked sets the callback for when authorization is revoked
func (oc *OperationsController) OnAuthRevoked(callback func()) {
	oc.onAuthRevoked = callback
}

// Start begins monitoring authorization status and controlling operations
func (oc *OperationsController) Start() {
	log.Println("[Operations] Operations controller starting")

	go func() {
		for authorized := range oc.authManager.AuthChangedChannel() {
			if authorized {
				oc.handleAuthGranted()
			} else {
				oc.handleAuthRevoked()
			}
		}
	}()

	// Check initial authorization status
	if oc.authManager.IsAuthorized() {
		oc.handleAuthGranted()
	} else {
		oc.handleAuthRevoked()
	}
}

// Stop stops the operations controller
func (oc *OperationsController) Stop() {
	log.Println("[Operations] Operations controller stopped")
}

// CanRunOperation checks if a specific operation is allowed
func (oc *OperationsController) CanRunOperation(operationName string) bool {
	if !oc.authManager.IsAuthorized() {
		log.Printf("[Operations] ⚠️ Operation '%s' blocked: client not authorized", operationName)
		return false
	}
	return true
}

// PauseOperation pauses a specific operation (scanner, queue, etc.)
func (oc *OperationsController) PauseOperation(operationName string) {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	oc.pausedOperations[operationName] = true
	log.Printf("[Operations] ⏸️ Paused operation: %s (authorization revoked)", operationName)
}

// ResumeOperation resumes a specific operation
func (oc *OperationsController) ResumeOperation(operationName string) {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	delete(oc.pausedOperations, operationName)
	log.Printf("[Operations] ▶️ Resumed operation: %s (authorization granted)", operationName)
}

// IsOperationPaused checks if an operation is paused
func (oc *OperationsController) IsOperationPaused(operationName string) bool {
	oc.mu.RLock()
	defer oc.mu.RUnlock()

	return oc.pausedOperations[operationName]
}

// GetPausedOperations returns list of paused operations
func (oc *OperationsController) GetPausedOperations() []string {
	oc.mu.RLock()
	defer oc.mu.RUnlock()

	var paused []string
	for op := range oc.pausedOperations {
		paused = append(paused, op)
	}
	return paused
}

// handleAuthGranted is called when authorization is granted
func (oc *OperationsController) handleAuthGranted() {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	if oc.isPaused {
		log.Println("[Operations] ✅ AUTHORIZATION GRANTED - Resuming all operations")
		oc.isPaused = false

		// Clear all paused operations
		oc.pausedOperations = make(map[string]bool)

		// Call callback
		if oc.onAuthGranted != nil {
			go oc.onAuthGranted()
		}
	}
}

// handleAuthRevoked is called when authorization is revoked
func (oc *OperationsController) handleAuthRevoked() {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	if !oc.isPaused {
		log.Println("[Operations] ⚠️ AUTHORIZATION REVOKED - Halting all operations")
		log.Println("[Operations] Waiting for re-authorization...")
		oc.isPaused = true

		// Mark key operations as paused
		oc.pausedOperations["scanner"] = true
		oc.pausedOperations["torrent_queue"] = true
		oc.pausedOperations["transfer_processor"] = true
		oc.pausedOperations["status_reporter"] = true
		oc.pausedOperations["update_agent"] = true

		// Call callback
		if oc.onAuthRevoked != nil {
			go oc.onAuthRevoked()
		}
	}
}

// WaitForAuthorization blocks until the client is authorized
// Returns true if authorized, false if context cancelled
func (oc *OperationsController) WaitForAuthorization(timeout time.Duration) bool {
	if oc.authManager.IsAuthorized() {
		return true
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	endTime := time.Now().Add(timeout)
	for {
		if oc.authManager.IsAuthorized() {
			log.Println("[Operations] ✅ Client authorized - starting operations")
			return true
		}

		if time.Now().After(endTime) {
			log.Println("[Operations] ⏰ Timeout waiting for authorization")
			return false
		}

		select {
		case <-ticker.C:
			// Continue checking
		}
	}
}
