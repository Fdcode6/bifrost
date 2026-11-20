package bifrost

import (
	"context"
	"fmt"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

// Keep original indices: the pinned key, layer and health callback all refer to this index.
type fallbackCursor struct {
	position int
	tried    []bool
}

func (c *fallbackCursor) next(ctx *schemas.BifrostContext, count, executed int) (int, int) {
	if count == 0 {
		return -1, 0
	}
	if d, ok := ctx.Value(schemas.BifrostContextKeyFallbackDeadline).(time.Time); ok && !d.IsZero() && !time.Now().Before(d) {
		return -1, 0
	}
	p := adaptiveV22(ctx)
	if p == nil {
		for c.position < count {
			i := c.position
			c.position++
			skip, stop, timeout := selectAdaptiveFallbackAction(ctx, count, i, executed)
			if stop {
				break
			}
			if !skip {
				return i, timeout
			}
		}
		return -1, 0
	}
	if ctx.Err() != nil || p.MaxTotalAttempts > 0 && executed >= p.MaxTotalAttempts {
		return -1, 0
	}
	if c.tried == nil {
		c.tried = make([]bool, count)
	}
	count = min(count, len(p.FallbackHints))
	deadline, _ := ctx.Value(schemas.BifrostContextKeyFallbackDeadline).(time.Time)
	if caller, ok := ctx.Deadline(); ok && (deadline.IsZero() || caller.Before(deadline)) {
		deadline = caller
	}
	best, regular, eligible := -1, -1, 0
	for i, h := range p.FallbackHints[:count] {
		if c.tried[i] || h.Available != nil && !h.Available() {
			continue
		}
		eligible++
		if best < 0 {
			best = i
		}
		if h.ContinuationRank > 0 && (regular < 0 || h.ContinuationRank < p.FallbackHints[regular].ContinuationRank) {
			regular = i
		}
	}
	if best < 0 {
		return -1, 0
	}
	const minHopMs, finalReserveMs = 3000, 14000
	final := eligible == 1 || p.MaxTotalAttempts == executed+1
	left := 0
	if !deadline.IsZero() {
		left = int(time.Until(deadline).Milliseconds())
		if left < minHopMs {
			return -1, 0
		}
		final = final || left < finalReserveMs+minHopMs
	}
	continuation := !final && regular >= 0 && !p.PrimaryHint.IsProbationProbe &&
		time.Since(p.RequestStartTime) < time.Duration(p.FastRescueElapsedMs)*time.Millisecond
	budget := p.RescueAttemptTimeoutMs
	if continuation {
		best = regular
		budget = p.FallbackHints[best].AttemptTimeoutMs
	}
	if left > 0 {
		if final {
			budget = left
		} else if budget <= 0 || budget > left-finalReserveMs {
			budget = left - finalReserveMs
		}
	}
	c.tried[best] = true
	ctx.AppendRoutingEngineLog("adaptive-v22", fmt.Sprintf("fallback=%d continuation=%t eligible=%d budget_ms=%d", best+1, continuation, eligible, budget))
	return best, budget
}

func adaptiveAttemptHint(p *schemas.AdaptiveFallbackRuntimePolicy, index int) *schemas.FallbackAttemptHint {
	if p == nil || index < 0 || index > len(p.FallbackHints) {
		return nil
	}
	if index == 0 {
		return &p.PrimaryHint
	}
	return &p.FallbackHints[index-1]
}

func (b *Bifrost) recordAdaptiveAttempt(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, index, hop int, stream bool, start time.Time, err *schemas.BifrostError) {
	hint := adaptiveAttemptHint(adaptiveV22(ctx), index)
	if hint == nil {
		return
	}
	elapsed := time.Since(start)
	if hint.Observe != nil {
		hint.Observe(ctx, stream, elapsed, err)
	}
	status := 200
	reason := "success"
	if err != nil {
		status = 0
		if err.StatusCode != nil {
			status = *err.StatusCode
		}
		reason = "upstream_error"
		if err.Error != nil && err.Error.Type != nil {
			reason = *err.Error.Type
		}
		if status == 504 {
			reason = "timeout"
		}
		if err.LocalQueueTimeout {
			reason = "local_queue_timeout"
		}
		if ctx.Err() != nil {
			reason = ctx.Err().Error()
		}
	}
	deadline, _ := ctx.Value(schemas.BifrostContextKeyFallbackDeadline).(time.Time)
	remaining := int64(-1)
	if !deadline.IsZero() {
		remaining = max(int64(0), time.Until(deadline).Milliseconds())
	}
	provider, model, _ := req.GetRequestFields()
	b.logger.Info("adaptive-v22 root=%v hop=%d target=%s/%s stream=%t elapsed_ms=%d remaining_ms=%d status=%d reason=%s caller_cancelled=%t", ctx.Value(schemas.BifrostContextKeyRequestID), hop, provider, model, stream, elapsed.Milliseconds(), remaining, status, reason, ctx.Err() != nil)
}

func (b *Bifrost) tryBudgetedRequest(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, index, hop, timeout int) (*schemas.BifrostResponse, *schemas.BifrostError) {
	clear := applyAdaptiveAttemptDeadline(ctx, timeout)
	defer clear()
	if adaptiveV22(ctx) == nil || isLargePayloadPassthrough(ctx) {
		return b.tryRequest(ctx, req)
	}
	switch req.RequestType {
	case schemas.ChatCompletionRequest, schemas.TextCompletionRequest, schemas.ResponsesRequest:
	default:
		return b.tryRequest(ctx, req)
	}
	deadline, ok := ctx.Value(schemas.BifrostContextKeyAttemptDeadline).(time.Time)
	if !ok {
		return b.tryRequest(ctx, req)
	}
	start := time.Now()
	attempt := providerUtils.NewStreamAttempt(ctx, deadline)
	defer attempt.Detach()
	provider, model, _ := req.GetRequestFields()
	ctx.AppendRoutingEngineLog("adaptive-v22", fmt.Sprintf("hop=%d target=%s/%s completion_ms=%d", hop, provider, model, time.Until(deadline).Milliseconds()))
	resp, err := b.tryRequest(ctx, req)
	if err == nil {
		err = ensureSuccessfulResponsePayload(resp, req.RequestType, provider, model)
	}
	if err == nil && ctx.Err() != nil {
		err = newBifrostCtxDoneError(ctx, provider, model, req.RequestType, "after provider response")
	} else if err == nil && !time.Now().Before(deadline) {
		err = providerUtils.NewBifrostTimeoutError("response arrived after attempt deadline", context.DeadlineExceeded, provider)
	}
	if raw, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseReader).(*providerUtils.LargeResponseReader); raw != nil {
		if err == nil {
			if handoffErr := raw.AdoptAttempt(attempt); handoffErr != nil {
				err = providerUtils.NewBifrostTimeoutError("large response handoff exceeded attempt deadline", handoffErr, provider)
			}
		}
		if err != nil {
			attempt.Close()
			_ = raw.Close()
			ctx.ClearValue(schemas.BifrostContextKeyLargeResponseReader)
			ctx.ClearValue(schemas.BifrostContextKeyLargeResponseMode)
		}
	}
	if err != nil {
		resp = nil
	}
	b.recordAdaptiveAttempt(ctx, req, index, hop, false, start, err)
	return resp, err
}

