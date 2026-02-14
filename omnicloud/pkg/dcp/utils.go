package dcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// GetPublicIP attempts to detect the public IP address
func GetPublicIP() string {
	// Try multiple services for reliability
	services := []string{
		"https://api.ipify.org",
		"https://icanhazip.com",
		"https://ifconfig.me",
	}

	for _, service := range services {
		resp, err := net.DialTimeout("tcp", service[8:]+":80", 5*time.Second)
		if err == nil {
			resp.Close()
			// Service is reachable, try to get IP
			httpResp, err := http.Get(service)
			if err == nil {
				defer httpResp.Body.Close()
				body, err := ioutil.ReadAll(httpResp.Body)
				if err == nil {
					ip := strings.TrimSpace(string(body))
					if net.ParseIP(ip) != nil {
						return ip
					}
				}
			}
		}
	}

	// Fallback: try to get local IP by dialing (doesn't actually connect)
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// CalculateDirectorySize calculates total size of a directory in bytes
func CalculateDirectorySize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
