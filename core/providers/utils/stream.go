package utils

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const (
	maxBufferedPreludeChunks = 32
	maxBufferedPreludeBytes  = 128 * 1024
)

// CheckFirstStreamChunkForError reads the first chunk from a streaming channel to detect
// errors returned inside HTTP 200 SSE streams (e.g., providers that send rate limit
// errors as SSE events instead of HTTP 429).
//
// Instead of only inspecting the very first chunk, this helper now buffers a small
// prelude window and keeps observing until one of three things happens:
//  1. A meaningful error arrives before any effective payload => return sync error
//  2. The first effective payload arrives => return a wrapped stream with buffered chunks
//  3. The prelude buffer limit is reached => commit the current stream as-is
//
// This lets callers retry/fallback when providers emit lifecycle/setup events first
// (for example Responses API `response.created` / `response.in_progress`) and only
// fail before any real user-visible output has been produced.
//
// If an error is returned, the source channel is drained in the background (so the
// provider goroutine can exit cleanly). The returned drainDone channel is closed
// once that drain completes — callers must wait on it before releasing any resources
// (e.g., plugin pipelines) that the provider goroutine's postHookRunner may still reference.
//
// If the stream is committed, it returns a wrapped channel that re-emits the buffered
// chunks followed by all remaining chunks from the source. drainDone is
// closed when the wrapper goroutine finishes forwarding the source stream.
//
// If the source channel is closed immediately (empty stream), it returns a
// nil channel with nil error. drainDone is already closed.
func CheckFirstStreamChunkForError(
	stream chan *schemas.BifrostStreamChunk,
) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	return CheckStreamPrelude(stream, nil)
}

func CheckStreamPrelude(stream chan *schemas.BifrostStreamChunk, attempt *StreamAttempt) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	var expired <-chan struct{}
	if attempt != nil {
		expired = attempt.done
	}
	fail := func(e *schemas.BifrostError) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
		if attempt != nil {
			if cause := attempt.Err(); cause != nil {
				e = preludeFailure(cause)
			}
			attempt.Close()
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if stream != nil {
				for range stream {
				}
			}
		}()
		return nil, done, e
	}
	buffered := make([]*schemas.BifrostStreamChunk, 0, max(1, min(cap(stream), maxBufferedPreludeChunks)))
	bufferedBytes := 0
	if attempt != nil && stream == nil {
		return fail(preludeFailure(errors.New("upstream returned no stream")))
	}

	for {
		var chunk *schemas.BifrostStreamChunk
		var ok bool
		select {
		case <-expired:
			return fail(preludeFailure(attempt.Err()))
		case chunk, ok = <-stream:
		}
		if attempt != nil && !ok {
			return fail(preludeFailure(errors.New("upstream closed before effective output")))
		}
		if !ok {
			if len(buffered) == 0 {
				// Channel closed immediately (empty stream) — return nil so callers
				// can distinguish this from a live stream channel.
				done := make(chan struct{})
				close(done)
				return nil, done, nil
			}
			return wrapObservedStream(buffered, stream)
		}

		if bifrostErr := extractRetryablePreludeError(chunk); bifrostErr != nil {
			return fail(bifrostErr)
		}

		buffered = append(buffered, chunk)
		bufferedBytes += estimatePreludeChunkSize(chunk)

		effective := isEffectiveStreamChunk(chunk)
		full := len(buffered) >= maxBufferedPreludeChunks || bufferedBytes >= maxBufferedPreludeBytes
		if attempt != nil {
			effective = effective && chunk != nil && (chunk.BifrostChatResponse != nil || chunk.BifrostResponsesStreamResponse != nil || chunk.BifrostTextCompletionResponse != nil)
			if !effective && full {
				return fail(preludeFailure(errors.New("stream prelude exceeded buffer limit")))
			}
			if effective {
				if err := attempt.commit(); err != nil {
					return fail(preludeFailure(err))
				}
				attempt.ctx.AppendRoutingEngineLog("adaptive-v22", fmt.Sprintf("stream effective output after %dms", time.Since(attempt.started).Milliseconds()))
				return forwardAdaptiveStream(attempt, buffered, stream)
			}
		}
		if effective || full {
			return wrapObservedStream(buffered, stream)
		}
	}
}

