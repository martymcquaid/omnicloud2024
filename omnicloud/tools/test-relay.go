// test-relay.go — End-to-end test of the OmniCloud relay system
//
// Tests:
//   1. Relay server connectivity (port 10866)
//   2. Seeder registration (RELAY-REGISTER)
//   3. Downloader connection via relay (RELAY-CONNECT)
//   4. Session establishment and data bridging
//   5. Keepalive (PING/PONG)
//
// Usage:
//   go run tools/test-relay.go [relay-address]
//   Default relay address: localhost:10866

package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	testTimeout = 30 * time.Second
)

var (
	passed  int
	failed  int
	relayAddr string
)

func main() {
	relayAddr = "localhost:10866"
	if len(os.Args) > 1 {
		relayAddr = os.Args[1]
	}

	fmt.Println("========================================")
	fmt.Println("  OmniCloud Relay System Test Suite")
	fmt.Println("========================================")
	fmt.Printf("Relay server: %s\n", relayAddr)
	fmt.Printf("Time: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	// Test 1: Basic connectivity
	testConnectivity()

	// Test 2: Seeder registration
	testRegistration()

	// Test 3: Ping/Pong keepalive
	testPingPong()

	// Test 4: Full relay flow (register → connect → bridge data)
	testFullRelayFlow()

	// Test 5: Unregistered peer (direct dial fallback)
	testUnregisteredPeer()

	// Test 6: Multiple concurrent sessions
	testConcurrentSessions()

	// Summary
	fmt.Println("\n========================================")
	fmt.Printf("  Results: %d PASSED, %d FAILED\n", passed, failed)
	fmt.Println("========================================")

	if failed > 0 {
		os.Exit(1)
	}
}

func pass(name string) {
	passed++
	fmt.Printf("  [PASS] %s\n", name)
}

func fail(name, reason string) {
	failed++
	fmt.Printf("  [FAIL] %s: %s\n", name, reason)
}

func sendMsg(conn net.Conn, msg string) error {
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := fmt.Fprintf(conn, "%s\n", msg)
	return err
}

func readMsg(conn net.Conn, timeout time.Duration) (string, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// readMsgFromReader reads using an existing reader (for connections where we need multiple reads)
func readMsgFromReader(reader *bufio.Reader, conn net.Conn, timeout time.Duration) (string, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// ============================================================
// Test 1: Basic Connectivity
// ============================================================
func testConnectivity() {
	fmt.Println("\n--- Test 1: Relay Server Connectivity ---")

	conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("TCP connect", fmt.Sprintf("Cannot connect to %s: %v", relayAddr, err))
		return
	}
	conn.Close()
	pass("TCP connect to relay server")

	// Verify we can send an invalid command and get an error (server is responsive)
	conn, err = net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("Protocol check", fmt.Sprintf("Cannot reconnect: %v", err))
		return
	}
	defer conn.Close()

	if err := sendMsg(conn, "INVALID-COMMAND test"); err != nil {
		fail("Protocol check", fmt.Sprintf("Cannot send: %v", err))
		return
	}

	resp, err := readMsg(conn, 5*time.Second)
	if err != nil {
		fail("Protocol check", fmt.Sprintf("No response: %v", err))
		return
	}

	if strings.HasPrefix(resp, "ERROR") {
		pass(fmt.Sprintf("Protocol responsive (got: %q)", resp))
	} else {
		fail("Protocol check", fmt.Sprintf("Unexpected response: %q", resp))
	}
}

// ============================================================
// Test 2: Seeder Registration
// ============================================================
func testRegistration() {
	fmt.Println("\n--- Test 2: Seeder Registration ---")

	conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("Registration connect", fmt.Sprintf("Cannot connect: %v", err))
		return
	}
	defer conn.Close()

	// Register as a fake seeder
	fakeAddr := "10.99.99.1:55555"
	if err := sendMsg(conn, fmt.Sprintf("RELAY-REGISTER %s", fakeAddr)); err != nil {
		fail("Send REGISTER", fmt.Sprintf("Cannot send: %v", err))
		return
	}

	resp, err := readMsg(conn, 5*time.Second)
	if err != nil {
		fail("Registration response", fmt.Sprintf("No response: %v", err))
		return
	}

	if resp == "OK" {
		pass(fmt.Sprintf("Registered as %s", fakeAddr))
	} else {
		fail("Registration response", fmt.Sprintf("Expected 'OK', got %q", resp))
	}
}

