package utils

import (
	schemas "github.com/maximhq/bifrost/core/schemas"
)

const (
	maxBufferedPreludeChunks = 8
	maxBufferedPreludeBytes  = 32 * 1024
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
	buffered := make([]*schemas.BifrostStreamChunk, 0, max(1, min(cap(stream), maxBufferedPreludeChunks)))
	bufferedBytes := 0

	for {
		chunk, ok := <-stream
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
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range stream {
				}
			}()
			return nil, done, bifrostErr
		}

		buffered = append(buffered, chunk)
		bufferedBytes += estimatePreludeChunkSize(chunk)

		if isEffectiveStreamChunk(chunk) || len(buffered) >= maxBufferedPreludeChunks || bufferedBytes >= maxBufferedPreludeBytes {
			return wrapObservedStream(buffered, stream)
		}
	}
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
