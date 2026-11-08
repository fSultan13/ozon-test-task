package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"ozon-test-task/internal/platform/config"
)

const testTimeout = 5 * time.Second

func TestServeHTTPDrainsActiveRequests(t *testing.T) {
	for _, stop := range []string{"signal", "listener error"} {
		t.Run(stop, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			server := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
					_, _ = w.Write([]byte("completed"))
				case <-r.Context().Done():
				}
			}), testTimeout)

			stopping := make(chan struct{})
			server.http.RegisterOnShutdown(func() { close(stopping) })
			response := server.request()
			awaitSignal(t, started)

			if stop == "signal" {
				server.cancel()
			} else if err := server.listener.Close(); err != nil {
				t.Fatal(err)
			}
			awaitSignal(t, stopping)

			select {
			case err := <-server.result:
				t.Fatalf("server returned before active request completed: %v", err)
			default:
			}

			close(release)
			if err := awaitResult(t, response); err != nil {
				t.Fatalf("active request failed: %v", err)
			}
			err := awaitResult(t, server.result)
			if stop == "signal" && err != nil {
				t.Fatalf("graceful shutdown: %v", err)
			}
			if stop == "listener error" && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("serve error = %v, want net.ErrClosed", err)
			}
		})
	}
}

func TestServeHTTPClosesActiveConnectionsAfterTimeout(t *testing.T) {
	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := startTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(requestCanceled)
	}), 50*time.Millisecond)

	response := server.request()
	awaitSignal(t, started)
	server.cancel()

	if err := awaitResult(t, server.result); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want context.DeadlineExceeded", err)
	}
	awaitSignal(t, requestCanceled)
	if err := awaitResult(t, response); err == nil {
		t.Fatal("request unexpectedly completed after forced connection close")
	}
}

type testHTTPServer struct {
	http     *http.Server
	listener net.Listener
	cancel   context.CancelFunc
	result   <-chan error
}

func startTestServer(t *testing.T, handler http.Handler, shutdownTimeout time.Duration) testHTTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	log := slog.New(slog.DiscardHandler)
	server := newHTTPServer(config.HTTP{
		ReadHeaderTimeout: testTimeout,
		ReadTimeout:       testTimeout,
		WriteTimeout:      testTimeout,
		IdleTimeout:       testTimeout,
	}, handler, log)
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- serveHTTP(ctx, server, listener, shutdownTimeout, log) }()
	return testHTTPServer{http: server, listener: listener, cancel: cancel, result: result}
}

func (server testHTTPServer) request() <-chan error {
	result := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: testTimeout}
		response, err := client.Get("http://" + server.listener.Addr().String())
		if err != nil {
			result <- err
			return
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err == nil && (response.StatusCode != http.StatusOK || string(body) != "completed") {
			err = fmt.Errorf("unexpected response: status %d, body %q", response.StatusCode, body)
		}
		result <- err
	}()
	return result
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for lifecycle event")
	}
}

func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for operation to finish")
		return nil
	}
}
