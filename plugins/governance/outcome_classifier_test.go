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
	assert.Equal(t, OutcomeSuccess, ClassifyOutcome(testBifrostError(400, "invalid request"), 0, 45*time.Second))
}

func testBifrostError(status int, message string) *schemas.BifrostError {
	return &schemas.BifrostError{
		StatusCode: bifrost.Ptr(status),
		Error: &schemas.ErrorField{
			Message: message,
		},
	}
}
