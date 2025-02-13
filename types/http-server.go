package types

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/Presslogic-Media/engine.io/v2/errors"
	"github.com/Presslogic-Media/engine.io/v2/events"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/quic-go/quic-go"
)

type HttpServer struct {
	events.EventEmitter
	*ServeMux

	servers []any
	mu      sync.RWMutex
}

func NewWebServer(defaultHandler http.Handler) *HttpServer {
	s := &HttpServer{
		EventEmitter: events.New(),
		ServeMux:     NewServeMux(defaultHandler),
	}
	return s
}

// Deprecated: this method will be removed in the next major release, please use [NewWebServer] instead.
func CreateServer(defaultHandler http.Handler) *HttpServer {
	return NewWebServer(defaultHandler)
}

func (s *HttpServer) httpServer(addr string, handler http.Handler) *http.Server {
	s.mu.Lock()
	defer s.mu.Unlock()

	server := &http.Server{Addr: addr, Handler: handler}

	s.servers = append(s.servers, server)

	return server
}

func (s *HttpServer) h3Server(handler http.Handler) *http3.Server {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Start the servers
	server := &http3.Server{Handler: handler}

	s.servers = append(s.servers, server)

	return server
}

func (s *HttpServer) webtransportServer(addr string, handler http.Handler) *webtransport.Server {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Start the servers
	server := &webtransport.Server{
		H3: http3.Server{Addr: addr, Handler: handler},
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	s.servers = append(s.servers, server)

	return server
}

func (s *HttpServer) Close(fn func(error)) (err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.Emit("close")

	if s.servers != nil {
		var closingErr, serverErr error
		for _, server := range s.servers {
			switch srv := server.(type) {
			case *http.Server:
				serverErr = srv.Shutdown(context.Background())
			case *http3.Server:
				serverErr = srv.Close()
			case *webtransport.Server:
				serverErr = srv.Close()
			default:
				serverErr = errors.New("unknown server type")
			}
			if serverErr != nil && closingErr == nil {
				closingErr = serverErr
			}
		}

		if closingErr != nil {
			err = fmt.Errorf("error occurred while closing servers: %v", closingErr)
		}
	}

	if fn != nil {
		defer fn(err)
	}

	return err
}

func (s *HttpServer) Listen(addr string, fn Callable) *http.Server {
	server := s.httpServer(addr, s)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	if fn != nil {
		defer fn()
	}
	s.Emit("listening")

	return server
}

func (s *HttpServer) ListenTLS(addr string, certFile string, keyFile string, fn Callable) *http.Server {
	server := s.httpServer(addr, s)
	go func() {
		if err := server.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	if fn != nil {
		defer fn()
	}
	s.Emit("listening")

	return server
}

func (s *HttpServer) ListenHTTP3TLS(addr string, certFile string, keyFile string, quicConfig *quic.Config, fn Callable) *http3.Server {
	var err error
	// Load certs
	certs := make([]tls.Certificate, 1)
	certs[0], err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		panic(err)
	}
	// We currently only use the cert-related stuff from tls.Config,
	// so we don't need to make a full copy.
	config := &tls.Config{
		Certificates: certs,
	}

	if addr == "" {
		addr = ":https"
	}

	// Open the listeners
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		panic(err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		panic(err)
	}

	server := s.h3Server(s)
	server.TLSConfig = config
	server.QuicConfig = quicConfig

	go func() {
		defer udpConn.Close()

		hErr := make(chan error)
		qErr := make(chan error)
		go func() {
			hErr <- s.httpServer(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				server.SetQuicHeaders(w.Header())
				s.ServeHTTP(w, r)
			})).ListenAndServeTLS(certFile, keyFile)
		}()
		go func() {
			qErr <- server.Serve(udpConn)
		}()

		select {
		case err := <-hErr:
			server.Close()
			if err != http.ErrServerClosed {
				panic(err)
			}
		case err := <-qErr:
			// Cannot close the HTTP server or wait for requests to complete properly :/
			if err != http.ErrServerClosed {
				panic(err)
			}
		}
	}()

	if fn != nil {
		defer fn()
	}
	s.Emit("listening")

	return server
}

func (s *HttpServer) ListenWebTransportTLS(addr string, certFile string, keyFile string, quicConfig *quic.Config, fn Callable) *webtransport.Server {
	server := s.webtransportServer(addr, s)
	server.H3.QuicConfig = quicConfig

	go func() {
		if err := server.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	if fn != nil {
		defer fn()
	}
	s.Emit("listening")

	return server
}
