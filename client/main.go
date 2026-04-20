package main

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "distchat/proto/chat"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

const chunkSize = 32 * 1024 // 32 KB

func dialServer(addr string, timeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
}

func reconnect(primaryAddr, backupAddr string, maxRetries int) (*grpc.ClientConn, string, error) {
	wait := time.Second
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("⏳ Reconnecting to primary... attempt %d/%d (waiting %s)\n", attempt, maxRetries, wait)
		time.Sleep(wait)
		conn, err := dialServer(primaryAddr, 5*time.Second)
		if err == nil {
			fmt.Println("✅ Reconnected to primary!")
			return conn, primaryAddr, nil
		}
		fmt.Printf("   Primary still unreachable: %v\n", err)
		wait *= 2
		if wait > 16*time.Second {
			wait = 16 * time.Second
		}
	}

	if backupAddr != "" {
		fmt.Printf("🔀 Primary unreachable after %d attempts — trying backup at %s...\n", maxRetries, backupAddr)
		conn, err := dialServer(backupAddr, 5*time.Second)
		if err == nil {
			fmt.Printf("✅ Connected to backup server at %s!\n", backupAddr)
			return conn, backupAddr, nil
		}
		fmt.Printf("   Backup also unreachable: %v\n", err)
	}
	return nil, "", fmt.Errorf("could not connect to primary or backup")
}

func joinRoom(ctx context.Context, client pb.ChatServiceClient, username, room string) (pb.ChatService_JoinRoomClient, error) {
	stream, err := client.JoinRoom(ctx)
	if err != nil {
		return nil, fmt.Errorf("JoinRoom failed: %w", err)
	}
	if err := stream.Send(&pb.ChatMessage{
		Sender:    username,
		Room:      room,
		Content:   "",
		Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		return nil, fmt.Errorf("failed to send join message: %w", err)
	}
	return stream, nil
}

func main() {
	addr := flag.String("addr", "localhost:50051", "primary server address")
	backup := flag.String("backup", "", "backup server address (e.g. localhost:50052)")
	username := flag.String("user", "", "your username (required)")
	room := flag.String("room", "general", "room to join")
	flag.Parse()

	if *username == "" {
		log.Fatal("--user is required")
	}

	fmt.Printf("🔌 Connecting to primary at %s...\n", *addr)
	if *backup != "" {
		fmt.Printf("🔗 Backup server configured at %s\n", *backup)
	}

	conn, err := dialServer(*addr, 5*time.Second)
	if err != nil {
		log.Fatalf("cannot connect to %s: %v", *addr, err)
	}
	defer conn.Close()

	currentAddr := *addr
	chatClient := pb.NewChatServiceClient(conn)
	fileClient := pb.NewFileServiceClient(conn)
	healthClient := pb.NewHealthServiceClient(conn)

	fmt.Printf("✅ Connected to distchat server at %s\n", currentAddr)
	fmt.Printf("👤 Username: %s  |  Room: %s\n", *username, *room)
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println("Commands: /users /upload /download /files /status /quit")
	fmt.Println("─────────────────────────────────────────────────────")

	// inputCh carries lines typed by the user — shared across reconnections
	inputCh := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Print("> ")
			if !scanner.Scan() {
				close(inputCh)
				return
			}
			inputCh <- scanner.Text()
		}
	}()

	for {
		ctx, cancel := context.WithCancel(context.Background())

		stream, err := joinRoom(ctx, chatClient, *username, *room)
		if err != nil {
			cancel()
			fmt.Printf("❌ Could not join room: %v\n", err)
			conn.Close()
			newConn, newAddr, err := reconnect(*addr, *backup, 6)
			if err != nil {
				log.Fatalf("gave up reconnecting: %v", err)
			}
			conn = newConn
			currentAddr = newAddr
			chatClient = pb.NewChatServiceClient(conn)
			fileClient = pb.NewFileServiceClient(conn)
			healthClient = pb.NewHealthServiceClient(conn)
			continue
		}

		// disconnected fires when the server drops us
		disconnected := make(chan struct{})
		go func() {
			defer close(disconnected)
			for {
				msg, err := stream.Recv()
				if err != nil {
					fmt.Println("\n⚠️  Disconnected from server.")
					cancel()
					return
				}
				t := time.UnixMilli(msg.Timestamp).Format("15:04:05")
				fmt.Printf("\r[%s] %s: %s\n> ", t, msg.Sender, msg.Content)
			}
		}()

		// input loop — exits when disconnected fires
		sessionDone := false
		for !sessionDone {
			select {
			case <-disconnected:
				sessionDone = true

			case line, ok := <-inputCh:
				if !ok {
					// stdin closed
					cancel()
					return
				}
				line = strings.TrimSpace(line)
				if line == "" {
					fmt.Print("> ")
					continue
				}

				// Check again if we got disconnected while typing
				select {
				case <-disconnected:
					sessionDone = true
					continue
				default:
				}

				parts := strings.Fields(line)
				cmd := parts[0]

				switch cmd {
				case "/quit":
					fmt.Println("Goodbye!")
					cancel()
					return

				case "/users":
					resp, err := chatClient.ListUsers(ctx, &pb.Empty{})
					if err != nil {
						fmt.Printf("error: %v\n", err)
					} else {
						fmt.Printf("Online users (%d): %s\n", len(resp.Usernames), strings.Join(resp.Usernames, ", "))
					}

				case "/files":
					resp, err := fileClient.ListFiles(ctx, &pb.Empty{})
					if err != nil {
						fmt.Printf("error: %v\n", err)
					} else if len(resp.Files) == 0 {
						fmt.Println("No files on server.")
					} else {
						fmt.Printf("%-30s %10s  %s\n", "Filename", "Size", "Uploaded")
						for _, f := range resp.Files {
							t := time.UnixMilli(f.UploadedAt).Format("2006-01-02 15:04")
							fmt.Printf("%-30s %10s  %s\n", f.Filename, humanBytes(f.SizeBytes), t)
						}
					}

				case "/upload":
					if len(parts) < 2 {
						fmt.Println("Usage: /upload <file-path>")
					} else if err := uploadFile(ctx, fileClient, parts[1], *username); err != nil {
						fmt.Printf("upload failed: %v\n", err)
					}

				case "/download":
					if len(parts) < 2 {
						fmt.Println("Usage: /download <filename>")
					} else if err := downloadFile(ctx, fileClient, parts[1]); err != nil {
						fmt.Printf("download failed: %v\n", err)
					}

				case "/status":
					resp, err := healthClient.GetStatus(ctx, &pb.Empty{})
					if err != nil {
						fmt.Printf("error: %v\n", err)
					} else {
						fmt.Printf("Role: %s | Users: %d | Files: %d | Uptime: %s\n",
							resp.Role, resp.ConnectedUsers, resp.StoredFiles, resp.Uptime)
					}

				default:
					if err := stream.Send(&pb.ChatMessage{
						Sender:    *username,
						Room:      *room,
						Content:   line,
						Timestamp: time.Now().UnixMilli(),
					}); err != nil {
						fmt.Printf("⚠️  Send failed: %v\n", err)
					}
				}
			}
		}

		// Session ended — reconnect
		cancel()
		fmt.Printf("🔄 Reconnecting (was on %s)...\n", currentAddr)
		conn.Close()
		newConn, newAddr, err := reconnect(*addr, *backup, 6)
		if err != nil {
			log.Fatalf("gave up reconnecting: %v", err)
		}
		conn = newConn
		currentAddr = newAddr
		chatClient = pb.NewChatServiceClient(conn)
		fileClient = pb.NewFileServiceClient(conn)
		healthClient = pb.NewHealthServiceClient(conn)
		fmt.Printf("🔄 Rejoining room %q as %q on %s...\n", *room, *username, currentAddr)
	}
}

