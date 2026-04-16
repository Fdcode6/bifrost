package utils

import (
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func newErrorChunk(message string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{
		BifrostError: &schemas.BifrostError{
			Error: &schemas.ErrorField{
				Message: message,
			},
		},
	}
}

func newChatChunk(delta *schemas.ChatStreamResponseChoiceDelta, finishReason *string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{
		BifrostChatResponse: &schemas.BifrostChatResponse{
			ID: "chatcmpl-123",
			Choices: []schemas.BifrostResponseChoice{
				{
					Index: 0,
					ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
						Delta: delta,
					},
					FinishReason: finishReason,
				},
			},
		},
	}
}

func newTextChunk(text *string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{
		BifrostTextCompletionResponse: &schemas.BifrostTextCompletionResponse{
			ID: "cmpl-123",
			Choices: []schemas.BifrostResponseChoice{
				{
					Index: 0,
					TextCompletionResponseChoice: &schemas.TextCompletionResponseChoice{
						Text: text,
					},
				},
			},
		},
	}
}

func newResponsesChunk(respType schemas.ResponsesStreamResponseType, delta *string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{
		BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
			Type:  respType,
			Delta: delta,
		},
	}
}

func newResponsesOutputItemAddedFunctionCallChunk(name, callID string) *schemas.BifrostStreamChunk {
	itemType := schemas.ResponsesMessageTypeFunctionCall
	return &schemas.BifrostStreamChunk{
		BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
			Type: schemas.ResponsesStreamResponseTypeOutputItemAdded,
			Item: &schemas.ResponsesMessage{
				Type: &itemType,
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					Name:   &name,
					CallID: &callID,
				},
			},
		},
	}
}

func newResponsesOutputTextDoneChunk(text string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{
		BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
			Type: schemas.ResponsesStreamResponseTypeOutputTextDone,
			Text: &text,
		},
	}
}

func collectChunks(stream chan *schemas.BifrostStreamChunk) []*schemas.BifrostStreamChunk {
	var chunks []*schemas.BifrostStreamChunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func TestCheckFirstStreamChunk_ErrorInFirstChunk(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 2)
	stream <- &schemas.BifrostStreamChunk{
		BifrostError: &schemas.BifrostError{
			Error: &schemas.ErrorField{
				Code:    schemas.Ptr("limit_burst_rate"),
				Message: "Request rate increased too quickly",
			},
		},
	}
	close(stream)

	_, drainDone, err := CheckFirstStreamChunkForError(stream)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	<-drainDone
	if err.Error.Message != "Request rate increased too quickly" {
		t.Errorf("unexpected error message: %s", err.Error.Message)
	}
	if err.Error.Code == nil || *err.Error.Code != "limit_burst_rate" {
		t.Errorf("unexpected error code: %v", err.Error.Code)
	}
}

func TestCheckFirstStreamChunk_ValidFirstChunk(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 3)
	text := "hello"
	chunk1 := newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Content: &text}, nil)
	chunk2 := newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Content: &text}, nil)
	stream <- chunk1
	stream <- chunk2
	close(stream)

	wrapped, _, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First chunk should be re-injected
	got1 := <-wrapped
	if got1.BifrostChatResponse == nil || got1.BifrostChatResponse.ID != "chatcmpl-123" {
		t.Error("first chunk not re-injected correctly")
	}

	// Second chunk should follow
	got2 := <-wrapped
	if got2.BifrostChatResponse == nil || got2.BifrostChatResponse.ID != "chatcmpl-123" {
		t.Error("second chunk not forwarded correctly")
	}

	// Channel should be closed
	_, ok := <-wrapped
	if ok {
		t.Error("expected wrapped channel to be closed")
	}
}

func TestCheckFirstStreamChunk_EmptyStream(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk)
	close(stream)

	wrapped, drainDone, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty stream should return nil channel
	if wrapped != nil {
		t.Error("expected nil channel for empty stream")
	}

	// drainDone should be already closed
	select {
	case <-drainDone:
	default:
		t.Error("expected drainDone to be closed for empty stream")
	}
}

