package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func TestV22StreamGateway(t *testing.T) {
	modes := []string{"headers", "body", "gemini-body", "comments", "role", "32roles", "empty", "pre-error", "long", "post-error", "tool-error", "truncated", "cancel-after", "cancel"}
	if !testing.Short() && os.Getenv("BIFROST_TEST_LONG_STREAM") == "1" {
		modes = append(modes, "over-60s")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			var primary, rescues atomic.Int32
			closed := make(chan struct{})
			root, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ Model string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				flush := func() { w.(http.Flusher).Flush() }
				delta := func(raw string) {
					fmt.Fprintf(w, "data: {\"id\":\"test\",\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", raw)
					flush()
				}
				if body.Model == "rescue" {
					rescues.Add(1)
					delta(`{"content":"rescue"}`)
					fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				primary.Add(1)
				defer close(closed)
				if mode != "headers" {
					flush()
				}
				switch mode {
				case "headers", "body", "cancel", "gemini-body":
					if mode == "cancel" {
						cancel()
					}
					select {
					case <-r.Context().Done():
					case <-time.After(4 * time.Second):
					}
				case "comments":
					for i := 0; i < 160; i++ {
						select {
						case <-r.Context().Done():
							return
						case <-time.After(5 * time.Millisecond):
							fmt.Fprint(w, ": ping\n\n")
							flush()
						}
					}
				case "role":
					delta(`{"role":"assistant"}`)
				case "32roles":
					for i := 0; i < 32; i++ {
						delta(`{"role":"assistant"}`)
					}
					select {
					case <-r.Context().Done():
					case <-time.After(3 * time.Second):
					}
				case "long", "over-60s":
					delta(`{"content":"a"}`)
					if mode == "over-60s" {
						for i := 0; i < 61; i++ {
							fmt.Fprint(w, ": ping\n\n")
							flush()
							time.Sleep(time.Second)
						}
					} else {
						time.Sleep(650 * time.Millisecond)
					}
					delta(`{"content":"b"}`)
					fmt.Fprint(w, "data: [DONE]\n\n")
				case "cancel-after":
					delta(`{"content":"a"}`)
					select {
					case <-r.Context().Done():
					case <-time.After(3 * time.Second):
					}
				case "truncated":
					delta(`{"content":"a"}`)
				case "post-error", "pre-error", "tool-error":
					if mode == "post-error" {
						delta(`{"content":"a"}`)
					}
					if mode == "tool-error" {
						delta(`{"tool_calls":[{"index":0,"id":"tool1","type":"function","function":{"name":"lookup","arguments":""}}]}`)
					}
					fmt.Fprint(w, "data: {\"error\":{\"message\":\"upstream error\"}}\n\n")
				}
			}))
			defer server.Close()
			account := NewMockAccount()
			account.AddProviderWithBaseURL(schemas.OpenAI, 1, 8, server.URL)
			if mode == "gemini-body" {
				account.AddProviderWithBaseURL(schemas.Gemini, 1, 8, server.URL)
			}
			gateway, err := Init(context.Background(), schemas.BifrostConfig{Account: account, Tracer: &schemas.NoOpTracer{}, Logger: NewDefaultLogger(schemas.LogLevelError), InitialPoolSize: 2})
			require.NoError(t, err)
			defer gateway.Shutdown()
			ctx := schemas.NewBifrostContext(root, schemas.NoDeadline)
			ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{V22: true, PrimaryHint: schemas.FallbackAttemptHint{AttemptTimeoutMs: 500}, FallbackHints: []schemas.FallbackAttemptHint{{}}, MaxTotalAttempts: 2})
			ctx.SetValue(schemas.BifrostContextKeyDisableProviderRetries, true)
			ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(6*time.Second))
			req := &schemas.BifrostChatRequest{Model: "primary", Provider: schemas.OpenAI, Input: []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("test")}}}, Fallbacks: []schemas.Fallback{{Provider: schemas.OpenAI, Model: "rescue"}}}
			if mode == "gemini-body" {
				req.Provider = schemas.Gemini
			}
			start := time.Now()
			stream, bfErr := gateway.ChatCompletionStreamRequest(ctx, req)
			if mode == "cancel" {
				require.NotNil(t, bfErr)
				require.Zero(t, rescues.Load())
				return
			}
			require.Nil(t, bfErr)
			var output strings.Builder
			tools, streamErrors := 0, 0
			for c := range stream {
				if c.BifrostError != nil {
					streamErrors++
				}
				if c.BifrostChatResponse != nil {
					for _, choice := range c.BifrostChatResponse.Choices {
						if choice.ChatStreamResponseChoice != nil && choice.Delta != nil {
							if choice.Delta.Content != nil {
								output.WriteString(*choice.Delta.Content)
								if mode == "cancel-after" {
									cancel()
								}
							}
							tools += len(choice.Delta.ToolCalls)
						}
					}
				}
			}
			require.EqualValues(t, 1, primary.Load())
			switch mode {
			case "long", "over-60s":
				require.Equal(t, "ab", output.String())
				require.Zero(t, rescues.Load())
				require.Zero(t, streamErrors)
			case "cancel-after":
				require.Equal(t, "a", output.String())
				require.Zero(t, rescues.Load())
				require.Less(t, time.Since(start), 2*time.Second)
			case "post-error", "tool-error", "truncated":
				require.Zero(t, rescues.Load())
				require.Positive(t, streamErrors)
				if mode == "tool-error" {
					require.Equal(t, 1, tools)
				} else {
					require.Equal(t, "a", output.String())
				}
			default:
				require.Equal(t, "rescue", output.String())
				require.EqualValues(t, 1, rescues.Load())
				require.Zero(t, streamErrors)
				require.Less(t, time.Since(start), 2*time.Second)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("primary connection still active")
			}
		})
	}
}

