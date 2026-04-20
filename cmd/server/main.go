package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"time"

	"distchat/server"

	pb "distchat/proto/chat"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

func main() {
	port := flag.Int("port", 50051, "gRPC server port")
	role := flag.String("role", "primary", "server role: primary | backup")
	peer := flag.String("peer", "", "peer server address for heartbeat monitoring")
	flag.Parse()

	addr := fmt.Sprintf(":%d", *port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(64*1024*1024),
		grpc.MaxSendMsgSize(64*1024*1024),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)

	chatSvc   := server.NewChatServer()
	fileSvc   := server.NewFileServer(*port)
	healthSvc := server.NewHealthServer(*role, chatSvc, fileSvc)

	// Primary replicates uploaded files to the backup
	if *role == "primary" && *peer != "" {
		fileSvc.SetBackup(*peer)
	}

	pb.RegisterChatServiceServer(grpcServer, chatSvc)
	pb.RegisterFileServiceServer(grpcServer, fileSvc)
	pb.RegisterHealthServiceServer(grpcServer, healthSvc)

	reflection.Register(grpcServer)

	// Start heartbeat monitor
	if *peer != "" {
		if *role == "backup" {
			healthSvc.StartHeartbeatMonitor(*peer, 2*time.Second, 3, func() {
				healthSvc.PromoteSelf()
			})
		} else {
			healthSvc.StartHeartbeatMonitor(*peer, 2*time.Second, 3, func() {
				log.Printf("[HEALTH] backup peer is down — continuing as sole primary")
			})
		}
	}

	log.Printf("🚀 distchat server [%s] listening on %s", *role, addr)
	if *peer != "" {
		log.Printf("🔗 monitoring peer at %s", *peer)
	}

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
