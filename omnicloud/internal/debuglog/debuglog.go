package debuglog

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

const logPath = "/home/appbox/DCPCLOUDAPP/.cursor/debug.log"

var mu sync.Mutex

// Log writes one NDJSON line to the debug log (location, message, data, hypothesisId).
func Log(location, message, hypothesisId string, data map[string]interface{}) {
	mu.Lock()
	defer mu.Unlock()
	if data == nil {
		data = make(map[string]interface{})
	}
	payload := map[string]interface{}{
		"location":     location,
		"message":      message,
		"data":         data,
		"hypothesisId": hypothesisId,
		"timestamp":    time.Now().UnixNano() / 1e6,
	}
	line, _ := json.Marshal(payload)
	line = append(line, '\n')
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(line)
}