func uploadFile(ctx context.Context, client pb.FileServiceClient, path string, uploader string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()
	info, _ := f.Stat()
	filename := filepath.Base(path)
	fmt.Printf("Uploading %s (%s)...\n", filename, humanBytes(info.Size()))
	hasher := md5.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return err
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))
	f.Seek(0, io.SeekStart)
	stream, err := client.UploadFile(ctx)
	if err != nil {
		return err
	}
	buf := make([]byte, chunkSize)
	var chunkIdx, totalSent int64
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			totalSent += int64(n)
			isLast := readErr == io.EOF || totalSent >= info.Size()
			chunk := &pb.FileChunk{Filename: filename, Content: buf[:n], ChunkIndex: chunkIdx, IsLast: isLast}
			if isLast {
				chunk.Checksum = checksum
			}
			if sendErr := stream.Send(chunk); sendErr != nil {
				return fmt.Errorf("send error: %w", sendErr)
			}
			chunkIdx++
			fmt.Printf("\r  Progress: %.1f%% (%s / %s)   ", float64(totalSent)/float64(info.Size())*100, humanBytes(totalSent), humanBytes(info.Size()))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}
	fmt.Printf("\n✅ %s\n", resp.Message)
	return nil
}

func downloadFile(ctx context.Context, client pb.FileServiceClient, filename string) error {
	fmt.Printf("Downloading %s...\n", filename)
	stream, err := client.DownloadFile(ctx, &pb.DownloadRequest{Filename: filename})
	if err != nil {
		return err
	}
	outPath := filepath.Join(".", "downloads", filename)
	os.MkdirAll("downloads", 0755)
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	hasher := md5.New()
	var totalBytes int64
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			os.Remove(outPath)
			return fmt.Errorf("recv error: %w", err)
		}
		n, _ := f.Write(chunk.Content)
		totalBytes += int64(n)
		hasher.Write(chunk.Content)
		fmt.Printf("\r  Received: %s   ", humanBytes(totalBytes))
		if chunk.IsLast && chunk.Checksum != "" {
			if hex.EncodeToString(hasher.Sum(nil)) != chunk.Checksum {
				os.Remove(outPath)
				return fmt.Errorf("checksum mismatch — file may be corrupted")
			}
		}
	}
	fmt.Printf("\n✅ Saved to %s (%s)\n", outPath, humanBytes(totalBytes))
	return nil
}

func humanBytes(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
