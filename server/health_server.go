package server

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	pb "distchat/proto/chat"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// HealthServer implements pb.HealthServiceServer.
type HealthServer struct {
	pb.UnimplementedHealthServiceServer

	role       string // "primary" or "backup"
	startTime  time.Time
	chatServer *ChatServer
	fileServer *FileServer

	// Failover state
	promoted int32 // atomic: 1 = this backup has promoted itself to primary
}

func NewHealthServer(role string, chat *ChatServer, file *FileServer) *HealthServer {
	return &HealthServer{
		role:       role,
		startTime:  time.Now(),
		chatServer: chat,
		fileServer: file,
	}
}

// Ping handles heartbeat pings from the peer server.
func (h *HealthServer) Ping(_ context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	return &pb.PingResponse{
		Alive:     true,
		Timestamp: time.Now().UnixMilli(),
	}, nil
}

// GetStatus returns live server metrics.
func (h *HealthServer) GetStatus(_ context.Context, _ *pb.Empty) (*pb.ServerStatus, error) {
	h.chatServer.mu.RLock()
	connectedUsers := int32(len(h.chatServer.clients))
	h.chatServer.mu.RUnlock()

	h.fileServer.mu.RLock()
	storedFiles := int32(len(h.fileServer.files))
	h.fileServer.mu.RUnlock()

	role := h.role
	if atomic.LoadInt32(&h.promoted) == 1 {
		role = "primary (promoted)"
	}

	return &pb.ServerStatus{
		Role:           role,
		ConnectedUsers: connectedUsers,
		StoredFiles:    storedFiles,
		Uptime:         time.Since(h.startTime).Round(time.Second).String(),
	}, nil
}

// IsPromoted returns true if this backup has taken over as primary.
func (h *HealthServer) IsPromoted() bool {
	return atomic.LoadInt32(&h.promoted) == 1
}

// StartHeartbeatMonitor begins pinging the peer server.
// - interval: how often to ping
// - maxMisses: consecutive missed pings before action is taken
// - onPeerDown: called once when peer is declared down
func (h *HealthServer) StartHeartbeatMonitor(peerAddr string, interval time.Duration, maxMisses int, onPeerDown func()) {
	go func() {
		log.Printf("[HEALTH] starting heartbeat monitor → %s (every %s, max misses: %d)", peerAddr, interval, maxMisses)

		conn, err := grpc.Dial(peerAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			log.Printf("[HEALTH] could not dial peer %s: %v", peerAddr, err)
		}
		defer conn.Close()

		client := pb.NewHealthServiceClient(conn)
		misses := 0

		for {
			time.Sleep(interval)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := client.Ping(ctx, &pb.PingRequest{
				From:      h.role,
				Timestamp: time.Now().UnixMilli(),
			})
			cancel()

			if err != nil {
				misses++
				log.Printf("[HEALTH] ping to %s failed (%d/%d): %v", peerAddr, misses, maxMisses, err)

				if misses >= maxMisses {
					log.Printf("[HEALTH] ⚠️  peer %s declared DOWN after %d missed pings", peerAddr, misses)
					onPeerDown()
					return // stop monitoring after action taken
				}
			} else {
				if misses > 0 {
					log.Printf("[HEALTH] peer %s is back up (misses reset)", peerAddr)
				}
				misses = 0
			}
		}
	}()
}

// PromoteSelf is called on the backup when it detects the primary is down.
func (h *HealthServer) PromoteSelf() {
	if atomic.CompareAndSwapInt32(&h.promoted, 0, 1) {
		log.Printf("[HEALTH] 🚨 PRIMARY IS DOWN — this backup is now promoting itself to PRIMARY")
		log.Printf("[HEALTH] ✅ Backup promoted. Now accepting client connections as primary.")
	}
}