func forwardAdaptiveStream(attempt *StreamAttempt, prefix []*schemas.BifrostStreamChunk, source chan *schemas.BifrostStreamChunk) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	out := make(chan *schemas.BifrostStreamChunk, len(prefix))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(out)
		defer attempt.Close()
		firstText := false
		firstTextMs := int64(-1)
		effectiveMs := time.Since(attempt.started).Milliseconds()
		interrupted := false
		defer func() {
			p, _ := attempt.ctx.Value(schemas.BifrostContextKeyAdaptiveFallbackPolicy).(*schemas.AdaptiveFallbackRuntimePolicy)
			prefixMs := int64(0)
			if p != nil && !p.RequestStartTime.IsZero() {
				prefixMs = attempt.started.Sub(p.RequestStartTime).Milliseconds()
			}
			if firstTextMs >= 0 {
				firstTextMs += prefixMs
			}
			getLogger().Info("adaptive-v22 stream_end root=%v hop_index=%v effective_ms=%d first_text_ms=%d elapsed_ms=%d interrupted=%t caller_cancelled=%t", attempt.ctx.Value(schemas.BifrostContextKeyRequestID), attempt.ctx.Value(schemas.BifrostContextKeyFallbackIndex), prefixMs+effectiveMs, firstTextMs, prefixMs+time.Since(attempt.started).Milliseconds(), interrupted, attempt.ctx.Err() != nil)
		}()
		forward := func(c *schemas.BifrostStreamChunk) {
			if !firstText && chunkHasText(c) {
				firstText = true
				firstTextMs = time.Since(attempt.started).Milliseconds()
				attempt.ctx.AppendRoutingEngineLog("adaptive-v22", fmt.Sprintf("stream first text after %dms", firstTextMs))
			}
			if !interrupted && c != nil && c.BifrostError != nil && attempt.ctx.Err() == nil {
				interrupted = true
				attempt.ctx.AppendRoutingEngineLog("adaptive-v22", "stream interrupted after commit")
				attempt.notifyInterrupt(c.BifrostError)
			}
			select {
			case out <- c:
			case <-attempt.ctx.Done():
			}
		}
		for _, c := range prefix {
			forward(c)
		}
		for c := range source {
			forward(c)
		}
	}()
	return out, done, nil
}

// StreamReadError detects EOF without a terminal event on V2.2's default SSE reader.
func StreamReadError(ctx *schemas.BifrostContext, reader SSEDataReader, err error, terminal bool) error {
	if err != io.EOF || terminal || ctx == nil {
		return err
	}
	r, ok := reader.(*defaultSSEDataReader)
	policy, _ := ctx.Value(schemas.BifrostContextKeyAdaptiveFallbackPolicy).(*schemas.AdaptiveFallbackRuntimePolicy)
	if ok && !r.doneMarker && policy != nil && policy.V22 {
		return io.ErrUnexpectedEOF
	}
	return err
}

func chunkHasText(c *schemas.BifrostStreamChunk) bool {
	if c == nil {
		return false
	}
	if r := c.BifrostChatResponse; r != nil {
		for _, v := range r.Choices {
			if v.ChatStreamResponseChoice != nil && v.Delta != nil && v.Delta.Content != nil && *v.Delta.Content != "" {
				return true
			}
		}
	}
	if r := c.BifrostResponsesStreamResponse; r != nil {
		return r.Type == schemas.ResponsesStreamResponseTypeOutputTextDelta && r.Delta != nil && *r.Delta != ""
	}
	return isEffectiveTextResponse(c.BifrostTextCompletionResponse)
}

func wrapObservedStream(
	buffered []*schemas.BifrostStreamChunk,
	stream chan *schemas.BifrostStreamChunk,
) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	done := make(chan struct{})
	wrapped := make(chan *schemas.BifrostStreamChunk, max(max(cap(stream), len(buffered)), 1))
	go func() {
		defer close(done)
		defer close(wrapped)
		for _, chunk := range buffered {
			wrapped <- chunk
		}
		for chunk := range stream {
			wrapped <- chunk
		}
	}()
	return wrapped, done, nil
}

