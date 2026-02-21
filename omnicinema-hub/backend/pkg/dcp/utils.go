package dcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// GetMACAddress returns the MAC address of the primary network interface
func GetMACAddress() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		// Get the hardware address (MAC)
		mac := iface.HardwareAddr.String()
		if mac != "" {
			return strings.ToUpper(mac), nil
		}
	}

	return "", fmt.Errorf("no active network interface found")
}

// HashRegistrationKey creates a SHA256 hash of the registration key
func HashRegistrationKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// VerifyRegistrationKey checks if a key matches the stored hash
func VerifyRegistrationKey(key, hash string) bool {
	return HashRegistrationKey(key) == hash
}
