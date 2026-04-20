# distchat — Distributed Chat & File Transfer System
**COEN 6731 · Winter 2026 · Charanish Miriyala (40307568)**

---

## Project Structure

```
distchat/
├── proto/
│   └── chat.proto          ← gRPC service definitions (Chat, File, Health)
├── proto/chat/             ← generated Go code (after running protoc)
├── server/
│   ├── main.go             ← server entry point
│   ├── chat_server.go      ← ChatService: bidirectional streaming + fan-out
│   ├── file_server.go      ← FileService: chunked upload/download + MD5 check
│   └── health_server.go    ← HealthService: ping + server status
├── client/
│   └── main.go             ← interactive CLI client
├── go.mod
└── README.md
```

---

## 1 — Install Prerequisites

### Go (1.21+)
```bash
# macOS
brew install go

# Ubuntu / Debian
sudo apt update && sudo apt install -y golang-go

# Windows — download from https://go.dev/dl/
```

Verify:
```bash
go version   # should print go1.21 or higher
```

---

### Protocol Buffer compiler + Go plugins

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

# Make sure Go binaries are on your PATH
export PATH="$PATH:$(go env GOPATH)/bin"
# Add this line to your ~/.bashrc or ~/.zshrc to make it permanent
```

---

## 2 — Generate gRPC Code from the Proto File

Run this once from the repo root (where `go.mod` lives):

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
- `chat.pb.go` — message types
- `chat_grpc.pb.go` — service interfaces & client stubs

---

## 3 — Install Go Dependencies

```bash
go mod tidy
```

---

## 4 — Run the Server

```bash
# Primary server on default port 50051
go run ./server --role=primary --port=50051

# (Optional) Backup replica on a different port
go run ./server --role=backup --port=50052
```

You should see:
```
🚀 distchat server [primary] listening on :50051
```

---

## 5 — Run Clients (open multiple terminals)

```bash
# Terminal 1
go run ./client --user=alice --room=general

# Terminal 2
go run ./client --user=bob --room=general

# Connect to a different server address
go run ./client --user=carol --addr=localhost:50052
```

---

## 6 — Available Commands (inside the client)

| Command | Description |
|---------|-------------|
| `hello everyone!` | Send a chat message to the room |
| `/users` | List all connected users |
| `/upload ./myfile.pdf` | Upload a file to the server |
| `/download myfile.pdf` | Download a file from the server |
| `/files` | List all files stored on the server |
| `/status` | Show server role, uptime, connected users, stored files |
| `/quit` | Exit |

---

## 7 — Quick Demo (copy-paste walkthrough)

**Terminal 1 — start server:**
```bash
go run ./server
```

**Terminal 2 — Alice joins:**
```bash
go run ./client --user=alice
> Hey Bob!
> /upload ./report.pdf
> /files
```

**Terminal 3 — Bob joins:**
```bash
go run ./client --user=bob
> Hi Alice!
> /download report.pdf
> /status
```

---

## 8 — Distributed Systems Concepts Demonstrated

| Concept | Where |
|---------|-------|
| Bidirectional gRPC streaming | `ChatService.JoinRoom` |
| Concurrent goroutines (fan-out) | `chat_server.go` — one goroutine per client sender |
| Thread-safe shared state | `sync.RWMutex` in all three servers |
| Chunked streaming file transfer | `FileService.UploadFile` / `DownloadFile` |
| Data integrity via checksum | MD5 verification on both upload and download |
| Health monitoring interface | `HealthService.Ping` / `GetStatus` |
| Multi-role server deployment | `--role=primary` / `--role=backup` flags |

---

## 9 — Troubleshooting

**`protoc-gen-go: program not found`**
```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

**`connection refused`** — make sure the server is running before starting clients.

**`username already taken`** — each client session needs a unique `--user` value.

**Port already in use:**
```bash
go run ./server --port=50052
go run ./client --addr=localhost:50052 --user=alice
```
