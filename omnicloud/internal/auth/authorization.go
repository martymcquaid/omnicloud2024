package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
)

// AuthorizationManager handles client authorization with encrypted tokens
type AuthorizationManager struct {
	database         *db.DB
	serverID         uuid.UUID
	mainServerURL    string
	macAddress       string
	registrationKey  string
	isAuthorized     bool
	authToken        string
	authTokenExpiry  time.Time
	lastCheckTime    time.Time
	checkInterval    time.Duration
	mu               sync.RWMutex
	stopChan         chan struct{}
	authChangedChan  chan bool // Signal when authorization status changes
}

// NewAuthorizationManager creates a new authorization manager for a client
func NewAuthorizationManager(
	database *db.DB,
	serverID uuid.UUID,
	mainServerURL, macAddress, registrationKey string,
) *AuthorizationManager {
	return &AuthorizationManager{
		database:        database,
		serverID:        serverID,
		mainServerURL:   mainServerURL,
		macAddress:      macAddress,
		registrationKey: registrationKey,
		isAuthorized:    false,
		checkInterval:   30 * time.Second, // Check authorization every 30 seconds
		stopChan:        make(chan struct{}),
		authChangedChan: make(chan bool, 1), // Buffered to avoid blocking
	}
}

// AuthToken represents an authorization token from the server
type AuthToken struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Authorized bool      `json:"authorized"`
}

// Start begins the authorization check loop
func (am *AuthorizationManager) Start() {
	log.Println("[Authorization] Client authorization manager starting")

	ticker := time.NewTicker(am.checkInterval)
	defer ticker.Stop()

	// Check immediately on startup
	am.checkAuthorization()

	go func() {
		for {
			select {
			case <-ticker.C:
				am.checkAuthorization()
			case <-am.stopChan:
				log.Println("[Authorization] Authorization manager stopped")
				return
			}
		}
	}()
}

// Stop stops the authorization check loop
func (am *AuthorizationManager) Stop() {
	close(am.stopChan)
}

// IsAuthorized returns whether this client is currently authorized
func (am *AuthorizationManager) IsAuthorized() bool {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.isAuthorized
}

// AuthChangedChannel returns a channel that signals when authorization status changes
// The channel receives true when authorized, false when revoked
func (am *AuthorizationManager) AuthChangedChannel() <-chan bool {
	return am.authChangedChan
}

// checkAuthorization verifies with the main server if this client is still authorized
func (am *AuthorizationManager) checkAuthorization() {
	// Check if token is still valid (don't spam server if recently checked)
	am.mu.RLock()
	timeSinceCheck := time.Since(am.lastCheckTime)
	currentAuth := am.isAuthorized
	am.mu.RUnlock()

	if timeSinceCheck < 10*time.Second {
		return // Skip if we checked very recently
	}

	// Query the main server for authorization status
	url := fmt.Sprintf("%s/api/v1/servers/%s/auth-status", am.mainServerURL, am.serverID.String())
	req, err := am.createAuthorizedRequest("GET", url)
	if err != nil {
		log.Printf("[Authorization] Failed to create request: %v", err)
		return
	}

	// Execute request with timeout
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Authorization] Failed to check authorization: %v", err)
		// If we can't reach the server, assume not authorized (fail closed)
		if currentAuth {
			am.setAuthorized(false, "")
			log.Println("[Authorization] ⚠️ AUTHORIZATION REVOKED: Cannot reach main server, halting operations")
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		// Unauthorized
		if currentAuth {
			am.setAuthorized(false, "")
			log.Println("[Authorization] ⚠️ AUTHORIZATION REVOKED: Main server reports this client is not authorized")
		}
		return
	}

	if resp.StatusCode != 200 {
		log.Printf("[Authorization] Unexpected status code: %d", resp.StatusCode)
		return
	}

	// Parse response
	var authToken AuthToken
	if err := json.NewDecoder(resp.Body).Decode(&authToken); err != nil {
		log.Printf("[Authorization] Failed to parse auth response: %v", err)
		return
	}

	// Check if authorized and token is valid
	if authToken.Authorized && time.Now().Before(authToken.ExpiresAt) {
		if !currentAuth {
			am.setAuthorized(true, authToken.Token)
			log.Println("[Authorization] ✅ AUTHORIZATION GRANTED: Client is now authorized to run")
		} else {
			am.updateToken(authToken.Token, authToken.ExpiresAt)
		}
	} else {
		if currentAuth {
			am.setAuthorized(false, "")
			log.Println("[Authorization] ⚠️ AUTHORIZATION REVOKED: Token expired or authorization removed")
		}
	}
}

// setAuthorized updates the authorization status and signals the change
func (am *AuthorizationManager) setAuthorized(authorized bool, token string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.isAuthorized = authorized
	am.authToken = token
	am.lastCheckTime = time.Now()

	if authorized {
		am.authTokenExpiry = time.Now().Add(5 * time.Minute)
	}

	// Send signal (non-blocking)
	select {
	case am.authChangedChan <- authorized:
	default:
		// Channel full, skip signal
	}
}

// updateToken refreshes the auth token without changing authorization status
func (am *AuthorizationManager) updateToken(token string, expiry time.Time) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.authToken = token
	am.authTokenExpiry = expiry
	am.lastCheckTime = time.Now()
}

// GetAuthToken returns the current authorization token
func (am *AuthorizationManager) GetAuthToken() string {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.authToken
}

// EncryptToken encrypts an authorization token using AES-256-GCM
func EncryptToken(plaintext, secretKey string) (string, error) {
	key := deriveKey(secretKey)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// DecryptToken decrypts an authorization token
func DecryptToken(ciphertext, secretKey string) (string, error) {
	key := deriveKey(secretKey)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	data, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext2 := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext2, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// deriveKey derives a 32-byte AES key from a secret string
func deriveKey(secret string) []byte {
	// For production, use a proper KDF like PBKDF2 or Argon2
	// For now, use SHA-256 to expand the secret to 32 bytes
	h := sha256.New()
	h.Write([]byte(secret))
	return h.Sum(nil)
}

// createAuthorizedRequest creates an HTTP request with authorization headers
func (am *AuthorizationManager) createAuthorizedRequest(method, url string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}

	// Add authentication headers
	req.Header.Set("X-Server-ID", am.serverID.String())
	req.Header.Set("X-MAC-Address", am.macAddress)

	// Add auth token if available
	token := am.GetAuthToken()
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}

	return req, nil
}
