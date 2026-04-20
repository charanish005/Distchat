package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	pb "distchat/proto/chat"
)

// client holds an active streaming connection for one user.
type client struct {
	username string
	room     string
	stream   pb.ChatService_JoinRoomServer
	send     chan *pb.ChatMessage // buffered channel for outbound messages
	done     chan struct{}
}

// ChatServer implements pb.ChatServiceServer.
type ChatServer struct {
	pb.UnimplementedChatServiceServer

	mu      sync.RWMutex
	clients map[string]*client // key: username
}

func NewChatServer() *ChatServer {
	return &ChatServer{
		clients: make(map[string]*client),
	}
}

// JoinRoom is a bidirectional stream.
// The first message sent by the client must contain the username and room.
// After that, every message is broadcast to all other clients in the same room.
func (s *ChatServer) JoinRoom(stream pb.ChatService_JoinRoomServer) error {
	// ── Step 1: Read the first message to get user identity ──────────────
	firstMsg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to read first message: %w", err)
	}

	username := firstMsg.Sender
	room := firstMsg.Room
	if username == "" || room == "" {
		return fmt.Errorf("first message must include sender and room")
	}

	// ── Step 2: Register the client ──────────────────────────────────────
	c := &client{
		username: username,
		room:     room,
		stream:   stream,
		send:     make(chan *pb.ChatMessage, 64),
		done:     make(chan struct{}),
	}

	s.mu.Lock()
	if _, exists := s.clients[username]; exists {
		s.mu.Unlock()
		return fmt.Errorf("username %q is already taken", username)
	}
	s.clients[username] = c
	s.mu.Unlock()

	log.Printf("[CHAT] %s joined room %q", username, room)

	// Announce join to everyone in the room
	s.broadcast(&pb.ChatMessage{
		Sender:    "SERVER",
		Room:      room,
		Content:   fmt.Sprintf("🟢 %s has joined the room", username),
		Timestamp: nowMillis(),
	}, username)

	// Also broadcast the first message if it has content
	if firstMsg.Content != "" {
		firstMsg.Timestamp = nowMillis()
		s.broadcast(firstMsg, "")
	}

	// ── Step 3: Start sender goroutine (server → client) ─────────────────
	go func() {
		for {
			select {
			case msg := <-c.send:
				if err := stream.Send(msg); err != nil {
					log.Printf("[CHAT] send error to %s: %v", username, err)
					close(c.done)
					return
				}
			case <-c.done:
				return
			case <-stream.Context().Done():
				close(c.done)
				return
			}
		}
	}()

	// ── Step 4: Receive loop (client → server → broadcast) ───────────────
	for {
		msg, err := stream.Recv()
		if err == io.EOF || stream.Context().Err() != nil {
			break
		}
		if err != nil {
			log.Printf("[CHAT] recv error from %s: %v", username, err)
			break
		}
		msg.Timestamp = nowMillis()
		log.Printf("[CHAT] [%s] %s: %s", room, username, msg.Content)
		s.broadcast(msg, "") // "" means broadcast to everyone including sender echo
	}

	// ── Step 5: Cleanup ──────────────────────────────────────────────────
	s.removeClient(username)
	s.broadcast(&pb.ChatMessage{
		Sender:    "SERVER",
		Room:      room,
		Content:   fmt.Sprintf("🔴 %s has left the room", username),
		Timestamp: nowMillis(),
	}, username)

	log.Printf("[CHAT] %s left room %q", username, room)
	return nil
}

// broadcast sends msg to all clients in the same room, skipping the sender
// if excludeSender is non-empty (pass "" to send to everyone).
func (s *ChatServer) broadcast(msg *pb.ChatMessage, excludeSender string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, c := range s.clients {
		if c.room != msg.Room {
			continue
		}
		if c.username == excludeSender {
			continue
		}
		select {
		case c.send <- msg:
		default:
			// Drop if buffer full — client is too slow
			log.Printf("[CHAT] dropped message for slow client %s", c.username)
		}
	}
}

func (s *ChatServer) removeClient(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, username)
}

// ListUsers returns all currently connected usernames.
func (s *ChatServer) ListUsers(_ context.Context, _ *pb.Empty) (*pb.UserList, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]string, 0, len(s.clients))
	for u := range s.clients {
		users = append(users, u)
	}
	return &pb.UserList{Usernames: users}, nil
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
