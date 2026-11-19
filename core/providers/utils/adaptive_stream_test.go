package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestV22TLSHostCapacityAndRelease(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	base := &fasthttp.Client{MaxConnsPerHost: 1, TLSConfig: &tls.Config{RootCAs: roots}}
	call := func() (*StreamAttempt, error) {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		a := NewStreamAttempt(ctx, time.Now().Add(time.Second))
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)
		req.SetRequestURI(server.URL)
		return a, a.do(base, req, resp)
	}
	first, err := call()
	defer first.Detach()
	require.NoError(t, err)
	blocked, err := call()
	blocked.Detach()
	require.ErrorIs(t, err, fasthttp.ErrNoFreeConns)
	first.Detach()
	last, err := call()
	defer last.Detach()
	require.NoError(t, err)
}

func TestV22BufferedTLSReuseAndCancellation(t *testing.T) {
	var connections atomic.Int32
	entered, closed := make(chan struct{}), make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stall" {
			fmt.Fprint(w, "partial")
			w.(http.Flusher).Flush()
			close(entered)
			select {
			case <-r.Context().Done():
			case <-time.After(4 * time.Second):
			}
			close(closed)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	base := ConfigureDialer(&fasthttp.Client{TLSConfig: &tls.Config{RootCAs: roots}, MaxConnsPerHost: 2, MaxConnWaitTimeout: 3 * time.Second})
	defer base.CloseIdleConnections()
	call := func(parent context.Context, path string, adaptive bool) error {
		ctx := schemas.NewBifrostContext(parent, schemas.NoDeadline)
		if adaptive {
			deadline := time.Now().Add(3 * time.Second)
			ctx.SetValue(schemas.BifrostContextKeyAttemptDeadline, deadline)
			a := NewStreamAttempt(ctx, deadline)
			defer a.Detach()
		}
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)
		req.SetRequestURI(server.URL + path)
		err, _ := DoWithAttemptDeadline(ctx, base, req, resp)
		if err == nil && string(resp.Body()) != "ok" {
			return fmt.Errorf("unexpected body %q", resp.Body())
		}
		return err
	}
	for _, adaptive := range []bool{false, true, true, true} {
		require.NoError(t, call(context.Background(), "/", adaptive))
	}
	require.EqualValues(t, 1, connections.Load())
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- call(parent, "/stall", true) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upstream never received request")
	}
	require.NoError(t, call(context.Background(), "/", true))
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled call still reading")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upstream connection still active")
	}
	require.NoError(t, call(context.Background(), "/", true))
	require.EqualValues(t, 2, connections.Load(), "cancelling one request must preserve the healthy pooled socket")
}

func TestV22LargeResponseOwnsSocketUntilReaderCloses(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	a := NewStreamAttempt(ctx, time.Now().Add(80*time.Millisecond))
	conn, peer := net.Pipe()
	defer peer.Close()
	a.conn = conn
	raw := &LargeResponseReader{Reader: conn, Resp: fasthttp.AcquireResponse()}
	defer raw.Close()
	require.NoError(t, raw.AdoptAttempt(a))
	a.Detach()
	time.Sleep(100 * time.Millisecond)
	go func() { _, _ = peer.Write([]byte("x")) }()
	var buf [1]byte
	_, err := raw.Read(buf[:])
	require.NoError(t, err)
	require.Equal(t, byte('x'), buf[0])
	require.NoError(t, raw.Close())
	_, err = peer.Read(buf[:])
	require.ErrorContains(t, err, "EOF")
}

