# distchat — Distributed Chat & File Transfer System

**COEN 6731 · Distributed Systems · Winter 2026**  
**Charanish Miriyala · Student ID: 40307568**

---

## Overview

distchat is a distributed real-time chat and file transfer system built in **Go** using **gRPC**. It demonstrates core distributed systems concepts including bidirectional streaming, concurrent goroutine fan-out, chunked file transfer with MD5 integrity verification, primary/backup replication, heartbeat-based failure detection, and automatic failover with client-side exponential backoff reconnection.

---

## Project Structure

```
distchat/
├── proto/
│   └── chat.proto              ← gRPC service definitions (Chat, File, Health)
├── proto/chat/                 ← generated Go stubs (after running protoc)
│   ├── chat.pb.go
│   └── chat_grpc.pb.go
├── server/
│   ├── main.go                 ← server entry point, gRPC registration
│   ├── chat_server.go          ← ChatService: bidirectional streaming + fan-out
│   ├── file_server.go          ← FileService: chunked upload/download + MD5
│   └── health_server.go        ← HealthService: ping + status + promotion
├── client/
│   └── main.go                 ← interactive CLI client with reconnect logic
├── loadtest/
│   └── main.go                 ← concurrent load test harness
├── install.sh                  ← automated dependency installer (Linux/macOS)
├── Dockerfile                  ← container for reproducible environment
├── docker-compose.yml          ← spins up primary + backup + two clients
├── go.mod
├── go.sum
└── README.md
```

---

## Distributed Systems Concepts Demonstrated

| Concept | Where |
|---|---|
| Bidirectional gRPC streaming | `ChatService.JoinRoom` |
| Goroutine fan-out with buffered channels | `chat_server.go` — sender goroutine per client |
| Thread-safe shared state | `sync.RWMutex` in all three servers |
| Chunked streaming file transfer | `FileService.UploadFile` / `DownloadFile` |
| End-to-end data integrity | MD5 checksum verification on upload and download |
| Atomic file commit | `.tmp` write → checksum verify → `os.Rename` |
| Primary/backup replication | `FileService.ReplicateFile` RPC |
| Heartbeat-based failure detection | `HealthService.StartHeartbeatMonitor` |
| Automatic failover | `PromoteSelf()` via atomic CAS on backup |
| Exponential backoff reconnection | `reconnect()` in `client/main.go` |
| gRPC keepalive | `KeepaliveParams` + `EnforcementPolicy` |

---

## Option A — Run with Docker (Recommended)

