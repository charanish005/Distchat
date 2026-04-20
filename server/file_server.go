package server

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	pb "distchat/proto/chat"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const chunkSize = 32 * 1024

var storageDirPath string

func getStorageDir() string {
	if storageDirPath != "" {
		return storageDirPath
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "./uploads"
	}
	return filepath.Join(cwd, "uploads")
}

type fileMeta struct {
	Filename   string
	SizeBytes  int64
	UploadedBy string
	UploadedAt int64
}

type FileServer struct {
	pb.UnimplementedFileServiceServer
	mu         sync.RWMutex
	files      map[string]*fileMeta
	backupAddr string
}

func NewFileServer(port int) *FileServer {
	cwd, _ := os.Getwd()
	storageDirPath = filepath.Join(cwd, fmt.Sprintf("uploads_%d", port))
	dir := getStorageDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("cannot create storage dir: %v", err)
	}
	log.Printf("[FILE] storage dir: %s", dir)
	return &FileServer{files: make(map[string]*fileMeta)}
}

func (s *FileServer) SetBackup(addr string) {
	s.backupAddr = addr
	log.Printf("[FILE] replication target set → %s", addr)
}

func (s *FileServer) UploadFile(stream pb.FileService_UploadFileServer) error {
	var (
		filename   string
		totalBytes int64
		hasher     = md5.New()
		tmpPath    string
		f          *os.File
		finalized  bool
	)

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if f != nil {
				f.Close()
				os.Remove(tmpPath)
			}
			return fmt.Errorf("receive error: %w", err)
		}

		// First chunk — open the temp file
		if filename == "" {
			filename = filepath.Base(chunk.Filename)
			tmpPath = filepath.Join(getStorageDir(), filename+".tmp")
			f, err = os.Create(tmpPath)
			if err != nil {
				return fmt.Errorf("cannot create temp file: %w", err)
			}
			log.Printf("[FILE] receiving: %s", filename)
		}

		// Write chunk
		n, werr := f.Write(chunk.Content)
		if werr != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write error: %w", werr)
		}
		totalBytes += int64(n)
		hasher.Write(chunk.Content)

		// Last chunk — close, verify, rename
		if chunk.IsLast {
			f.Close()
			computed := hex.EncodeToString(hasher.Sum(nil))
			if chunk.Checksum != "" && computed != chunk.Checksum {
				os.Remove(tmpPath)
				return fmt.Errorf("checksum mismatch")
			}
			finalPath := filepath.Join(getStorageDir(), filename)
			if rerr := os.Rename(tmpPath, finalPath); rerr != nil {
				return fmt.Errorf("rename failed: %w", rerr)
			}
			s.mu.Lock()
			s.files[filename] = &fileMeta{
				Filename:   filename,
				SizeBytes:  totalBytes,
				UploadedBy: "client",
				UploadedAt: time.Now().UnixMilli(),
			}
			s.mu.Unlock()
			log.Printf("[FILE] ✅ upload complete: %s (%d bytes)", filename, totalBytes)
			finalized = true
			if s.backupAddr != "" {
				go func(fn string) {
					time.Sleep(500 * time.Millisecond)
					s.replicateToBackup(fn)
				}(filename)
			}
			break
		}
	}

	if !finalized && f != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("upload incomplete — no final chunk received")
	}

	return stream.SendAndClose(&pb.UploadResponse{
		Success:    true,
		Message:    fmt.Sprintf("uploaded %s successfully", filename),
		BytesSaved: totalBytes,
	})
}

func (s *FileServer) ReplicateFile(stream pb.FileService_ReplicateFileServer) error {
	var (
		filename   string
		totalBytes int64
		hasher     = md5.New()
		tmpPath    string
		f          *os.File
	)

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if f != nil {
				f.Close()
				os.Remove(tmpPath)
			}
			return fmt.Errorf("receive error: %w", err)
		}

		if filename == "" {
			filename = filepath.Base(chunk.Filename)
			tmpPath = filepath.Join(getStorageDir(), filename+".tmp")
			f, err = os.Create(tmpPath)
			if err != nil {
				return fmt.Errorf("cannot create temp file: %w", err)
			}
			log.Printf("[FILE] replicating: %s", filename)
		}

		n, werr := f.Write(chunk.Content)
		if werr != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write error: %w", werr)
		}
		totalBytes += int64(n)
		hasher.Write(chunk.Content)

		if chunk.IsLast {
			f.Close()
			computed := hex.EncodeToString(hasher.Sum(nil))
			if chunk.Checksum != "" && computed != chunk.Checksum {
				os.Remove(tmpPath)
				return fmt.Errorf("checksum mismatch")
			}
			finalPath := filepath.Join(getStorageDir(), filename)
			os.Remove(finalPath) // remove existing file if any (Windows won't overwrite)
			if rerr := os.Rename(tmpPath, finalPath); rerr != nil {
				return fmt.Errorf("rename failed: %w", rerr)
			}
			s.mu.Lock()
			s.files[filename] = &fileMeta{
				Filename:   filename,
				SizeBytes:  totalBytes,
				UploadedBy: "replicated",
				UploadedAt: time.Now().UnixMilli(),
			}
			s.mu.Unlock()
			log.Printf("[FILE] ✅ replicated: %s (%d bytes)", filename, totalBytes)
			break
		}
	}

	return stream.SendAndClose(&pb.UploadResponse{
		Success:    true,
		Message:    fmt.Sprintf("replicated %s", filename),
		BytesSaved: totalBytes,
	})
}