// ============================================================
// Test 3: Ping/Pong Keepalive
// ============================================================
func testPingPong() {
	fmt.Println("\n--- Test 3: Ping/Pong Keepalive ---")

	conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("PingPong connect", fmt.Sprintf("Cannot connect: %v", err))
		return
	}
	defer conn.Close()

	// Register first (need control connection)
	fakeAddr := "10.99.99.2:55556"
	if err := sendMsg(conn, fmt.Sprintf("RELAY-REGISTER %s", fakeAddr)); err != nil {
		fail("PingPong register", fmt.Sprintf("Cannot send: %v", err))
		return
	}

	resp, err := readMsg(conn, 5*time.Second)
	if err != nil || resp != "OK" {
		fail("PingPong register", fmt.Sprintf("Registration failed: resp=%q err=%v", resp, err))
		return
	}

	// Send a PING, expect PONG back
	if err := sendMsg(conn, "PING"); err != nil {
		fail("Send PING", fmt.Sprintf("Cannot send: %v", err))
		return
	}

	resp, err = readMsg(conn, 5*time.Second)
	if err != nil {
		fail("PONG response", fmt.Sprintf("No response: %v", err))
		return
	}

	if resp == "PONG" {
		pass("PING/PONG keepalive works")
	} else {
		fail("PONG response", fmt.Sprintf("Expected 'PONG', got %q", resp))
	}
}