func (b *Bifrost) tryBudgetedStream(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, index, hop, timeout int) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	large, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool)
	if adaptiveV22(ctx) == nil || large {
		return b.tryStreamRequest(ctx, req)
	}
	switch req.RequestType {
	case schemas.ChatCompletionStreamRequest, schemas.TextCompletionStreamRequest, schemas.ResponsesStreamRequest:
	default:
		return b.tryStreamRequest(ctx, req)
	}
	clear := applyAdaptiveAttemptDeadline(ctx, timeout)
	defer clear()
	deadline, ok := ctx.Value(schemas.BifrostContextKeyAttemptDeadline).(time.Time)
	if !ok {
		return b.tryStreamRequest(ctx, req)
	}
	start := time.Now()
	attempt := providerUtils.NewStreamAttempt(ctx, deadline)
	defer attempt.Detach()
	if hint := adaptiveAttemptHint(adaptiveV22(ctx), index); hint != nil && hint.Observe != nil {
		observe := hint.Observe
		attempt.SetOnInterrupt(func(interruptErr *schemas.BifrostError, elapsed time.Duration) {
			observe(ctx, true, elapsed, interruptErr)
		})
	}
	provider, model, _ := req.GetRequestFields()
	ctx.AppendRoutingEngineLog("adaptive-v22", fmt.Sprintf("hop=%d target=%s/%s establish_ms=%d", hop, provider, model, time.Until(deadline).Milliseconds()))
	stream, err := b.tryStreamRequest(ctx, req)
	if err != nil && ctx.Err() != nil {
		err = newBifrostCtxDoneError(ctx, provider, model, req.RequestType, "waiting for first output")
	} else if err != nil && !err.LocalQueueTimeout && attempt.Err() == context.DeadlineExceeded {
		err = providerUtils.NewBifrostTimeoutError("stream establishment deadline exceeded", context.DeadlineExceeded, provider)
	}
	b.recordAdaptiveAttempt(ctx, req, index, hop, true, start, err)
	return stream, err
}
