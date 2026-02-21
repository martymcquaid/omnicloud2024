package websocket

import (
	"encoding/json"
	"time"
)

// MessageType defines the type of WebSocket message
type MessageType string

const (
	// Ping/Pong for connection health
	MessageTypePing       MessageType = "ping"
	MessageTypePong       MessageType = "pong"

	// Server status updates
	MessageTypeHeartbeat  MessageType = "heartbeat"
	MessageTypeStatus     MessageType = "status"

	// Commands from main server to client
	MessageTypeCommand    MessageType = "command"
	MessageTypeRestart    MessageType = "restart"
	MessageTypeUpgrade    MessageType = "upgrade"

	// Responses from client to main server
	MessageTypeResponse   MessageType = "response"
	MessageTypeError      MessageType = "error"

	// Data sync
	MessageTypeSync       MessageType = "sync"
	MessageTypeRequest    MessageType = "request"

	// Activity reporting from clients
	MessageTypeActivity   MessageType = "activity"
)

// CommandType defines specific commands that can be sent
type CommandType string

const (
	CommandRestart        CommandType = "restart"
	CommandUpgrade        CommandType = "upgrade"
	CommandRescan         CommandType = "rescan"
	CommandStatusUpdate   CommandType = "status_update"
	CommandDeleteContent  CommandType = "delete_content"
)

// Message represents a WebSocket message
type Message struct {
	Type      MessageType            `json:"type"`
	MessageID string                 `json:"message_id,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// CommandMessage represents a command from main server to client
type CommandMessage struct {
	Type      MessageType `json:"type"`
	MessageID string      `json:"message_id"`
	Command   CommandType `json:"command"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   interface{} `json:"payload,omitempty"`
}

// HeartbeatPayload contains client status information
type HeartbeatPayload struct {
	ServerID          string  `json:"server_id"`
	ServerName        string  `json:"server_name"`
	MACAddress        string  `json:"mac_address"`
	SoftwareVersion   string  `json:"software_version"`
	StorageCapacityTB float64 `json:"storage_capacity_tb"`
	PackageCount      int     `json:"package_count"`
	PublicIP          string  `json:"public_ip,omitempty"`
	APIURL            string  `json:"api_url,omitempty"`
}

// ActivityReport represents a live activity update from a client server
type ActivityReport struct {
	ServerID   string           `json:"server_id"`
	Timestamp  time.Time        `json:"timestamp"`
	Activities []ActivityItem   `json:"activities"`
}

// ActivityItem represents a single activity happening on a server
type ActivityItem struct {
	Category    string                 `json:"category"`     // scanner, torrent_gen, seeding, downloading, queue, system, sync, transfer
	Action      string                 `json:"action"`       // started, progress, completed, error, idle
	Title       string                 `json:"title"`        // Human-readable summary e.g. "Scanning library"
	Detail      string                 `json:"detail,omitempty"` // Extra detail e.g. file name, path
	Progress    float64                `json:"progress,omitempty"` // 0-100 if applicable
	Speed       string                 `json:"speed,omitempty"`   // Human-readable speed e.g. "45.2 MB/s"
	SpeedBytes  int64                  `json:"speed_bytes,omitempty"` // Raw bytes/sec for UI formatting
	ETA         int                    `json:"eta_seconds,omitempty"` // ETA in seconds
	Extra       map[string]interface{} `json:"extra,omitempty"` // Any additional data
	StartedAt   *time.Time             `json:"started_at,omitempty"`
}

// ResponseMessage represents a response to a command
type ResponseMessage struct {
	Type         MessageType `json:"type"`
	MessageID    string      `json:"message_id"`    // ID of the command being responded to
	ResponseID   string      `json:"response_id"`   // Unique ID for this response
	Success      bool        `json:"success"`
	Message      string      `json:"message,omitempty"`
	Error        string      `json:"error,omitempty"`
	Timestamp    time.Time   `json:"timestamp"`
	Payload      interface{} `json:"payload,omitempty"`
}

// NewMessage creates a new message
func NewMessage(msgType MessageType, payload map[string]interface{}) *Message {
	return &Message{
		Type:      msgType,
		MessageID: generateMessageID(),
		Timestamp: time.Now(),
		Payload:   payload,
	}
}

// NewCommandMessage creates a new command message
func NewCommandMessage(command CommandType, payload interface{}) *CommandMessage {
	return &CommandMessage{
		Type:      MessageTypeCommand,
		MessageID: generateMessageID(),
		Command:   command,
		Timestamp: time.Now(),
		Payload:   payload,
	}
}

// NewResponseMessage creates a response message
func NewResponseMessage(commandID string, success bool, message string, err error, payload interface{}) *ResponseMessage {
	resp := &ResponseMessage{
		Type:       MessageTypeResponse,
		MessageID:  commandID,
		ResponseID: generateMessageID(),
		Success:    success,
		Message:    message,
		Timestamp:  time.Now(),
		Payload:    payload,
	}

	if err != nil {
		resp.Error = err.Error()
	}

	return resp
}

// ToJSON converts message to JSON bytes
func (m *Message) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// ToJSON converts command message to JSON bytes
func (cm *CommandMessage) ToJSON() ([]byte, error) {
	return json.Marshal(cm)
}

// ToJSON converts response message to JSON bytes
func (rm *ResponseMessage) ToJSON() ([]byte, error) {
	return json.Marshal(rm)
}

// ParseMessage parses JSON bytes into a Message
func ParseMessage(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// ParseCommandMessage parses JSON bytes into a CommandMessage
func ParseCommandMessage(data []byte) (*CommandMessage, error) {
	var cmd CommandMessage
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, err
	}
	return &cmd, nil
}

// ParseResponseMessage parses JSON bytes into a ResponseMessage
func ParseResponseMessage(data []byte) (*ResponseMessage, error) {
	var resp ResponseMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// generateMessageID generates a unique message ID
func generateMessageID() string {
	return time.Now().Format("20060102150405.000000")
}
