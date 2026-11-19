package governance

import (
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

func TestClassifyOutcome_FastSuccess(t *testing.T) {
	assert.Equal(t, OutcomeSuccess, ClassifyOutcome(nil, 2*time.Second, 45*time.Second))
}

func TestClassifyOutcome_SlowSuccess(t *testing.T) {
	assert.Equal(t, OutcomeSlow, ClassifyOutcome(nil, 46*time.Second, 45*time.Second))
}

func TestClassifyOutcome_429IsSoftFail(t *testing.T) {
	assert.Equal(t, OutcomeSoftFail, ClassifyOutcome(testBifrostError(429, "rate limited"), 0, 45*time.Second))
	assert.Equal(t, OutcomeSoftFail, ClassifyOutcome(testBifrostErrorWithCode(429, "upstream_error", "429", "You exceeded your current quota, please check your plan and billing details."), 0, 45*time.Second))
}

func TestClassifyOutcome_TimeoutIsSoftFail(t *testing.T) {
	assert.Equal(t, OutcomeSoftFail, ClassifyOutcome(testBifrostError(504, "upstream timeout"), 0, 45*time.Second))
}

func TestClassifyOutcome_QuotaIsSoftFail(t *testing.T) {
	assert.Equal(t, OutcomeSoftFail, ClassifyOutcome(testBifrostError(400, "quota exhausted"), 0, 45*time.Second))
}

func TestClassifyOutcome_ContextLengthExceededIsSoftFail(t *testing.T) {
	assert.Equal(t, OutcomeSoftFail, ClassifyOutcome(testBifrostError(400, "context_length_exceeded"), 0, 45*time.Second))
}

func TestClassifyOutcome_Unknown5xxIsHardFail(t *testing.T) {
	assert.Equal(t, OutcomeHardFail, ClassifyOutcome(testBifrostError(500, "unknown upstream error"), 0, 45*time.Second))
}

func TestClassifyOutcome_Normal4xxDoesNotPolluteHealth(t *testing.T) {
	assert.Equal(t, OutcomeIgnore, ClassifyOutcome(testBifrostError(400, "invalid request"), 0, 45*time.Second))
}

func TestClassifyOutcome_ClientCancellationIsIgnored(t *testing.T) {
	assert.Equal(t, OutcomeIgnore, ClassifyOutcome(testBifrostErrorWithCode(499, schemas.RequestCancelled, "", "Request cancelled by context: context canceled"), 0, 45*time.Second))
	assert.Equal(t, OutcomeIgnore, ClassifyOutcome(testBifrostError(499, "Request cancelled: client disconnected"), 0, 45*time.Second))
}

func TestClassifyOutcome_FatalChannelErrors(t *testing.T) {
	cases := []struct {
		name string
		err  *schemas.BifrostError
	}{
		{
			name: "B.AI 400 insufficient balance with code",
			err:  testBifrostErrorWithCode(400, "api_error", "insufficient_user_quota", "credit insufficient balance: balance=9590 required=25328"),
		},
		{
			name: "NightRouter 403 no active subscription",
			err:  testBifrostErrorWithCode(403, "new_api_error", "insufficient_user_quota", "You have no active subscription"),
		},
		{
			name: "NightRouter 403 banned",
			err:  testBifrostErrorWithCode(403, "new_api_error", "", "User has been banned"),
		},
		{
			name: "NightRouter 401 invalid token",
			err:  testBifrostErrorWithCode(401, "new_api_error", "", "Invalid token"),
		},
		{
			name: "Polo 404 entity not found",
			err:  testBifrostErrorWithCode(404, "upstream_error", "404", "Requested entity was not found."),
		},
		{
			name: "Polo 503 no available channel",
			err:  testBifrostErrorWithCode(503, "new_api_error", "model_not_found", "No available channel for model gemini-3.1-pro-preview"),
		},
		{
			name: "Polo 503 auth_unavailable",
			err:  testBifrostErrorWithCode(503, "server_error", "internal_server_error", "auth_unavailable: no auth available"),
		},
		{
			name: "StoreAPI 503 fallback_skip_json",
			err:  testBifrostErrorWithCode(503, "invalid_request_error", "fallback_skip_json", "this route does not support JSON mode"),
		},
		{
			name: "Yunwu 403 local insufficient quota",
			err:  testBifrostErrorWithCode(403, "new_api_error", "local:insufficient_quota", "user quota is not enough"),
		},
		{
			name: "OpenRouter 402 payment required",
			err:  testBifrostErrorWithCode(402, "", "402", "This request requires more credits"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, OutcomeFatalChannel, ClassifyOutcome(tc.err, 0, 45*time.Second))
		})
	}
}

func TestClassifyOutcome_Transient503OverloadStaysSoftFail(t *testing.T) {
	err := testBifrostErrorWithCode(503, "api_error", "model_temporarily_unavailable", "The model service is temporarily unavailable. Please try again later.")
	assert.Equal(t, OutcomeSoftFail, ClassifyOutcome(err, 0, 45*time.Second))
}

func testBifrostError(status int, message string) *schemas.BifrostError {
	return &schemas.BifrostError{
		StatusCode: bifrost.Ptr(status),
		Error: &schemas.ErrorField{
			Message: message,
		},
	}
}

func testBifrostErrorWithCode(status int, errType, code, message string) *schemas.BifrostError {
	field := &schemas.ErrorField{
		Message: message,
	}
	if errType != "" {
		field.Type = bifrost.Ptr(errType)
	}
	if code != "" {
		field.Code = bifrost.Ptr(code)
	}
	return &schemas.BifrostError{
		StatusCode: bifrost.Ptr(status),
		Error:      field,
	}
}
