package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServer_GracefulShutdownDrainsInFlightRequest proves the core graceful
// shutdown guarantee: a request already being handled when shutdown starts is
// allowed to finish, rather than being cut off.
func TestServer_GracefulShutdownDrainsInFlightRequest(t *testing.T) {
	const addr = "localhost:18080"

	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	srv := New(Config{Port: "18080", ShutdownTimeout: 5 * time.Second}, handler)

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- srv.RunWithContext(ctx) }()

	// Wait for the listener to accept connections — a raw dial, not an HTTP request,
	// so it doesn't itself reach (and get stuck in) the blocking handler above.
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 2*time.Second, 10*time.Millisecond)

	clientDone := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/") //nolint:noctx // test helper
		require.NoError(t, err)
		clientDone <- resp
	}()

	<-started // the handler is now blocked mid-request
	cancel()  // trigger shutdown while that request is still in flight

	select {
	case <-clientDone:
		t.Fatal("client request completed before the handler was released — shutdown didn't wait for it")
	case <-time.After(100 * time.Millisecond):
		// still blocked, as expected: Shutdown must not cut the in-flight request off.
	}

	close(release) // let the in-flight handler finish

	select {
	case resp := <-clientDone:
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request never completed")
	}

	select {
	case err := <-runErrCh:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("RunWithContext never returned after the in-flight request finished")
	}
}