This is the fastest and most reproducible way to run distchat. Docker handles all dependencies automatically.

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (v20+)
- [Docker Compose](https://docs.docker.com/compose/install/) (v2+)

### Start the full stack

```bash
git clone https://github.com/charanish-miriyala/distchat.git
cd distchat
docker compose up --build
```

This starts:
- **primary** server on port `50051`
- **backup** server on port `50052`  
- **alice** client connected to primary
- **bob** client connected to primary with backup configured

To open an interactive client session:

```bash
docker compose run --rm client \
  go run ./client --user=carol --room=general --addr=primary:50051 --backup=backup:50052
```

To stop everything:

```bash
docker compose down
```

---

## Option B — Automated Install Script (Linux / macOS)

```bash
git clone https://github.com/charanish-miriyala/distchat.git
cd distchat
chmod +x install.sh
./install.sh
```

The script installs Go 1.21+, protoc, the Go gRPC plugins, runs `go mod tidy`, and generates the proto stubs automatically. After it completes, skip straight to **Step 4 — Run the Server** below.

---

## Option C — Manual Setup

### Step 1 — Install Go (1.21+)

```bash
# macOS
brew install go

# Ubuntu / Debian
sudo apt update && sudo apt install -y golang-go

# Windows — download installer from https://go.dev/dl/
```

Verify:

```bash
go version   # should print go1.21 or higher
```

### Step 2 — Install protoc and Go plugins

```bash
# macOS
brew install protobuf

# Ubuntu / Debian
sudo apt install -y protobuf-compiler

# Windows — download from https://github.com/protocolbuffers/protobuf/releases
```

Install the Go code-generation plugins:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Add Go bin directory to PATH
export PATH="$PATH:$(go env GOPATH)/bin"
# Make it permanent — add the line above to your ~/.bashrc or ~/.zshrc
```

### Step 3 — Clone and install dependencies

```bash
git clone https://github.com/charanish-miriyala/distchat.git
cd distchat
go mod tidy
```

### Step 4 — Generate gRPC stubs from the proto file

Run this once from the repo root (the directory containing `go.mod`):

```bash
mkdir -p proto/chat

protoc \
  --go_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_out=. \
  --go-grpc_opt=paths=source_relative \
  proto/chat.proto
```

This generates two files inside `proto/chat/`:
- `chat.pb.go` — all message struct types
- `chat_grpc.pb.go` — service interfaces and client stubs

---

## Running the System

### Start the Primary Server

```bash
go run ./server --role=primary --port=50051
```

Expected output:
```
🚀 distchat server [primary] listening on :50051
```

### Start the Backup Server (optional, for fault tolerance)

Open a second terminal:

```bash
go run ./server --role=backup --port=50052 --peer=localhost:50051
```

To enable file replication from primary to backup, also pass `--peer` to the primary:

```bash
go run ./server --role=primary --port=50051 --peer=localhost:50052
```

### Connect Clients

Open separate terminals for each client:

```bash
# Terminal A
go run ./client --user=alice --room=general

# Terminal B
go run ./client --user=bob --room=general

# With backup failover configured
go run ./client --user=carol --room=general --addr=localhost:50051 --backup=localhost:50052
```

---

## Client Commands

Once inside the client prompt (`>`), the following commands are available:

| Command | Description |
|---|---|
| `hello everyone!` | Send a chat message to everyone in the room |
| `/users` | List all currently connected users |
| `/upload ./myfile.pdf` | Upload a local file to the server |
| `/download myfile.pdf` | Download a file from the server |
| `/files` | List all files stored on the server |
| `/status` | Show server role, uptime, connected users, stored files |
| `/quit` | Disconnect and exit |

---

## Quick Demo (copy-paste walkthrough)

**Terminal 1 — start primary server:**
```bash
go run ./server --role=primary --port=50051
```

**Terminal 2 — start backup server:**
```bash
go run ./server --role=backup --port=50052 --peer=localhost:50051
```

**Terminal 3 — Alice joins:**
```bash
go run ./client --user=alice --room=general --backup=localhost:50052
> Hey Bob, check this out!
> /upload ./report.pdf
> /files
```

**Terminal 4 — Bob joins:**
```bash
go run ./client --user=bob --room=general --backup=localhost:50052
> Got it Alice!
> /download report.pdf
> /status
```

**Failover test — kill the primary (Ctrl+C in Terminal 1):**  
The backup promotes itself within 6 seconds. Both clients automatically reconnect.

---

## Running the Load Test

The load test harness spawns N concurrent clients and measures throughput and latency:

```bash
# 50 clients, 20 messages each
go run ./loadtest --clients=50 --messages=20 --addr=localhost:50051 --room=loadtest
```

Output example:
```
🚀 Load test: 50 clients × 20 messages = 1000 total messages
   Server: localhost:50051 | Room: loadtest

─────────────────────────────────────────
           LOAD TEST RESULTS
─────────────────────────────────────────
Clients:            50
Messages sent:      1000 / 1000
Errors:             0
Total duration:     1.61s
Throughput:         621.1 msg/sec
Avg send latency:   540µs
Min send latency:   81µs
Max send latency:   4.2ms
─────────────────────────────────────────
```

---

## Server Flags Reference

| Flag | Default | Description |
|---|---|---|
| `--port` | `50051` | Port to listen on |
| `--role` | `primary` | Server role: `primary` or `backup` |
| `--peer` | _(none)_ | Peer address for heartbeat + replication |

## Client Flags Reference

| Flag | Default | Description |
|---|---|---|
| `--addr` | `localhost:50051` | Primary server address |
| `--backup` | _(none)_ | Backup server address for automatic failover |
| `--user` | _(required)_ | Username — must be unique per session |
| `--room` | `general` | Chat room to join |

---

## Troubleshooting

**`protoc-gen-go: program not found`**
```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

**`connection refused`**  
Make sure the server is running before starting clients.

**`username already taken`**  
Each connected client must use a unique `--user` value.

**Port already in use:**
```bash
go run ./server --port=50053
go run ./client --addr=localhost:50053 --user=alice
```

**Files not showing after failover:**  
Run `/files` again after reconnecting — the promoted backup reconciles its file index on first query.

---

## Dependencies

| Dependency | Version |
|---|---|
| Go | 1.21+ |
| google.golang.org/grpc | v1.79.2 |
| google.golang.org/protobuf | v1.36.10 |
| protoc (Protocol Buffer compiler) | v3+ |

All Go dependencies are pinned in `go.mod` and `go.sum`.

---

## License

MIT License — see [LICENSE](LICENSE) for details.
