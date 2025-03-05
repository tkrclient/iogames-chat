// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
//	"bytes"
	"log"
	"net/http"
	"time"
    "encoding/json"

	"github.com/gorilla/websocket"

	// For saving database of chat
	"bufio"
    "os"
)

func (c *Client) register(username string) {
	c.username = username
	c.hub.register <- c
}

type Message struct {
    Username string `json:"username"`
    Color    string `json:"color"`
    Text     string `json:"text"`
}

var chatLogFile *os.File // Global file pointer for storing chat messages

// ReadLastMessages reads the last `n` lines from the chat log file
func ReadLastMessages(filePath string, n int) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)

	// Read all lines into a slice
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Return only the last `n` lines
	if len(lines) > n {
		return lines[len(lines)-n:], nil
	}
	return lines, nil
}

func (c *Client) sendChatHistory() {
	const chatLogPath = "chat_log.txt" // Path to your chat log file
	const maxMessages = 50            // Number of messages to send

	// Read the last `maxMessages` from the log file
	messages, err := ReadLastMessages(chatLogPath, maxMessages)
	if err != nil {
		log.Printf("Error reading chat log: %v", err)
		return
	}

	// Send each message to the client
	for _, message := range messages {
		err := c.conn.WriteMessage(websocket.TextMessage, []byte(message))
		if err != nil {
			log.Printf("Error sending message to client: %v", err)
			break
		}
	}
}

// Initialize the chat log file
func initChatLog() {
	var err error
	chatLogFile, err = os.OpenFile("chat_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Error opening chat log file: %v", err)
	}
}

// Save rawMessage and msg to the log file
func saveToLog(rawMessage []byte, msg Message) {
	if chatLogFile == nil {
		log.Println("Chat log file is not initialized")
		return
	}

	// Write rawMessage to the file
	_, err := chatLogFile.WriteString(string(rawMessage) + "\n")
	if err != nil {
		log.Printf("Error writing raw message to chat log file: %v", err)
		return
	}
}

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
    CheckOrigin: func(r *http.Request) bool {
        return true // This allows all origins
    },
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

    // Add fields for username and color
    username string
    color    string

    // Timestamps of last messages sent
    messageTimestamps []time.Time
}

func newClient(hub *Hub, conn *websocket.Conn) *Client {
    return &Client{
        hub: hub,
        conn: conn,
        send: make(chan []byte, 256),
        messageTimestamps: make([]time.Time, 0),
    }
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
    defer func() {
        c.hub.unregister <- c
        c.conn.Close()
    }()

    c.conn.SetReadLimit(maxMessageSize)
    c.conn.SetReadDeadline(time.Now().Add(pongWait))
    c.conn.SetPongHandler(func(string) error {
        c.conn.SetReadDeadline(time.Now().Add(pongWait))
        return nil
    })

	// Send chat history to the newly connected client
	c.sendChatHistory()

    for {
        _, rawMessage, err := c.conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
                log.Printf("error: %v", err)
            }
            break
        }

        // Check message rate limit
        now := time.Now()
        c.messageTimestamps = append(c.messageTimestamps, now)

        // Remove timestamps older than 1 second
        for len(c.messageTimestamps) > 0 && now.Sub(c.messageTimestamps[0]) > time.Second {
            c.messageTimestamps = c.messageTimestamps[1:]
        }

        if len(c.messageTimestamps) >= 3 {
            log.Println("Message rate limit exceeded. Blocking client: ", c.conn.RemoteAddr())
		    // Block the client by closing the connection
            c.conn.Close()
            return
        }

        var clientMsg Message

        // Parse incoming raw JSON message into clientMsg struct
        err = json.Unmarshal(rawMessage, &clientMsg)
        if err != nil {
            log.Println("Error parsing incoming message:", err)
            continue
        }

        msg := Message{
            Username: clientMsg.Username,
            Color:    clientMsg.Color,
            Text:     clientMsg.Text,
        }

	    initChatLog()
		saveToLog(rawMessage, msg) // Save rawMessage and msg to the log file

        // Serialize the message as JSON for broadcasting
        jsonMessage, err := json.Marshal(msg)
        if err != nil {
            log.Println("Error serializing message:", err)
            continue
        }

        // Broadcast the JSON message to all clients
        c.hub.broadcast <- jsonMessage
    }
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// serveWs handles websocket requests from the peer.
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}
	client.hub.register <- client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