func TestV22NonStreamThreeHops(t *testing.T) {
	var calls atomic.Int32
	models := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		models <- body.Model
		if calls.Add(1) < 3 {
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"rescue","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 8, server.URL)
	gateway, err := Init(context.Background(), schemas.BifrostConfig{Account: account, Logger: NewDefaultLogger(schemas.LogLevelError), InitialPoolSize: 2})
	require.NoError(t, err)
	defer gateway.Shutdown()
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(18*time.Second))
	hints := make([]schemas.FallbackAttemptHint, 7)
	hints[0].Observe = func(ctx *schemas.BifrostContext, _ bool, _ time.Duration, err *schemas.BifrostError) {
		if err == nil || err.StatusCode == nil || *err.StatusCode != 504 {
			t.Error("second hop should time out")
		}
		// Reproduce the 16 seconds left after 22s + 20s without sleeping 42s.
		ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(16*time.Second))
	}
	ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{V22: true, PrimaryHint: schemas.FallbackAttemptHint{AttemptTimeoutMs: 200}, RescueAttemptTimeoutMs: 200, FallbackHints: hints, MaxTotalAttempts: 5})
	ctx.SetValue(schemas.BifrostContextKeyDisableProviderRetries, true)
	req := &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "primary", Input: []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("x")}}}, Fallbacks: []schemas.Fallback{{Provider: schemas.OpenAI, Model: "second"}, {Provider: schemas.OpenAI, Model: "third"}}}
	for i := 3; i <= len(hints); i++ {
		req.Fallbacks = append(req.Fallbacks, schemas.Fallback{Provider: schemas.OpenAI, Model: fmt.Sprintf("backup-%d", i)})
	}
	start := time.Now()
	resp, bfErr := gateway.ChatCompletionRequest(ctx, req)
	require.Nil(t, bfErr)
	require.Equal(t, "ok", *resp.Choices[0].Message.Content.ContentStr)
	require.EqualValues(t, 3, calls.Load())
	require.Equal(t, []string{"primary", "second", "third"}, []string{<-models, <-models, <-models})
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestV22QueueExpiry(t *testing.T) {
	b, err := Init(context.Background(), schemas.BifrostConfig{Account: NewMockAccount(), Logger: NewDefaultLogger(schemas.LogLevelError), InitialPoolSize: 1})
	require.NoError(t, err)
	defer b.Shutdown()
	for _, capacity := range []int{0, 1} {
		q := &ProviderQueue{queue: make(chan *ChannelMessage, capacity), done: make(chan struct{})}
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{V22: true})
		ctx.SetValue(schemas.BifrostContextKeyAttemptDeadline, time.Now().Add(30*time.Millisecond))
		m := b.getChannelMessage(schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest})
		m.Context = ctx
		e := b.enqueueAttempt(q, m, schemas.OpenAI, "test")
		require.NotNil(t, e)
		require.Equal(t, 504, *e.StatusCode)
		require.True(t, e.LocalQueueTimeout)
		if capacity > 0 {
			require.False(t, b.claimQueued(<-q.queue))
		}
	}
}

