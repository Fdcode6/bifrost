# 流式请求首个有效片段前 Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `TextCompletionStreamRequest`、`ChatCompletionStreamRequest`、`ResponsesStreamRequest` 在首个有效输出前报错时继续走现有 fallback，同时不把失败线路的前导事件泄露给客户端。

**Architecture:** 继续把 `executeRequestWithRetries()` 作为唯一的同步错误入口，不改 provider 实现，也不新建第二套 fallback 管道。核心改动集中在 `core/providers/utils/stream.go`：把“检查第一个 chunk”升级成“观察到首个有效片段或错误”为止；`core/bifrost.go` 只保留调用点与收尾行为，确保失败 attempt 仍完成清理、日志和健康度统计。

**Tech Stack:** Go, Bifrost core streaming, SSE stream helpers, Go testing, local k8s, Docker

---

## Completion Standard

- `Responses`、`Chat`、`Text` 三类流都支持“首个有效片段前失败继续 fallback”
- 客户端只看到最终成功线路的流，不看到失败线路的前导事件或错误事件
- 失败线路仍保留原有失败语义：
  - 会进入现有 retry / fallback 判断
  - 会保留日志、health tracking、fallback metadata
- 已开始输出有效内容后再失败，保持现有行为，不做中途续切
- 空流直接关闭保持非错误语义，不新增 fallback
- 现有 timeout 语义不变，不新增独立 timeout 机制
- `go test ./core/providers/utils -count=1`
- `go test ./core -run 'TestExecuteRequestWithRetries_|TestHandleStreamRequest_' -count=1`
- 本地镜像构建成功，并在本地多 K 环境完成至少一组真实模拟验证

## File Map

- Modify: `core/providers/utils/stream.go`
  - 扩展首段流检查逻辑，新增“有效片段”判断
- Modify: `core/providers/utils/stream_test.go`
  - 覆盖 helper 级边界场景
- Modify: `core/bifrost.go`
  - 继续复用统一调用点，必要时微调注释或资源清理顺序
- Modify: `core/bifrost_test.go`
  - 覆盖 executeRequestWithRetries 在流式场景的同步失败 / fallback 入口
- Create: `core/bifrost_stream_fallback_test.go`
  - 覆盖更贴近真实语义的流式 fallback 场景
- Modify: `docs/superpowers/specs/2026-04-16-stream-pre-effective-fallback-design.md`
  - 如实现阶段发现必须收敛的边界，回写到设计稿

### Task 1: 先把 helper 级失败测试补齐

**Files:**
- Modify: `core/providers/utils/stream_test.go`

- [ ] **Step 1: 写 Responses 前导事件后报错的失败测试**

```go
func TestCheckFirstStreamChunk_ResponsesLifecycleThenError(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 4)
	stream <- &schemas.BifrostStreamChunk{
		BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
			Type: schemas.ResponsesStreamResponseTypeCreated,
		},
	}
	stream <- &schemas.BifrostStreamChunk{
		BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
			Type: schemas.ResponsesStreamResponseTypeInProgress,
		},
	}
	stream <- &schemas.BifrostStreamChunk{
		BifrostError: &schemas.BifrostError{
			Error: &schemas.ErrorField{Message: "upstream timeout"},
		},
	}
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
```

- [ ] **Step 2: 跑这个测试，确认当前实现会失败**

Run: `go test ./core/providers/utils -run 'TestCheckFirstStreamChunk_ResponsesLifecycleThenError' -count=1`
Expected: FAIL，因为当前实现只看第一个 chunk

- [ ] **Step 3: 再补 Chat / Text 关键边界测试**

```go
func TestCheckFirstStreamChunk_RoleOnlyThenError(t *testing.T) { /* role -> empty delta -> error => fallback */ }

func TestCheckFirstStreamChunk_RoleOnlyThenContent(t *testing.T) { /* role -> empty delta -> content => success */ }

func TestCheckFirstStreamChunk_ErrorAfterEffectiveChunk(t *testing.T) { /* content emitted before error => no fallback */ }

func TestCheckFirstStreamChunk_BufferLimitBeforeEffectiveChunk(t *testing.T) { /* too many prelude chunks => commit current stream */ }

func TestCheckFirstStreamChunk_EmptyClosedStreamStaysNonError(t *testing.T) { /* closed before any chunk => nil,nil */ }
```