func TestCheckFirstStreamChunk_ErrorInSecondChunk(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 3)
	text := "hello"
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Content: &text}, nil)
	stream <- newErrorChunk("some error in second chunk")
	close(stream)

	// Should NOT return error — only first chunk matters for retry
	wrapped, _, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read all chunks
	got1 := <-wrapped
	if got1.BifrostChatResponse == nil {
		t.Error("first chunk should be valid data")
	}
	got2 := <-wrapped
	if got2.BifrostError == nil {
		t.Error("second chunk should be the error")
	}

	_, ok := <-wrapped
	if ok {
		t.Error("expected wrapped channel to be closed")
	}
}

func TestCheckFirstStreamChunk_ErrorDrainsSource(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 5)
	stream <- newErrorChunk("rate limit error")
	// Add more chunks that should be drained
	text := "hello"
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Content: &text}, nil)
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Content: &text}, nil)
	close(stream)

	_, drainDone, err := CheckFirstStreamChunkForError(stream)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	<-drainDone
	if err.Error.Message != "rate limit error" {
		t.Errorf("unexpected error message: %s", err.Error.Message)
	}
	if drainDone == nil {
		t.Fatal("expected drainDone channel, got nil")
	}
	// Wait for drain to complete — verifies the channel signals properly
	<-drainDone
}

func TestCheckFirstStreamChunk_ErrorWithEmptyMessage(t *testing.T) {
	// Error with empty message and no code/type should NOT be treated as an error
	stream := make(chan *schemas.BifrostStreamChunk, 2)
	stream <- &schemas.BifrostStreamChunk{
		BifrostError: &schemas.BifrostError{
			Error: &schemas.ErrorField{
				Message: "",
			},
		},
	}
	close(stream)

	wrapped, _, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("unexpected error for empty message: %v", err)
	}
	// Should be treated as valid chunk
	<-wrapped
}

func TestCheckFirstStreamChunk_CodeOnlyError(t *testing.T) {
	// Error with code but no message should be treated as an error
	stream := make(chan *schemas.BifrostStreamChunk, 2)
	stream <- &schemas.BifrostStreamChunk{
		BifrostError: &schemas.BifrostError{
			Error: &schemas.ErrorField{
				Code: schemas.Ptr("limit_burst_rate"),
			},
		},
	}
	close(stream)

	_, drainDone, err := CheckFirstStreamChunkForError(stream)
	if err == nil {
		t.Fatal("expected error for code-only error, got nil")
	}
	<-drainDone
	if err.Error.Code == nil || *err.Error.Code != "limit_burst_rate" {
		t.Errorf("unexpected error code: %v", err.Error.Code)
	}
}

func TestCheckFirstStreamChunk_ResponsesLifecycleThenError(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 4)
	stream <- newResponsesChunk(schemas.ResponsesStreamResponseTypeCreated, nil)
	stream <- newResponsesChunk(schemas.ResponsesStreamResponseTypeInProgress, nil)
	stream <- newErrorChunk("upstream timeout")
	close(stream)

	wrapped, drainDone, err := CheckFirstStreamChunkForError(stream)
	if wrapped != nil {
		t.Fatal("expected wrapped stream to be nil when error happens before effective chunk")
	}
	if err == nil || err.Error == nil || err.Error.Message != "upstream timeout" {
		t.Fatalf("expected upstream timeout error, got %#v", err)
	}
	<-drainDone
}

func TestCheckFirstStreamChunk_RoleOnlyThenError(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 4)
	role := string(schemas.ChatMessageRoleAssistant)
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Role: &role}, nil)
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{}, nil)
	stream <- newErrorChunk("stream closed")
	close(stream)

	wrapped, drainDone, err := CheckFirstStreamChunkForError(stream)
	if wrapped != nil {
		t.Fatal("expected wrapped stream to be nil when error happens before effective chat delta")
	}
	if err == nil || err.Error == nil || err.Error.Message != "stream closed" {
		t.Fatalf("expected stream closed error, got %#v", err)
	}
	<-drainDone
}