func TestV22CancelledPoolWaitCannotSendLater(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()
	defer unblock()
	base := ConfigureDialer(&fasthttp.Client{MaxConnsPerHost: 1, MaxConnWaitTimeout: 3 * time.Second})
	defer base.CloseIdleConnections()
	fetch := func(ctx context.Context) error {
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)
		req.SetRequestURI(server.URL)
		err, _ := DoWithAttemptDeadline(ctx, base, req, resp)
		return err
	}
	first := make(chan error, 1)
	go func() { first <- fetch(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start")
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	deadline := time.Now().Add(80 * time.Millisecond)
	ctx.SetValue(schemas.BifrostContextKeyAttemptDeadline, deadline)
	a := NewStreamAttempt(ctx, deadline)
	defer a.Detach()
	start := time.Now()
	require.Error(t, fetch(ctx))
	require.Less(t, time.Since(start), time.Second)
	require.EqualValues(t, 1, calls.Load())
	unblock()
	require.NoError(t, <-first)
	require.NoError(t, fetch(context.Background()))
	require.EqualValues(t, 2, calls.Load())
}

func TestV22SocketCleanupConcurrent(t *testing.T) {
	var active atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		fmt.Fprint(w, ": ping\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			a := NewStreamAttempt(ctx, time.Now().Add(150*time.Millisecond))
			defer a.Detach()
			req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
			defer fasthttp.ReleaseRequest(req)
			defer ReleaseStreamingResponse(resp)
			req.SetRequestURI(server.URL)
			resp.StreamBody = true
			if err := DoStreamRequest(ctx, &fasthttp.Client{}, req, resp); err != nil {
				return
			}
			if resp.BodyStream() != nil {
				buf := make([]byte, 64)
				for {
					if _, err := resp.BodyStream().Read(buf); err != nil {
						break
					}
				}
			}
		}()
	}
	wg.Wait()
	require.Eventually(t, func() bool { return active.Load() == 0 && runtime.NumGoroutine() <= before+6 }, 4*time.Second, 20*time.Millisecond)
}

func TestV22PreludeLimitAndBoundary(t *testing.T) {
	for i := 0; i < 64; i++ {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		a := NewStreamAttempt(ctx, time.Now().Add(time.Minute))
		stream := make(chan *schemas.BifrostStreamChunk, 1)
		if i == 0 {
			stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Role: schemas.Ptr(strings.Repeat("r", 128*1024))}, nil)
		} else {
			go a.abort(context.DeadlineExceeded, true)
			stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Content: schemas.Ptr("once")}, nil)
		}
		close(stream)
		out, done, e := CheckStreamPrelude(stream, a)
		if out != nil {
			n := 0
			for range out {
				n++
			}
			require.Equal(t, 1, n)
			require.Nil(t, e)
		} else {
			require.NotNil(t, e)
		}
		if i == 0 {
			require.Nil(t, out)
			require.Equal(t, 502, *e.StatusCode)
		}
		<-done
		a.Detach()
	}
}

func TestV22LateDialClosed(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	a := NewStreamAttempt(ctx, time.Now().Add(100*time.Millisecond))
	defer a.Detach()
	unblock := make(chan struct{})
	late, peer := net.Pipe()
	defer peer.Close()
	client := &fasthttp.Client{Dial: func(string) (net.Conn, error) { <-unblock; return late, nil }}
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI("http://localhost/")
	err := DoStreamRequest(ctx, client, req, resp)
	close(unblock)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	peer.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	_, err = peer.Read(b[:])
	require.ErrorContains(t, err, "EOF")
}

func TestV22BufferedStaleConnectionRetry(t *testing.T) {
	var connections, requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		fmt.Fprintf(w, "ok-%d", n)
	}))
	server.Config.IdleTimeout = 120 * time.Millisecond
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	base := ConfigureDialer(&fasthttp.Client{
		TLSConfig:           &tls.Config{RootCAs: roots},
		MaxIdleConnDuration: 5 * time.Second,
		MaxConnsPerHost:     4,
	})
	defer base.CloseIdleConnections()

	post := func() (string, error) {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		deadline := time.Now().Add(2 * time.Second)
		ctx.SetValue(schemas.BifrostContextKeyAttemptDeadline, deadline)
		a := NewStreamAttempt(ctx, deadline)
		defer a.Detach()
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)
		req.SetRequestURI(server.URL)
		req.Header.SetMethod(http.MethodPost)
		req.SetBodyString(`{"x":1}`)
		err, _ := DoWithAttemptDeadline(ctx, base, req, resp)
		return string(resp.Body()), err
	}

	body1, err := post()
	require.NoError(t, err)
	require.Equal(t, "ok-1", body1)
	require.EqualValues(t, 1, connections.Load())

	// Let server close the idle keep-alive socket while client pool still holds it.
	time.Sleep(220 * time.Millisecond)

	body2, err := post()
	require.NoError(t, err)
	require.Equal(t, "ok-2", body2)
	require.EqualValues(t, 2, connections.Load())
}