- [ ] **Step 4: 跑 helper 测试组，确认至少新增场景先失败**

Run: `go test ./core/providers/utils -run 'TestCheckFirstStreamChunk_' -count=1`
Expected: FAIL in new tests, PASS in unchanged old tests

### Task 2: 给 executeRequestWithRetries 补流式 fallback 入口测试

**Files:**
- Modify: `core/bifrost_test.go`

- [ ] **Step 1: 写“首个有效片段前错误会触发重试”的失败测试**

```go
func TestExecuteRequestWithRetries_StreamRetriesBeforeEffectiveChunk(t *testing.T) {
	config := createTestConfig(1, 100*time.Millisecond, time.Second)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyTracer, &schemas.NoOpTracer{})
	logger := NewDefaultLogger(schemas.LogLevelError)

	callCount := 0
	handler := func() (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
		callCount++
		stream := make(chan *schemas.BifrostStreamChunk, 3)
		if callCount == 1 {
			stream <- &schemas.BifrostStreamChunk{
				BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
					Type: schemas.ResponsesStreamResponseTypeCreated,
				},
			}
			stream <- &schemas.BifrostStreamChunk{
				BifrostError: &schemas.BifrostError{
					StatusCode: Ptr(504),
					Error: &schemas.ErrorField{Message: "gateway timeout"},
				},
			}
			close(stream)
			return stream, nil
		}
		stream <- &schemas.BifrostStreamChunk{
			BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
				Type:  schemas.ResponsesStreamResponseTypeOutputTextDelta,
				Delta: Ptr("ok"),
			},
		}
		close(stream)
		return stream, nil
	}

	result, err := executeRequestWithRetries(
		ctx, config, handler, schemas.ResponsesStreamRequest, schemas.OpenAI, "gpt-4.1", nil, logger,
	)
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected retry after pre-effective error, got %d calls", callCount)
	}
	first := <-result
	if first == nil || first.BifrostResponsesStreamResponse == nil || first.BifrostResponsesStreamResponse.Delta == nil || *first.BifrostResponsesStreamResponse.Delta != "ok" {
		t.Fatalf("expected retried stream payload, got %#v", first)
	}
}
```

- [ ] **Step 2: 写“有效输出后报错不触发重试”的测试**

```go
func TestExecuteRequestWithRetries_StreamDoesNotRetryAfterEffectiveChunk(t *testing.T) {
	// first attempt emits content delta, then error chunk
	// expect no retry and wrapped stream still contains the error
}
```

- [ ] **Step 3: 跑流式重试测试，确认新增用例先失败**

Run: `go test ./core -run 'TestExecuteRequestWithRetries_Stream' -count=1`
Expected: FAIL before implementation

### Task 3: 实现“首个有效片段前观察器”

**Files:**
- Modify: `core/providers/utils/stream.go`

- [ ] **Step 1: 为 helper 增加有效片段判断函数**

```go
func isEffectiveStreamChunk(chunk *schemas.BifrostStreamChunk) bool {
	switch {
	case chunk == nil:
		return false
	case chunk.BifrostChatResponse != nil:
		return isEffectiveChatChunk(chunk.BifrostChatResponse)
	case chunk.BifrostTextCompletionResponse != nil:
		return isEffectiveTextChunk(chunk.BifrostTextCompletionResponse)
	case chunk.BifrostResponsesStreamResponse != nil:
		return isEffectiveResponsesChunk(chunk.BifrostResponsesStreamResponse)
	default:
		return true
	}
}
```

- [ ] **Step 2: 把 helper 从“只看第一条”改成“观察到有效片段或错误”为止**

```go
func CheckFirstStreamChunkForError(stream chan *schemas.BifrostStreamChunk) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	// 读 stream
	// 缓存最多 8 个 chunk、约 32KB
	// 如果缓存阶段遇到终止错误 => drain source and return sync error
	// 如果遇到有效片段 => 把缓存和后续转发出去
	// 如果超出缓存阈值 => 直接提交当前流，避免无限前导等待
}
```

- [ ] **Step 3: 让 Responses / Chat / Text 的有效判定严格按设计稿**