func TestCheckFirstStreamChunk_RoleOnlyThenContent(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 4)
	role := string(schemas.ChatMessageRoleAssistant)
	text := "hello"
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Role: &role}, nil)
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{}, nil)
	stream <- newChatChunk(&schemas.ChatStreamResponseChoiceDelta{Content: &text}, nil)
	close(stream)

	wrapped, _, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunks := collectChunks(wrapped)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 forwarded chunks, got %d", len(chunks))
	}
	if chunks[2].BifrostChatResponse == nil ||
		chunks[2].BifrostChatResponse.Choices[0].ChatStreamResponseChoice == nil ||
		chunks[2].BifrostChatResponse.Choices[0].ChatStreamResponseChoice.Delta == nil ||
		chunks[2].BifrostChatResponse.Choices[0].ChatStreamResponseChoice.Delta.Content == nil ||
		*chunks[2].BifrostChatResponse.Choices[0].ChatStreamResponseChoice.Delta.Content != "hello" {
		t.Fatalf("expected content chunk to be preserved, got %#v", chunks[2])
	}
}

func TestCheckFirstStreamChunk_TextPreludeThenError(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 3)
	stream <- newTextChunk(nil)
	stream <- newErrorChunk("provider timeout")
	close(stream)

	wrapped, drainDone, err := CheckFirstStreamChunkForError(stream)
	if wrapped != nil {
		t.Fatal("expected wrapped stream to be nil when text stream errors before effective text")
	}
	if err == nil || err.Error == nil || err.Error.Message != "provider timeout" {
		t.Fatalf("expected provider timeout error, got %#v", err)
	}
	<-drainDone
}

func TestCheckFirstStreamChunk_BufferLimitBeforeEffectiveChunk(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 16)
	for i := 0; i < 9; i++ {
		stream <- newResponsesChunk(schemas.ResponsesStreamResponseTypeCreated, nil)
	}
	stream <- newErrorChunk("late timeout")
	close(stream)

	wrapped, _, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("expected stream to commit once buffer limit is reached, got %v", err)
	}
	if wrapped == nil {
		t.Fatal("expected wrapped stream after buffer limit is reached")
	}

	chunks := collectChunks(wrapped)
	if len(chunks) != 10 {
		t.Fatalf("expected all buffered chunks plus terminal error, got %d", len(chunks))
	}
	if chunks[len(chunks)-1].BifrostError == nil {
		t.Fatalf("expected terminal error chunk to remain in stream, got %#v", chunks[len(chunks)-1])
	}
}

func TestCheckFirstStreamChunk_ResponsesFunctionCallAddedThenError(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 3)
	stream <- newResponsesOutputItemAddedFunctionCallChunk("search_docs", "call-1")
	stream <- newErrorChunk("tool stream failed")
	close(stream)

	wrapped, _, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("expected function-call output_item.added to commit stream, got %v", err)
	}
	if wrapped == nil {
		t.Fatal("expected wrapped stream after function-call output item was emitted")
	}

	chunks := collectChunks(wrapped)
	if len(chunks) != 2 {
		t.Fatalf("expected output_item.added plus error, got %d chunks", len(chunks))
	}
	if chunks[0].BifrostResponsesStreamResponse == nil || chunks[0].BifrostResponsesStreamResponse.Type != schemas.ResponsesStreamResponseTypeOutputItemAdded {
		t.Fatalf("expected first chunk to be output_item.added, got %#v", chunks[0])
	}
	if chunks[1].BifrostError == nil {
		t.Fatalf("expected terminal error chunk to remain in stream, got %#v", chunks[1])
	}
}

func TestCheckFirstStreamChunk_ResponsesOutputTextDoneThenError(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 3)
	stream <- newResponsesOutputTextDoneChunk("hello")
	stream <- newErrorChunk("stream ended after text")
	close(stream)

	wrapped, _, err := CheckFirstStreamChunkForError(stream)
	if err != nil {
		t.Fatalf("expected output_text.done with text to commit stream, got %v", err)
	}
	if wrapped == nil {
		t.Fatal("expected wrapped stream after output_text.done")
	}

	chunks := collectChunks(wrapped)
	if len(chunks) != 2 {
		t.Fatalf("expected output_text.done plus error, got %d chunks", len(chunks))
	}
	if chunks[0].BifrostResponsesStreamResponse == nil || chunks[0].BifrostResponsesStreamResponse.Type != schemas.ResponsesStreamResponseTypeOutputTextDone {
		t.Fatalf("expected first chunk to be output_text.done, got %#v", chunks[0])
	}
	if chunks[1].BifrostError == nil {
		t.Fatalf("expected terminal error chunk to remain in stream, got %#v", chunks[1])
	}
}
