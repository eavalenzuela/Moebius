package ipc

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
)

// Server listens on a platform-specific IPC endpoint (Unix socket or named
// pipe) and dispatches incoming JSON-RPC requests via a Router.
type Server struct {
	path     string
	router   *Router
	log      *slog.Logger
	listener net.Listener

	mu   sync.Mutex
	done chan struct{}
}

// NewServer creates a Server that will listen on the given socket/pipe path.
func NewServer(path string, router *Router, log *slog.Logger) *Server {
	return &Server{
		path:   path,
		router: router,
		log:    log,
		done:   make(chan struct{}),
	}
}

// Serve creates the platform-specific listener and accepts connections until
// ctx is cancelled. It blocks until shutdown is complete.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := createListener(s.path)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	s.log.Info("IPC server listening", slog.String("path", s.path))

	var wg sync.WaitGroup

	// Close listener when context is cancelled.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Expected when listener is closed during shutdown.
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			s.log.Error("IPC accept error", slog.String("error", err.Error()))
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleConn(ctx, conn)
		}()
	}

	wg.Wait()
	close(s.done)
	s.log.Info("IPC server stopped")
	return nil
}

// Done returns a channel that is closed when the server has fully stopped.
func (s *Server) Done() <-chan struct{} {
	return s.done
}

// handleConn reads requests from a single connection, dispatches them, and
// writes responses. The connection is closed when the client disconnects or
// the context is cancelled.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Close the connection when context is cancelled to unblock reads.
	go func() {
		<-connCtx.Done()
		_ = conn.Close()
	}()

	scanner := bufio.NewScanner(conn)
	// Allow messages up to 1 MB (generous for local IPC).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		req, err := ReadRequest(scanner)
		if err != nil {
			if err != io.EOF && connCtx.Err() == nil {
				s.log.Debug("IPC read error", slog.String("error", err.Error()))
			}
			return
		}

		resp := s.router.Dispatch(connCtx, req)

		if err := WriteMessage(conn, resp); err != nil {
			if connCtx.Err() == nil {
				s.log.Debug("IPC write error", slog.String("error", err.Error()))
			}
			return
		}
	}
}