func TestV22RescueHonorsCallerAndLastEligibleTarget(t *testing.T) {
	root, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	ctx := schemas.NewBifrostContext(root, schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(58*time.Second))
	ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{V22: true, MaxTotalAttempts: 5, RescueAttemptTimeoutMs: 20000, FallbackHints: []schemas.FallbackAttemptHint{{}, {Available: func() bool { return false }}}})
	index, timeout := new(fallbackCursor).next(ctx, 2, 1)
	require.Equal(t, 0, index)
	require.InDelta(t, 7000, timeout, 20)
}

func TestV22RescueBudgets(t *testing.T) {
	for _, tc := range []struct {
		left, want, n int
		wantIndex     int
	}{
		{36000, 20000, 2, 0},
		{30000, 16000, 2, 0},
		{26100, 12100, 2, 0},
		{25000, 25000, 2, 0},
		{18000, 18000, 4, 0},
		{16000, 16000, 7, 0},
		{16000, 16000, 1, 0},
		{2000, 0, 1, -1},
	} {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{V22: true, RescueAttemptTimeoutMs: 20000, FallbackHints: make([]schemas.FallbackAttemptHint, tc.n), MaxTotalAttempts: 5})
		ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(time.Duration(tc.left)*time.Millisecond))
		cursor := new(fallbackCursor)
		index, timeout := cursor.next(ctx, tc.n, 1)
		require.Equal(t, tc.wantIndex, index, "left=%d", tc.left)
		require.InDelta(t, tc.want, timeout, 20, "left=%d", tc.left)
		index, _ = cursor.next(ctx, tc.n, 5)
		require.Equal(t, -1, index)
	}
}

func TestV22FinalHopPromotesHealthyLastResortWhenNoFastRescueRemains(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	p := &schemas.AdaptiveFallbackRuntimePolicy{
		V22:                    true,
		RequestStartTime:       time.Now().Add(-44 * time.Second),
		FastRescueElapsedMs:    15000,
		RescueAttemptTimeoutMs: 22000,
		MaxTotalAttempts:       5,
		FallbackHints: []schemas.FallbackAttemptHint{
			{IsFastRescue: false},                     // flaky/demoted L1
			{IsFastRescue: false, IsLastResort: true}, // slow L3 should not be promoted
			{IsFastRescue: true, IsLastResort: true},  // healthy fast L3
		},
	}
	ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, p)

	// Non-final hop (left=36s >= 26s): MUST NOT promote L3 early (strict L3 isolation)
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(36*time.Second))
	i, timeout := new(fallbackCursor).next(ctx, 3, 1)
	require.Equal(t, 0, i)
	require.InDelta(t, 22000, timeout, 25)

	// Final hop due to remaining budget (left=18s < 26s): promotes healthy L3 with full 18s (no 4s waste hop)
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(18*time.Second))
	i, timeout = new(fallbackCursor).next(ctx, 3, 2)
	require.Equal(t, 2, i)
	require.InDelta(t, 18000, timeout, 25)

	// Final hop due to MaxTotalAttempts (executed=4 of 5): promotes healthy L3
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(36*time.Second))
	i, timeout = new(fallbackCursor).next(ctx, 3, 4)
	require.Equal(t, 2, i)
	require.InDelta(t, 36000, timeout, 25)

	// If best candidate is ALREADY a proven fast-rescue channel, keep it instead of burning L3
	p.FallbackHints[0].IsFastRescue = true
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(18*time.Second))
	i, timeout = new(fallbackCursor).next(ctx, 3, 2)
	require.Equal(t, 0, i)
	require.InDelta(t, 18000, timeout, 25)
}

func TestV22ContinuationAndSlowRescue(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	p := &schemas.AdaptiveFallbackRuntimePolicy{V22: true, MaxTotalAttempts: 5, RequestStartTime: time.Now().Add(-400 * time.Millisecond), FastRescueElapsedMs: 15000, RescueAttemptTimeoutMs: 20000,
		FallbackHints: []schemas.FallbackAttemptHint{{IsFastRescue: true}, {ContinuationRank: 1, AttemptTimeoutMs: 28000}, {IsLastResort: true}}}
	ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, p)
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(57600*time.Millisecond))
	cursor := new(fallbackCursor)
	i, timeout := cursor.next(ctx, 3, 1)
	require.Equal(t, 1, i)
	require.Equal(t, 28000, timeout)
	i, _ = new(fallbackCursor).next(ctx, 3, 4)
	require.Equal(t, 0, i, "the final slot should use rescue ranking even after quick failures")
	p.RequestStartTime = time.Now().Add(-22 * time.Second)
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(36*time.Second))
	i, timeout = cursor.next(ctx, 3, 2)
	require.Equal(t, 0, i)
	require.Equal(t, 20000, timeout)
	i, timeout = new(fallbackCursor).next(ctx, 3, 1)
	require.Equal(t, 0, i)
	require.Equal(t, 20000, timeout)
	p.FallbackHints[0].Available = func() bool { return false }
	ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, time.Now().Add(16*time.Second))
	i, timeout = new(fallbackCursor).next(ctx, 3, 2)
	require.Equal(t, 1, i)
	require.InDelta(t, 16000, timeout, 20)
}

