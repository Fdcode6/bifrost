package utils

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/valyala/fasthttp"
)

// Use the provider's existing host pool. Only the request owning a checked-out
// connection may interrupt it. SSE and raw response readers own their sockets separately.
type bufferedAttemptTransport struct{}

type bufferedLease struct {
	conn    net.Conn
	release func(discard bool)
	err     error
}

func (bufferedAttemptTransport) RoundTrip(h *fasthttp.HostClient, req *fasthttp.Request, resp *fasthttp.Response) (bool, error) {
	a, _ := req.UserValue(streamAttemptKey).(*StreamAttempt)
	if a == nil {
		return fasthttp.DefaultTransport.RoundTrip(h, req, resp)
	}
	// AcquireConn can block on the pool or on a custom dialer. A cancelled caller
	// must not retain pooled request objects while a late acquisition finishes.
	left, closing := time.Until(a.deadline), req.ConnectionClose()
	if left <= 0 {
		return false, context.DeadlineExceeded
	}
	ready := make(chan bufferedLease)
	go func() {
		cc, err := h.AcquireConn(left, closing)
		lease := bufferedLease{err: err}
		if err == nil {
			retire := closing || h.MaxConnDuration > 0 && time.Since(cc.CreatedTime()) >= h.MaxConnDuration
			lease.conn = cc.Conn()
			lease.release = func(discard bool) {
				if discard || retire {
					h.CloseConn(cc)
				} else {
					h.ReleaseConn(cc)
				}
			}
		}
		select {
		case ready <- lease:
		case <-a.done:
			if lease.release != nil {
				lease.release(false)
			}
		}
	}()
	var lease bufferedLease
	select {
	case lease = <-ready:
		if lease.err != nil {
			return false, lease.err
		}
	case <-a.done:
		return false, a.Err()
	}
	a.mu.Lock()
	if a.err != nil {
		cause := a.err
		a.mu.Unlock()
		lease.release(false)
		return false, cause
	}
	a.conn = lease.conn
	a.mu.Unlock()

	err := a.exchangeBuffered(h, lease.conn, req, resp)
	a.mu.Lock()
	a.conn = nil // Disarm before making this socket visible to the next request.
	aborted := a.err != nil
	if aborted {
		err = a.err
	}
	a.mu.Unlock()
	lease.release(err != nil || resp.ConnectionClose())
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() && !errors.Is(err, context.DeadlineExceeded) {
		err = fasthttp.ErrTimeout
	}
	return !aborted && err != nil && !errors.Is(err, fasthttp.ErrBodyTooLarge), err
}

func (a *StreamAttempt) exchangeBuffered(h *fasthttp.HostClient, conn net.Conn, req *fasthttp.Request, resp *fasthttp.Response) error {
	deadline := func(limit time.Duration) time.Time {
		if limit > 0 && limit < time.Until(a.deadline) {
			return time.Now().Add(limit)
		}
		return a.deadline
	}
	resp.ParseNetConn(conn)
	if err := conn.SetWriteDeadline(deadline(h.WriteTimeout)); err != nil {
		return err
	}
	w := h.AcquireWriter(conn)
	err := req.Write(w)
	if err == nil {
		err = w.Flush()
	}
	h.ReleaseWriter(w)
	if err != nil {
		return err
	}
	if err := conn.SetReadDeadline(deadline(h.ReadTimeout)); err != nil {
		return err
	}
	if req.Header.IsHead() {
		resp.SkipBody = true
	}
	if h.DisableHeaderNamesNormalizing {
		resp.Header.DisableNormalizing()
	}
	r := h.AcquireReader(conn)
	err = resp.ReadLimitBody(r, h.MaxResponseBodySize)
	h.ReleaseReader(r)
	return err
}