func extractRetryablePreludeError(chunk *schemas.BifrostStreamChunk) *schemas.BifrostError {
	if chunk == nil || chunk.BifrostError == nil || chunk.BifrostError.Error == nil {
		return nil
	}
	if chunk.BifrostError.Error.Message == "" && chunk.BifrostError.Error.Code == nil && chunk.BifrostError.Error.Type == nil {
		return nil
	}
	return chunk.BifrostError
}

func isEffectiveStreamChunk(chunk *schemas.BifrostStreamChunk) bool {
	switch {
	case chunk == nil:
		return false
	case chunk.BifrostChatResponse != nil:
		return isEffectiveChatResponse(chunk.BifrostChatResponse)
	case chunk.BifrostTextCompletionResponse != nil:
		return isEffectiveTextResponse(chunk.BifrostTextCompletionResponse)
	case chunk.BifrostResponsesStreamResponse != nil:
		return isEffectiveResponsesStreamResponse(chunk.BifrostResponsesStreamResponse)
	default:
		return true
	}
}

func isEffectiveChatResponse(resp *schemas.BifrostChatResponse) bool {
	if resp == nil {
		return false
	}
	for _, choice := range resp.Choices {
		if choice.ChatStreamResponseChoice == nil || choice.ChatStreamResponseChoice.Delta == nil {
			continue
		}
		if isEffectiveChatDelta(choice.ChatStreamResponseChoice.Delta) {
			return true
		}
	}
	return false
}

func isEffectiveChatDelta(delta *schemas.ChatStreamResponseChoiceDelta) bool {
	if delta == nil {
		return false
	}
	if delta.Content != nil && *delta.Content != "" {
		return true
	}
	if delta.Refusal != nil && *delta.Refusal != "" {
		return true
	}
	if delta.Reasoning != nil && *delta.Reasoning != "" {
		return true
	}
	if len(delta.ReasoningDetails) > 0 {
		return true
	}
	if len(delta.ToolCalls) > 0 {
		return true
	}
	if delta.Audio != nil {
		return true
	}
	return false
}

func isEffectiveTextResponse(resp *schemas.BifrostTextCompletionResponse) bool {
	if resp == nil {
		return false
	}
	for _, choice := range resp.Choices {
		if choice.TextCompletionResponseChoice != nil && choice.TextCompletionResponseChoice.Text != nil && *choice.TextCompletionResponseChoice.Text != "" {
			return true
		}
		if choice.ChatStreamResponseChoice != nil && isEffectiveChatDelta(choice.ChatStreamResponseChoice.Delta) {
			return true
		}
	}
	return false
}

func isEffectiveResponsesStreamResponse(resp *schemas.BifrostResponsesStreamResponse) bool {
	if resp == nil {
		return false
	}
	switch resp.Type {
	case schemas.ResponsesStreamResponseTypeOutputItemAdded:
		return isEffectiveResponsesOutputItem(resp.Item)
	case schemas.ResponsesStreamResponseTypeOutputTextDelta,
		schemas.ResponsesStreamResponseTypeRefusalDelta,
		schemas.ResponsesStreamResponseTypeReasoningSummaryTextDelta,
		schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta,
		schemas.ResponsesStreamResponseTypeMCPCallArgumentsDelta,
		schemas.ResponsesStreamResponseTypeCustomToolCallInputDelta,
		schemas.ResponsesStreamResponseTypeCodeInterpreterCallCodeDelta:
		return resp.Delta != nil && *resp.Delta != ""
	case schemas.ResponsesStreamResponseTypeOutputTextDone:
		return resp.Text != nil && *resp.Text != ""
	case schemas.ResponsesStreamResponseTypeRefusalDone:
		return resp.Refusal != nil && *resp.Refusal != ""
	case schemas.ResponsesStreamResponseTypeReasoningSummaryTextDone:
		return resp.Text != nil && *resp.Text != ""
	case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDone,
		schemas.ResponsesStreamResponseTypeMCPCallArgumentsDone,
		schemas.ResponsesStreamResponseTypeCustomToolCallInputDone:
		return resp.Arguments != nil && *resp.Arguments != ""
	case schemas.ResponsesStreamResponseTypeCodeInterpreterCallCodeDone:
		return resp.Code != nil && *resp.Code != ""
	case schemas.ResponsesStreamResponseTypeImageGenerationCallPartialImage:
		return resp.PartialImageB64 != nil && *resp.PartialImageB64 != ""
	default:
		return false
	}
}

