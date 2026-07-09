package nfs

import (
	"context"
	"fmt"
	"net"
	"sync"

	nfs "github.com/willscott/go-nfs"
)

// Server wraps the NFS server functionality
type Server struct {
	handler     nfs.Handler
	listener    net.Listener
	logger      *Logger
	nfsVersions []uint32
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewServer creates a new NFS server
func NewServer(handler nfs.Handler, address string, logger *Logger, nfsVersions []uint32) (*Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		handler:     handler,
		listener:    listener,
		logger:      logger,
		nfsVersions: nfsVersions,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// Start begins serving NFS requests
func (s *Server) Start() error {
	s.logger.Info("Starting NFS server",
		"address", s.listener.Addr().String(),
		"versions", s.nfsVersions)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		srv := &nfs.Server{
			Handler:            s.handler,
			EnabledNFSVersions: s.nfsVersions,
		}
		if err := srv.Serve(s.listener); err != nil {
			s.logger.Error("NFS server error", "error", err)
		}
	}()

	return nil
}

// Stop gracefully stops the NFS server
func (s *Server) Stop() error {
	s.logger.Info("Stopping NFS server")

	// Cancel context to signal shutdown
	s.cancel()

	// Close the listener to stop accepting new connections
	if err := s.listener.Close(); err != nil {
		s.logger.Error("Error closing listener", "error", err)
	}

	// Wait for all goroutines to finish
	s.wg.Wait()

	s.logger.Info("NFS server stopped")
	return nil
}

// Address returns the server's listening address
func (s *Server) Address() string {
	return s.listener.Addr().String()
}

// Made with Bob