// ============================================================
// Test 4: Full Relay Flow (the main test)
// ============================================================
func testFullRelayFlow() {
	fmt.Println("\n--- Test 4: Full Relay Flow (Register → Connect → Bridge) ---")

	fakeSeederAddr := "10.99.99.10:60000"
	testData := "HELLO FROM DOWNLOADER TO SEEDER VIA RELAY"
	testReply := "HELLO FROM SEEDER TO DOWNLOADER VIA RELAY"

	// Step 1: Register a fake seeder
	seederCtrl, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("Seeder control connect", fmt.Sprintf("Cannot connect: %v", err))
		return
	}
	defer seederCtrl.Close()

	if err := sendMsg(seederCtrl, fmt.Sprintf("RELAY-REGISTER %s", fakeSeederAddr)); err != nil {
		fail("Seeder register", fmt.Sprintf("Cannot send: %v", err))
		return
	}
	seederCtrlReader := bufio.NewReader(seederCtrl)
	resp, err := readMsgFromReader(seederCtrlReader, seederCtrl, 5*time.Second)
	if err != nil || resp != "OK" {
		fail("Seeder register", fmt.Sprintf("Registration failed: resp=%q err=%v", resp, err))
		return
	}
	pass("Seeder registered with relay")

	// Step 2: Downloader requests connection to seeder
	var sessionID string
	var downloaderConn net.Conn

	// We need to handle the seeder side concurrently:
	// - Seeder will receive SESSION-REQUEST on control conn
	// - Seeder opens data conn and sends RELAY-SESSION
	var wg sync.WaitGroup
	seederDataReady := make(chan net.Conn, 1)
	seederErr := make(chan error, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Wait for SESSION-REQUEST on control connection
		msg, err := readMsgFromReader(seederCtrlReader, seederCtrl, 15*time.Second)
		if err != nil {
			seederErr <- fmt.Errorf("no SESSION-REQUEST received: %v", err)
			return
		}

		parts := strings.SplitN(msg, " ", 2)
		if len(parts) != 2 || parts[0] != "SESSION-REQUEST" {
			seederErr <- fmt.Errorf("expected SESSION-REQUEST, got %q", msg)
			return
		}
		sessID := parts[1]

		// Open data connection
		dataConn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
		if err != nil {
			seederErr <- fmt.Errorf("cannot open data connection: %v", err)
			return
		}

		// Send RELAY-SESSION
		if err := sendMsg(dataConn, fmt.Sprintf("RELAY-SESSION %s", sessID)); err != nil {
			dataConn.Close()
			seederErr <- fmt.Errorf("cannot send RELAY-SESSION: %v", err)
			return
		}

		// Read OK
		resp, err := readMsg(dataConn, 5*time.Second)
		if err != nil {
			dataConn.Close()
			seederErr <- fmt.Errorf("no OK for data conn: %v", err)
			return
		}
		if resp != "OK" {
			dataConn.Close()
			seederErr <- fmt.Errorf("data conn rejected: %q", resp)
			return
		}

		seederDataReady <- dataConn
	}()

	// Downloader connects
	downloaderConn, err = net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("Downloader connect", fmt.Sprintf("Cannot connect: %v", err))
		return
	}
	defer downloaderConn.Close()

	if err := sendMsg(downloaderConn, fmt.Sprintf("RELAY-CONNECT %s", fakeSeederAddr)); err != nil {
		fail("Downloader CONNECT", fmt.Sprintf("Cannot send: %v", err))
		return
	}

	// Wait for OK from relay (this blocks until seeder completes its side)
	resp, err = readMsg(downloaderConn, 20*time.Second)
	if err != nil {
		fail("Downloader CONNECT response", fmt.Sprintf("No response: %v", err))
		return
	}

	parts := strings.SplitN(resp, " ", 2)
	if parts[0] != "OK" {
		fail("Downloader CONNECT response", fmt.Sprintf("Expected OK, got %q", resp))
		return
	}
	if len(parts) > 1 {
		sessionID = parts[1]
	}
	pass(fmt.Sprintf("Session established (id=%s)", sessionID))

	// Check seeder side
	select {
	case err := <-seederErr:
		fail("Seeder session", err.Error())
		return
	case <-time.After(1 * time.Second):
		// Give a moment for seeder goroutine
	}

	var seederDataConn net.Conn
	select {
	case seederDataConn = <-seederDataReady:
		defer seederDataConn.Close()
		pass("Seeder data connection established")
	case err := <-seederErr:
		fail("Seeder data connection", err.Error())
		return
	case <-time.After(10 * time.Second):
		fail("Seeder data connection", "Timeout waiting for seeder data conn")
		return
	}

	// Step 3: Test data bridging (downloader → seeder)
	downloaderConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = fmt.Fprint(downloaderConn, testData)
	if err != nil {
		fail("Data bridge (dl→seeder)", fmt.Sprintf("Write failed: %v", err))
		return
	}

	// Read on seeder side
	buf := make([]byte, len(testData)+100)
	seederDataConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := seederDataConn.Read(buf)
	if err != nil {
		fail("Data bridge (dl→seeder)", fmt.Sprintf("Read failed: %v", err))
		return
	}

	received := string(buf[:n])
	if received == testData {
		pass(fmt.Sprintf("Data bridge dl→seeder: %d bytes transferred correctly", n))
	} else {
		fail("Data bridge (dl→seeder)", fmt.Sprintf("Data mismatch: expected %q, got %q", testData, received))
		return
	}

	// Step 4: Test reverse direction (seeder → downloader)
	seederDataConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = fmt.Fprint(seederDataConn, testReply)
	if err != nil {
		fail("Data bridge (seeder→dl)", fmt.Sprintf("Write failed: %v", err))
		return
	}

	buf2 := make([]byte, len(testReply)+100)
	downloaderConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err = downloaderConn.Read(buf2)
	if err != nil {
		fail("Data bridge (seeder→dl)", fmt.Sprintf("Read failed: %v", err))
		return
	}

	received2 := string(buf2[:n])
	if received2 == testReply {
		pass(fmt.Sprintf("Data bridge seeder→dl: %d bytes transferred correctly", n))
	} else {
		fail("Data bridge (seeder→dl)", fmt.Sprintf("Data mismatch: expected %q, got %q", testReply, received2))
	}

	wg.Wait()
}

// ============================================================
// Test 5: Unregistered peer (direct dial fallback)
// ============================================================
func testUnregisteredPeer() {
	fmt.Println("\n--- Test 5: Unregistered Peer (Direct Dial Fallback) ---")

	conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("Unregistered peer connect", fmt.Sprintf("Cannot connect: %v", err))
		return
	}
	defer conn.Close()

	// Try to connect to a peer that doesn't exist and isn't directly reachable
	if err := sendMsg(conn, "RELAY-CONNECT 10.255.255.1:99999"); err != nil {
		fail("Send CONNECT", fmt.Sprintf("Cannot send: %v", err))
		return
	}

	// Should get ERROR after direct dial timeout (ConnectTimeout = 10s)
	resp, err := readMsg(conn, 15*time.Second)
	if err != nil {
		fail("Unregistered peer response", fmt.Sprintf("No response: %v", err))
		return
	}

	if strings.HasPrefix(resp, "ERROR") {
		pass(fmt.Sprintf("Correctly rejected unregistered/unreachable peer (got: %q)", resp))
	} else {
		fail("Unregistered peer response", fmt.Sprintf("Expected ERROR, got %q", resp))
	}
}

