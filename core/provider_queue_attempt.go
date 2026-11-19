package bifrost

import (
	"context"
	"sync/atomic"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

// Once published, only the worker can recycle an abandoned message.
type queueTicket struct {
	claimed atomic.Bool
	started chan struct{}
}

func (b *Bifrost) claimQueued(m *ChannelMessage) bool {
	if t := m.ticket; t != nil {
		if !t.claimed.CompareAndSwap(false, true) {
			b.releaseChannelMessage(m)
			return false
		}
		close(t.started)
	}
	return true
}

func (b *Bifrost) enqueueAttempt(q *ProviderQueue, m *ChannelMessage, provider schemas.ModelProvider, model string) *schemas.BifrostError {
	ctx, kind := m.Context, m.RequestType
	failure := func(message string) *schemas.BifrostError {
		e := newBifrostErrorFromMsg(message)
		e.ExtraFields = schemas.BifrostErrorExtraFields{Provider: provider, ModelRequested: model, RequestType: kind}
		return e
	}
	var ticket *queueTicket
	var expired <-chan time.Time
	if deadline, ok := ctx.Value(schemas.BifrostContextKeyAttemptDeadline).(time.Time); ok && adaptiveV22(ctx) != nil {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			b.releaseChannelMessage(m)
			return queueDeadlineError(ctx, provider, model, kind)
		}
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		expired = timer.C
		ticket = &queueTicket{started: make(chan struct{})}
		m.ticket = ticket
	}
	err := func() *schemas.BifrostError {
		q.sendMu.RLock()
		defer q.sendMu.RUnlock()
		if q.isClosing() {
			return failure("provider is shutting down")
		}
		select {
		case q.queue <- m:
		default:
			if b.dropExcessRequests.Load() {
				b.logger.Warn("request dropped: queue is full")
				return failure("request dropped: queue is full")
			}
			select {
			case q.queue <- m:
			case <-q.done:
				return failure("provider is shutting down")
			case <-ctx.Done():
				return newBifrostCtxDoneError(ctx, provider, model, kind, "while waiting for queue space")
			case <-expired:
				return queueDeadlineError(ctx, provider, model, kind)
			}
		}
		return nil
	}()
	if err != nil {
		b.releaseChannelMessage(m)
		return err
	}
	if ticket != nil {
		select {
		case <-ticket.started:
			return nil
		case <-expired:
		case <-ctx.Done():
		}
		if ticket.claimed.CompareAndSwap(false, true) {
			return queueDeadlineError(ctx, provider, model, kind)
		}
	}
	return nil
}

func queueDeadlineError(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string, kind schemas.RequestType) *schemas.BifrostError {
	if ctx.Err() != nil {
		return newBifrostCtxDoneError(ctx, provider, model, kind, "before dispatch")
	}
	e := providerUtils.NewBifrostTimeoutErrorWithTimeoutSeconds(0, context.DeadlineExceeded, provider)
	e.LocalQueueTimeout = true
	e.Error.Message = "routing attempt deadline exceeded before dispatch"
	e.ExtraFields = schemas.BifrostErrorExtraFields{Provider: provider, ModelRequested: model, RequestType: kind}
	return e
}
