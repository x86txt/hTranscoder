package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	// DiscoveryPort is the UDP port used for discovery
	DiscoveryPort = 9999
	// BroadcastInterval is how often to broadcast discovery messages
	BroadcastInterval = 5 * time.Second
	// DiscoveryTimeout is how long to wait for responses
	DiscoveryTimeout = 10 * time.Second
)

// DiscoveryMessage represents a discovery broadcast message
type DiscoveryMessage struct {
	Type       string `json:"type"`       // "request" or "response"
	SecretHash string `json:"secretHash"` // SHA256 hash of the secret
	ServerAddr string `json:"serverAddr"` // Server address (only in response)
	ServerPort int    `json:"serverPort"` // Server port (only in response)
	ServerName string `json:"serverName"` // Server name (only in response)
}

// Server handles discovery requests from workers
type Server struct {
	secretKey  string
	serverAddr string
	serverPort int
	serverName string
	conn       *net.UDPConn
	done       chan bool
}

// NewServer creates a new discovery server
func NewServer(secretKey, serverAddr string, serverPort int, serverName string) *Server {
	return &Server{
		secretKey:  secretKey,
		serverAddr: serverAddr,
		serverPort: serverPort,
		serverName: serverName,
		done:       make(chan bool),
	}
}

// Start begins listening for discovery requests
func (s *Server) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", DiscoveryPort))
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP port %d: %w", DiscoveryPort, err)
	}

	s.conn = conn
	fmt.Printf("Discovery server listening on UDP port %d\n", DiscoveryPort)

	go s.handleRequests()
	return nil
}

// Stop stops the discovery server
func (s *Server) Stop() {
	close(s.done)
	if s.conn != nil {
		s.conn.Close()
	}
}

func (s *Server) handleRequests() {
	buffer := make([]byte, 1024)
	expectedHash := hashSecret(s.secretKey)

	for {
		select {
		case <-s.done:
			return
		default:
			s.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, addr, err := s.conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				fmt.Printf("Error reading UDP: %v\n", err)
				continue
			}

			// Basic validation - check if data looks like JSON
			data := buffer[:n]
			if len(data) == 0 {
				continue
			}

			// Check if the first character looks like JSON
			firstChar := data[0]
			if firstChar != '{' && firstChar != '[' {
				// Not JSON, likely from another service - silently ignore
				continue
			}

			var msg DiscoveryMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				// Only log JSON errors if the data actually looks like it might be JSON
				if firstChar == '{' {
					fmt.Printf("Error unmarshaling potential discovery message from %s (length: %d): %v\n", addr, len(data), err)
					fmt.Printf("First 50 chars: %q\n", string(data[:min(50, len(data))]))
				}
				continue
			}

			// Verify the request
			if msg.Type == "request" && msg.SecretHash == expectedHash {
				// Get the actual server IP if serverAddr is empty or 0.0.0.0
				serverIP := s.serverAddr
				if serverIP == "" || serverIP == "0.0.0.0" {
					// Use the interface that received the request
					serverIP = getOutboundIP(addr.IP).String()
				}

				response := DiscoveryMessage{
					Type:       "response",
					SecretHash: expectedHash,
					ServerAddr: serverIP,
					ServerPort: s.serverPort,
					ServerName: s.serverName,
				}

				responseData, err := json.Marshal(response)
				if err != nil {
					fmt.Printf("Error marshaling response: %v\n", err)
					continue
				}

				_, err = s.conn.WriteToUDP(responseData, addr)
				if err != nil {
					fmt.Printf("Error sending response: %v\n", err)
					continue
				}

				fmt.Printf("Sent discovery response to %s\n", addr)
			}
		}
	}
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Client handles discovery of servers
type Client struct {
	secretKey string
}

// NewClient creates a new discovery client
func NewClient(secretKey string) *Client {
	return &Client{
		secretKey: secretKey,
	}
}

// DiscoverServer broadcasts discovery requests and returns the first responding server
func (c *Client) DiscoverServer() (serverAddr string, serverPort int, serverName string, err error) {
	// Create UDP socket
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to create UDP socket: %w", err)
	}
	defer conn.Close()

	// Enable broadcast
	broadcast, err := net.ResolveUDPAddr("udp", fmt.Sprintf("255.255.255.255:%d", DiscoveryPort))
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to resolve broadcast address: %w", err)
	}

	// Create discovery request
	request := DiscoveryMessage{
		Type:       "request",
		SecretHash: hashSecret(c.secretKey),
	}

	data, err := json.Marshal(request)
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send discovery requests periodically
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	timeout := time.After(DiscoveryTimeout)
	responseChan := make(chan DiscoveryMessage, 1)

	// Start listening for responses
	go func() {
		buffer := make([]byte, 1024)
		for {
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, _, err := conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}

			// Basic validation - check if data looks like JSON
			data := buffer[:n]
			if len(data) == 0 {
				continue
			}

			// Check if the first character looks like JSON
			if data[0] != '{' && data[0] != '[' {
				// Not JSON, ignore
				continue
			}

			var msg DiscoveryMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				// Silently ignore JSON parse errors from other services
				continue
			}

			if msg.Type == "response" && msg.SecretHash == hashSecret(c.secretKey) {
				responseChan <- msg
				return
			}
		}
	}()

	// Send discovery broadcasts
	for {
		select {
		case <-ticker.C:
			_, err = conn.WriteToUDP(data, broadcast)
			if err != nil {
				fmt.Printf("Error sending discovery broadcast: %v\n", err)
			} else {
				fmt.Println("Sent discovery broadcast")
			}

		case response := <-responseChan:
			return response.ServerAddr, response.ServerPort, response.ServerName, nil

		case <-timeout:
			return "", 0, "", fmt.Errorf("discovery timeout - no server found")
		}
	}
}

// hashSecret creates a SHA256 hash of the secret key
func hashSecret(secret string) string {
	hash := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hash[:])
}

// getOutboundIP gets the preferred outbound IP of this machine
func getOutboundIP(targetIP net.IP) net.IP {
	// Try to connect to the target IP to determine which interface to use
	conn, err := net.Dial("udp", fmt.Sprintf("%s:80", targetIP))
	if err != nil {
		// Fallback: try to connect to a public IP
		conn, err = net.Dial("udp", "8.8.8.8:80")
		if err != nil {
			return net.IPv4(127, 0, 0, 1)
		}
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP
}