// ============================================================
// Test 6: Multiple Concurrent Sessions
// ============================================================
func testConcurrentSessions() {
	fmt.Println("\n--- Test 6: Multiple Concurrent Sessions ---")

	fakeSeederAddr := "10.99.99.20:60001"

	// Register seeder
	seederCtrl, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		fail("Concurrent: seeder connect", fmt.Sprintf("Cannot connect: %v", err))
		return
	}
	defer seederCtrl.Close()

	if err := sendMsg(seederCtrl, fmt.Sprintf("RELAY-REGISTER %s", fakeSeederAddr)); err != nil {
		fail("Concurrent: seeder register", fmt.Sprintf("Cannot send: %v", err))
		return
	}
	seederReader := bufio.NewReader(seederCtrl)
	resp, err := readMsgFromReader(seederReader, seederCtrl, 5*time.Second)
	if err != nil || resp != "OK" {
		fail("Concurrent: seeder register", fmt.Sprintf("Failed: resp=%q err=%v", resp, err))
		return
	}

	// Launch 3 concurrent downloader connections
	numSessions := 3
	var wg sync.WaitGroup
	results := make(chan bool, numSessions)

	// Seeder side: handle all SESSION-REQUESTs
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numSessions; i++ {
			msg, err := readMsgFromReader(seederReader, seederCtrl, 20*time.Second)
			if err != nil {
				fmt.Printf("    Seeder: error reading session request %d: %v\n", i, err)
				return
			}

			parts := strings.SplitN(msg, " ", 2)
			if len(parts) != 2 || parts[0] != "SESSION-REQUEST" {
				fmt.Printf("    Seeder: unexpected message: %q\n", msg)
				return
			}

			sessID := parts[1]

			// Open data connection
			go func(sid string) {
				dataConn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
				if err != nil {
					fmt.Printf("    Seeder: data conn failed for %s: %v\n", sid, err)
					return
				}
				defer dataConn.Close()

				sendMsg(dataConn, fmt.Sprintf("RELAY-SESSION %s", sid))
				resp, err := readMsg(dataConn, 5*time.Second)
				if err != nil || resp != "OK" {
					fmt.Printf("    Seeder: session %s rejected: %v %q\n", sid, err, resp)
					return
				}

				// Read test data from downloader
				buf := make([]byte, 100)
				dataConn.SetReadDeadline(time.Now().Add(5 * time.Second))
				n, err := dataConn.Read(buf)
				if err != nil && err != io.EOF {
					fmt.Printf("    Seeder: read error on session %s: %v\n", sid, err)
					return
				}

				// Echo it back
				if n > 0 {
					dataConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					dataConn.Write(buf[:n])
				}
			}(sessID)
		}
	}()

	// Launch downloaders
	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
			if err != nil {
				fmt.Printf("    Downloader %d: connect failed: %v\n", idx, err)
				results <- false
				return
			}
			defer conn.Close()

			if err := sendMsg(conn, fmt.Sprintf("RELAY-CONNECT %s", fakeSeederAddr)); err != nil {
				fmt.Printf("    Downloader %d: send failed: %v\n", idx, err)
				results <- false
				return
			}

			resp, err := readMsg(conn, 20*time.Second)
			if err != nil {
				fmt.Printf("    Downloader %d: no response: %v\n", idx, err)
				results <- false
				return
			}

			if !strings.HasPrefix(resp, "OK") {
				fmt.Printf("    Downloader %d: rejected: %q\n", idx, resp)
				results <- false
				return
			}

			// Send test data
			testMsg := fmt.Sprintf("CONCURRENT-TEST-%d", idx)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprint(conn, testMsg)

			// Read echo
			buf := make([]byte, 100)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				fmt.Printf("    Downloader %d: read error: %v\n", idx, err)
				results <- false
				return
			}

			if string(buf[:n]) == testMsg {
				results <- true
			} else {
				fmt.Printf("    Downloader %d: data mismatch\n", idx)
				results <- false
			}
		}(i)
	}

	// Wait with timeout
	go func() {
		wg.Wait()
		close(results)
	}()

	successCount := 0
	timeout := time.After(30 * time.Second)
	for i := 0; i < numSessions; i++ {
		select {
		case ok := <-results:
			if ok {
				successCount++
			}
		case <-timeout:
			fail("Concurrent sessions", "Timeout waiting for results")
			return
		}
	}

	if successCount == numSessions {
		pass(fmt.Sprintf("All %d concurrent sessions completed successfully", numSessions))
	} else {
		fail("Concurrent sessions", fmt.Sprintf("Only %d/%d succeeded", successCount, numSessions))
	}
}