```go
var effectiveResponsesTypes = map[schemas.ResponsesStreamResponseType]struct{}{
	schemas.ResponsesStreamResponseTypeOutputTextDelta: {},
	schemas.ResponsesStreamResponseTypeRefusalDelta: {},
	schemas.ResponsesStreamResponseTypeReasoningSummaryTextDelta: {},
	schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta: {},
	schemas.ResponsesStreamResponseTypeMCPCallArgumentsDelta: {},
	schemas.ResponsesStreamResponseTypeCustomToolCallInputDelta: {},
	schemas.ResponsesStreamResponseTypeCodeInterpreterCallCodeDelta: {},
	schemas.ResponsesStreamResponseTypeImageGenerationCallPartialImage: {},
}
```

- [ ] **Step 4: 跑 helper 测试，确认全部通过**

Run: `go test ./core/providers/utils -run 'TestCheckFirstStreamChunk_' -count=1`
Expected: PASS

### Task 4: 接回统一重试入口并验证边界不回归

**Files:**
- Modify: `core/bifrost.go`
- Modify: `core/bifrost_test.go`

- [ ] **Step 1: 只在必要处微调 `executeRequestWithRetries()` 注释或收尾逻辑**

```go
if firstChunkErr != nil {
	<-drainDone
	bifrostError = firstChunkErr
} else {
	result = any(checkedStream).(T)
}
```

- [ ] **Step 2: 增加“空流保持非错误”和“错误仍受原有 retry gate 控制”的测试断言**

```go
func TestExecuteRequestWithRetries_StreamClosedBeforeChunks(t *testing.T) { /* expect nil stream, nil error */ }

func TestExecuteRequestWithRetries_StreamPreEffectiveNonRetryableError(t *testing.T) { /* 400 error => no retry */ }
```

- [ ] **Step 3: 跑核心流式相关单测**

Run: `go test ./core -run 'TestExecuteRequestWithRetries_|TestHandleStreamRequest_' -count=1`
Expected: PASS

### Task 5: 补贴近真实语义的回归测试

**Files:**
- Create: `core/bifrost_stream_fallback_test.go`

- [ ] **Step 1: 写“首路失败、次路成功，客户端只看到成功流”的回归测试**

```go
func TestStreamFallbackBeforeEffectiveChunk_ClientOnlySeesWinningAttempt(t *testing.T) {
	// mock first attempt: created -> in_progress -> retryable error
	// mock second attempt: output_text.delta -> completed
	// assert client stream only contains second attempt payload
}
```

- [ ] **Step 2: 写“有效输出后失败，不做中途续切”的回归测试**

```go
func TestStreamFallbackAfterEffectiveChunk_DoesNotSwitchAttempts(t *testing.T) {
	// first attempt already emitted content, later error should stay in same stream
}
```

- [ ] **Step 3: 跑完整 core 测试范围**

Run: `go test ./core/... -count=1`
Expected: PASS

### Task 6: 本地构建、部署到多 K、模拟真实场景

**Files:**
- Modify: `docs/superpowers/specs/2026-04-16-stream-pre-effective-fallback-design.md`（仅当实现边界有必要收口）

- [ ] **Step 1: 构建本地镜像**

Run: `make build`
Expected: build success

- [ ] **Step 2: 构建或刷新本地镜像标签**

Run: `docker build -t bifrost:stream-pre-effective-fallback-local .`
Expected: image build success

- [ ] **Step 3: 部署到本地多 K 环境**

Run: `make -f recipes/local-k8s.mk deploy-local-k8s-helm`
Expected: pods become ready

- [ ] **Step 4: 构造真实模拟**

```text
上游 1：先返回 response.created / response.in_progress，再返回可重试错误
上游 2：正常返回 output_text.delta
验证：
- 客户端收到的是上游 2 的流
- 日志里存在失败 attempt + 成功 attempt
- 健康度记录首路失败
```

- [ ] **Step 5: 记录验证结论并补修发现的问题**

Run: `kubectl get pods -A | grep bifrost`
Expected: local k8s service healthy

Run: `go test ./core/... -count=1`
Expected: PASS after final fixes

### Task 7: 子代理复核

**Files:**
- No code changes required unless review finds issues

- [ ] **Step 1: 发起一轮功能一致性复核**
  - 检查是否完全符合设计稿
  - 检查客户端可见行为是否干净

- [ ] **Step 2: 发起一轮边界与副作用复核**
  - 检查 trace、日志、health、retry gate、drain 行为

- [ ] **Step 3: 修掉复核发现的问题后，再跑最终验证**

Run: `go test ./core/... -count=1`
Expected: PASS