func TestV22GatewayFast503AndSlowRescue(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, elapsed := range []time.Duration{400 * time.Millisecond, 22 * time.Second} {
			t.Run(fmt.Sprintf("stream=%t/elapsed=%s", stream, elapsed), func(t *testing.T) {
				received := make(chan string, 8)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct{ Model string }
					if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
						t.Error(e)
						return
					}
					received <- body.Model
					if body.Model == "primary" {
						http.Error(w, `{"error":{"message":"unavailable"}}`, 503)
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
					}
				}))
				defer server.Close()
				account := NewMockAccount()
				account.AddProviderWithBaseURL(schemas.OpenAI, 1, 8, server.URL)
				gw, e := Init(context.Background(), schemas.BifrostConfig{Account: account, Logger: NewDefaultLogger(schemas.LogLevelError), InitialPoolSize: 2})
				require.NoError(t, e)
				defer gw.Shutdown()
				ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
				start := time.Now().Add(-elapsed)
				ctx.SetValue(schemas.BifrostContextKeyFallbackDeadline, start.Add(58*time.Second))
				ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{V22: true, RequestStartTime: start, FastRescueElapsedMs: 15000, RescueAttemptTimeoutMs: 20000, MaxTotalAttempts: 5, PrimaryHint: schemas.FallbackAttemptHint{AttemptTimeoutMs: 22000}, FallbackHints: []schemas.FallbackAttemptHint{{IsFastRescue: true}, {ContinuationRank: 1, AttemptTimeoutMs: 28000}, {IsLastResort: true}}})
				req := &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "primary", Input: []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("x")}}}}
				for _, name := range []string{"backup", "regular", "retail"} {
					req.Fallbacks = append(req.Fallbacks, schemas.Fallback{Provider: schemas.OpenAI, Model: name})
				}
				if stream {
					out, err := gw.ChatCompletionStreamRequest(ctx, req)
					require.Nil(t, err)
					for chunk := range out {
						require.Nil(t, chunk.BifrostError)
					}
				} else {
					_, err := gw.ChatCompletionRequest(ctx, req)
					require.Nil(t, err)
				}
				want := "regular"
				if elapsed >= 15*time.Second {
					want = "backup"
				}
				require.Len(t, received, 2)
				require.Equal(t, "primary", <-received)
				require.Equal(t, want, <-received)
			})
		}
	}
}

func TestV22QueueTimeoutSurvivesStreamTimer(t *testing.T) {
	entered, unblock := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-unblock:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer server.Close()
	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 4, server.URL)
	gw, e := Init(context.Background(), schemas.BifrostConfig{Account: account, Logger: NewDefaultLogger(schemas.LogLevelError), InitialPoolSize: 4})
	require.NoError(t, e)
	defer gw.Shutdown()
	request := func() *schemas.BifrostChatRequest {
		return &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "pro", Input: []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("test")}}}}
	}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		gw.ChatCompletionRequest(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), request())
	}()
	defer func() { close(unblock); <-firstDone }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	for _, stream := range []bool{true, true, false, false} {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		observed := false
		hint := schemas.FallbackAttemptHint{AttemptTimeoutMs: 40, Observe: func(_ *schemas.BifrostContext, s bool, _ time.Duration, err *schemas.BifrostError) {
			observed = true
			require.Equal(t, stream, s)
			require.NotNil(t, err)
			require.True(t, err.LocalQueueTimeout)
		}}
		ctx.SetValue(schemas.BifrostContextKeyAdaptiveFallbackPolicy, &schemas.AdaptiveFallbackRuntimePolicy{V22: true, PrimaryHint: hint})
		var err *schemas.BifrostError
		if stream {
			_, err = gw.ChatCompletionStreamRequest(ctx, request())
		} else {
			_, err = gw.ChatCompletionRequest(ctx, request())
		}
		require.NotNil(t, err)
		require.Equal(t, 504, *err.StatusCode)
		require.True(t, observed)
	}
	require.EqualValues(t, 1, calls.Load())
}