func (s *FileServer) replicateToBackup(filename string) {
	path := filepath.Join(getStorageDir(), filename)
	log.Printf("[FILE] replicating %s to %s", filename, s.backupAddr)

	conn, err := grpc.Dial(s.backupAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("[FILE] dial failed: %v", err)
		return
	}
	defer conn.Close()

	client := pb.NewFileServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := client.ReplicateFile(ctx)
	if err != nil {
		log.Printf("[FILE] stream failed: %v", err)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		log.Printf("[FILE] open failed: %v", err)
		return
	}
	defer f.Close()

	hasher := md5.New()
	io.Copy(hasher, f)
	checksum := hex.EncodeToString(hasher.Sum(nil))
	f.Seek(0, io.SeekStart)

	info, _ := f.Stat()
	buf := make([]byte, chunkSize)
	var chunkIdx, totalSent int64

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			totalSent += int64(n)
			isLast := totalSent >= info.Size()
			chunk := &pb.FileChunk{
				Filename:   filename,
				Content:    buf[:n],
				ChunkIndex: chunkIdx,
				IsLast:     isLast,
			}
			if isLast {
				chunk.Checksum = checksum
			}
			if serr := stream.Send(chunk); serr != nil {
				log.Printf("[FILE] send error: %v", serr)
				return
			}
			chunkIdx++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			log.Printf("[FILE] read error: %v", readErr)
			return
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		log.Printf("[FILE] close error: %v", err)
		return
	}
	log.Printf("[FILE] ✅ replicated to backup: %s", resp.Message)
}

func (s *FileServer) DownloadFile(req *pb.DownloadRequest, stream pb.FileService_DownloadFileServer) error {
	filename := filepath.Base(req.Filename)
	path := filepath.Join(getStorageDir(), filename)

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("file not found: %s", filename)
	}
	defer f.Close()

	info, _ := f.Stat()
	hasher := md5.New()
	io.Copy(hasher, f)
	checksum := hex.EncodeToString(hasher.Sum(nil))
	f.Seek(0, io.SeekStart)

	buf := make([]byte, chunkSize)
	var chunkIdx, totalSent int64

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			totalSent += int64(n)
			isLast := readErr == io.EOF || totalSent >= info.Size()
			chunk := &pb.FileChunk{
				Filename:   filename,
				Content:    buf[:n],
				ChunkIndex: chunkIdx,
				IsLast:     isLast,
			}
			if isLast {
				chunk.Checksum = checksum
			}
			if serr := stream.Send(chunk); serr != nil {
				return fmt.Errorf("send error: %w", serr)
			}
			chunkIdx++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read error: %w", readErr)
		}
	}
	log.Printf("[FILE] download complete: %s", filename)
	return nil
}

func (s *FileServer) ListFiles(_ context.Context, _ *pb.Empty) (*pb.FileList, error) {
	s.mu.Lock()
	entries, _ := os.ReadDir(getStorageDir())
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) != ".tmp" {
			if _, exists := s.files[e.Name()]; !exists {
				info, _ := e.Info()
				s.files[e.Name()] = &fileMeta{
					Filename:   e.Name(),
					SizeBytes:  info.Size(),
					UploadedBy: "replicated",
					UploadedAt: info.ModTime().UnixMilli(),
				}
			}
		}
	}
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	var list []*pb.FileInfo
	for _, m := range s.files {
		list = append(list, &pb.FileInfo{
			Filename:   m.Filename,
			SizeBytes:  m.SizeBytes,
			UploadedBy: m.UploadedBy,
			UploadedAt: m.UploadedAt,
		})
	}
	return &pb.FileList{Files: list}, nil
}
