package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sync"
	"time"

	pb "distchat/proto/chat"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type result struct {
	clientID  int
	messages  int
	errors    int
	latencies []time.Duration
}

func runClient(id int, addr, room string, msgCount int, wg *sync.WaitGroup, results chan<- result) {
	defer wg.Done()

	res := result{clientID: id}

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("[client %d] dial failed: %v", id, err)
		res.errors++
		results <- res
		return
	}
	defer conn.Close()

	client := pb.NewChatServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stream, err := client.JoinRoom(ctx)
	if err != nil {
		log.Printf("[client %d] JoinRoom failed: %v", id, err)
		res.errors++
		results <- res
		return
	}

	username := fmt.Sprintf("loaduser%d", id)

	if err := stream.Send(&pb.ChatMessage{
		Sender:    username,
		Room:      room,
		Content:   "",
		Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		res.errors++
		results <- res
		return
	}

	// Drain incoming messages in background
	go func() {
		for {
			_, err := stream.Recv()
			if err != nil {
				return
			}
		}
	}()

	// Send messages and measure latency
	for i := 0; i < msgCount; i++ {
		msg := fmt.Sprintf("load test message %d from client %d", i, id)
		start := time.Now()
		err := stream.Send(&pb.ChatMessage{
			Sender:    username,
			Room:      room,
			Content:   msg,
			Timestamp: time.Now().UnixMilli(),
		})
		latency := time.Since(start)
		if err != nil {
			res.errors++
		} else {
			res.messages++
			res.latencies = append(res.latencies, latency)
		}
		time.Sleep(50 * time.Millisecond)
	}

	stream.CloseSend()
	results <- res
}

func main() {
	addr := flag.String("addr", "localhost:50051", "server address")
	clients := flag.Int("clients", 10, "number of concurrent clients")
	messages := flag.Int("messages", 20, "messages per client")
	room := flag.String("room", "loadtest", "room name")
	flag.Parse()

	fmt.Printf("🚀 Load test: %d clients × %d messages = %d total messages\n", *clients, *messages, *clients**messages)
	fmt.Printf("   Server: %s | Room: %s\n\n", *addr, *room)

	results := make(chan result, *clients)
	var wg sync.WaitGroup

	start := time.Now()

	for i := 1; i <= *clients; i++ {
		wg.Add(1)
		go runClient(i, *addr, *room, *messages, &wg, results)
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()
	close(results)

	totalDuration := time.Since(start)

	var totalMessages, totalErrors int
	var allLatencies []time.Duration

	for res := range results {
		totalMessages += res.messages
		totalErrors += res.errors
		allLatencies = append(allLatencies, res.latencies...)
	}

	if len(allLatencies) == 0 {
		fmt.Println("No latency data collected.")
		return
	}

	var totalLatency time.Duration
	minL := allLatencies[0]
	maxL := allLatencies[0]
	for _, l := range allLatencies {
		totalLatency += l
		if l < minL {
			minL = l
		}
		if l > maxL {
			maxL = l
		}
	}
	avgLatency := totalLatency / time.Duration(len(allLatencies))
	throughput := float64(totalMessages) / totalDuration.Seconds()

	fmt.Println("─────────────────────────────────────────")
	fmt.Println("           LOAD TEST RESULTS")
	fmt.Println("─────────────────────────────────────────")
	fmt.Printf("Clients:            %d\n", *clients)
	fmt.Printf("Messages sent:      %d / %d\n", totalMessages, *clients**messages)
	fmt.Printf("Errors:             %d\n", totalErrors)
	fmt.Printf("Total duration:     %s\n", totalDuration.Round(time.Millisecond))
	fmt.Printf("Throughput:         %.1f msg/sec\n", throughput)
	fmt.Printf("Avg send latency:   %s\n", avgLatency)
	fmt.Printf("Min send latency:   %s\n", minL)
	fmt.Printf("Max send latency:   %s\n", maxL)
	fmt.Println("─────────────────────────────────────────")
}
