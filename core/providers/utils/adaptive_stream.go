package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

const streamAttemptKey schemas.BifrostContextKey = "adaptive-stream-establishment"

// Dedicated sockets still share a host's configured connection allowance.
type upstreamQuota struct {
	host  string
	limit int
}

var adaptiveHostSlots sync.Map // upstreamQuota -> chan struct{}

// StreamAttempt owns its transport socket until the provider has finished reading.
// Interrupt the socket, never close a fasthttp response concurrently with its parser.
type StreamAttempt struct {
	ctx         *schemas.BifrostContext
	deadline    time.Time
	started     time.Time
	timer       *time.Timer
	stopParent  func() bool
	mu          sync.Mutex
	conn        net.Conn
	slot        chan struct{}
	err         error
	committed   atomic.Bool
	done        chan struct{}
	onInterrupt func(*schemas.BifrostError, time.Duration)
}

func NewStreamAttempt(ctx *schemas.BifrostContext, deadline time.Time) *StreamAttempt {
	a := &StreamAttempt{ctx: ctx, started: time.Now(), deadline: deadline, done: make(chan struct{})}
	a.timer = time.AfterFunc(time.Until(deadline), func() { a.abort(context.DeadlineExceeded, true) })
	a.stopParent = context.AfterFunc(ctx, func() { a.abort(ctx.Err(), false) })
	ctx.SetValue(streamAttemptKey, a)
	return a
}
func CurrentStreamAttempt(ctx *schemas.BifrostContext) *StreamAttempt {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(streamAttemptKey).(*StreamAttempt)
	return a
}
func (a *StreamAttempt) abort(err error, establishingOnly bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil || establishingOnly && a.committed.Load() {
		return
	}
	a.err = err
	close(a.done)
	if a.conn != nil {
		a.conn.Close()
	}
	a.releaseHostLocked()
}

func (a *StreamAttempt) releaseHostLocked() {
	if a.slot != nil {
		<-a.slot
		a.slot = nil
	}
}
func (a *StreamAttempt) Close() {
	a.timer.Stop()
	a.stopParent()
	a.abort(context.Canceled, false)
}

func (a *StreamAttempt) Detach() {
	a.ctx.ClearValue(streamAttemptKey)
	if !a.committed.Load() {
		a.Close()
	}
}
func (a *StreamAttempt) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// SetOnInterrupt registers a callback invoked at most once if a committed stream
// receives an upstream error before normal completion (while caller is still active).
func (a *StreamAttempt) SetOnInterrupt(fn func(*schemas.BifrostError, time.Duration)) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.onInterrupt = fn
	a.mu.Unlock()
}

func (a *StreamAttempt) notifyInterrupt(err *schemas.BifrostError) {
	if a == nil || err == nil {
		return
	}
	a.mu.Lock()
	fn := a.onInterrupt
	a.onInterrupt = nil
	a.mu.Unlock()
	if fn != nil {
		fn(err, time.Since(a.started))
	}
}
func (a *StreamAttempt) commit() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(a.deadline) {
		return context.DeadlineExceeded
	}
	a.committed.Store(true)
	a.timer.Stop()
	if a.conn != nil {
		_ = a.conn.SetReadDeadline(time.Now().Add(GetStreamIdleTimeout(a.ctx)))
	}
	return nil
}

type adaptiveStreamConn struct {
	net.Conn
	attempt *StreamAttempt
	idle    time.Duration
}

func (c adaptiveStreamConn) Read(p []byte) (int, error) {
	if c.attempt.committed.Load() {
		if err := c.Conn.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(p)
}

func DoStreamRequest(ctx *schemas.BifrostContext, base *fasthttp.Client, req *fasthttp.Request, resp *fasthttp.Response) error {
	a := CurrentStreamAttempt(ctx)
	if a == nil {
		return base.Do(req, resp)
	}
	return a.do(base, req, resp)
}
func (a *StreamAttempt) acquireHost(base *fasthttp.Client, host string) error {
	limit := base.MaxConnsPerHost
	if limit <= 0 {
		limit = fasthttp.DefaultMaxConnsPerHost
	}
	key := upstreamQuota{host, limit}
	value, ok := adaptiveHostSlots.Load(key)
	if !ok {
		value, _ = adaptiveHostSlots.LoadOrStore(key, make(chan struct{}, limit))
	}
	slots := value.(chan struct{})
	select {
	case slots <- struct{}{}:
	default:
		if base.MaxConnWaitTimeout <= 0 {
			return fasthttp.ErrNoFreeConns
		}
		timer := time.NewTimer(base.MaxConnWaitTimeout)
		defer timer.Stop()
		select {
		case slots <- struct{}{}:
		case <-timer.C:
			return fasthttp.ErrNoFreeConns
		case <-a.done:
			return a.Err()
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		<-slots
		return a.err
	}
	a.slot = slots
	return nil
}

func (a *StreamAttempt) do(base *fasthttp.Client, req *fasthttp.Request, resp *fasthttp.Response) error {
	if err := a.acquireHost(base, string(req.URI().Host())); err != nil {
		return err
	}
	// A private host client gives this attempt exclusive ownership of the socket.
	// Preserve the configured proxy/dialer, TLS and transport options.
	client := CloneFastHTTPClientConfig(base)
	client.Dial = nil
	client.MaxIdemponentCallAttempts = 1
	client.MaxIdleConnDuration = time.Second // The attempt requests Connection: close. Retire private host bookkeeping promptly.
	client.DialTimeout = func(addr string, timeout time.Duration) (net.Conn, error) {
		remaining := time.Until(a.deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		if timeout <= 0 || remaining < timeout {
			timeout = remaining
		}
		type result struct {
			conn net.Conn
			err  error
		}
		connected := make(chan result)
		go func() {
			var conn net.Conn
			var err error
			switch {
			case base.DialTimeout != nil:
				conn, err = base.DialTimeout(addr, timeout)
			case base.Dial != nil:
				conn, err = base.Dial(addr)
			case base.DialDualStack:
				conn, err = fasthttp.DialDualStackTimeout(addr, timeout)
			default:
				conn, err = fasthttp.DialTimeout(addr, timeout)
			}
			select {
			case connected <- result{conn, err}:
			case <-a.done:
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
		var c net.Conn
		var err error
		select {
		case dial := <-connected:
			c, err = dial.conn, dial.err
		case <-a.done:
			return nil, a.Err()
		}
		if err != nil {
			return nil, err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.err != nil {
			c.Close()
			return nil, a.err
		}
		a.conn = c
		return adaptiveStreamConn{c, a, GetStreamIdleTimeout(a.ctx)}, nil
	}
	req.SetConnectionClose()
	if cause := a.Err(); cause != nil {
		return cause
	}
	err := client.Do(req, resp)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	return err
}

func preludeFailure(err error) *schemas.BifrostError {
	status := 502
	if errors.Is(err, context.DeadlineExceeded) {
		status = 504
	}
	if errors.Is(err, context.Canceled) {
		status = 499
	}
	return &schemas.BifrostError{StatusCode: schemas.Ptr(status), Error: &schemas.ErrorField{Message: fmt.Sprintf("upstream stream establishment: %v", err), Error: err}}
}