func isEffectiveResponsesOutputItem(item *schemas.ResponsesMessage) bool {
	if item == nil || item.Type == nil {
		return false
	}

	switch *item.Type {
	case schemas.ResponsesMessageTypeFunctionCall,
		schemas.ResponsesMessageTypeMCPCall,
		schemas.ResponsesMessageTypeCustomToolCall,
		schemas.ResponsesMessageTypeCodeInterpreterCall,
		schemas.ResponsesMessageTypeImageGenerationCall,
		schemas.ResponsesMessageTypeFileSearchCall,
		schemas.ResponsesMessageTypeWebSearchCall,
		schemas.ResponsesMessageTypeWebFetchCall,
		schemas.ResponsesMessageTypeComputerCall,
		schemas.ResponsesMessageTypeLocalShellCall:
		if item.ResponsesToolMessage == nil {
			return false
		}
		return hasNonEmptyString(item.ResponsesToolMessage.Name) ||
			hasNonEmptyString(item.ResponsesToolMessage.CallID) ||
			hasNonEmptyString(item.ResponsesToolMessage.Arguments) ||
			hasNonEmptyString(item.ResponsesToolMessage.Error)
	default:
		return false
	}
}

func hasNonEmptyString(value *string) bool {
	return value != nil && *value != ""
}

func estimatePreludeChunkSize(chunk *schemas.BifrostStreamChunk) int {
	if chunk == nil {
		return 0
	}

	size := 64
	if chunk.BifrostError != nil && chunk.BifrostError.Error != nil {
		size += len(chunk.BifrostError.Error.Message)
		size += lenPtr(chunk.BifrostError.Error.Code)
		size += lenPtr(chunk.BifrostError.Error.Type)
		return size
	}

	if resp := chunk.BifrostResponsesStreamResponse; resp != nil {
		size += len(string(resp.Type))
		size += lenPtr(resp.Delta)
		size += lenPtr(resp.Text)
		size += lenPtr(resp.Refusal)
		size += lenPtr(resp.Arguments)
		size += lenPtr(resp.PartialImageB64)
		size += lenPtr(resp.Code)
		size += lenPtr(resp.Message)
		return size
	}

	if resp := chunk.BifrostChatResponse; resp != nil {
		size += len(resp.ID) + len(resp.Model)
		for _, choice := range resp.Choices {
			if choice.ChatStreamResponseChoice == nil || choice.ChatStreamResponseChoice.Delta == nil {
				continue
			}
			delta := choice.ChatStreamResponseChoice.Delta
			size += lenPtr(delta.Role)
			size += lenPtr(delta.Content)
			size += lenPtr(delta.Refusal)
			size += lenPtr(delta.Reasoning)
			for _, toolCall := range delta.ToolCalls {
				size += lenPtr(toolCall.Type)
				size += lenPtr(toolCall.ID)
				size += lenPtr(toolCall.Function.Name)
				size += len(toolCall.Function.Arguments)
			}
			if delta.Audio != nil {
				size += len(delta.Audio.ID) + len(delta.Audio.Data) + len(delta.Audio.Transcript) + 16
			}
		}
		return size
	}

	if resp := chunk.BifrostTextCompletionResponse; resp != nil {
		size += len(resp.ID) + len(resp.Model)
		for _, choice := range resp.Choices {
			if choice.TextCompletionResponseChoice != nil {
				size += lenPtr(choice.TextCompletionResponseChoice.Text)
			}
		}
		return size
	}

	return size
}

func lenPtr(value *string) int {
	if value == nil {
		return 0
	}
	return len(*value)
}
