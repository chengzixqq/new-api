package ali

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPollAliTaskCountsNetworkFailuresAndStops(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/", nil)
	var attempts atomic.Int32
	fetchErr := errors.New("temporary network failure")

	_, _, err := pollAliTask(ctx, &relaycommon.RelayInfo{}, "task", 0, time.Millisecond, 3,
		func(context.Context, *relaycommon.RelayInfo, string) (*AliResponse, error, []byte) {
			attempts.Add(1)
			return nil, fetchErr, nil
		})

	require.ErrorIs(t, err, fetchErr)
	assert.Equal(t, int32(3), attempts.Load())
}

func TestPollAliTaskCancellationStopsTimerAndFetch(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/", nil).WithContext(requestCtx)
	var attempts atomic.Int32

	_, _, err := pollAliTask(ctx, &relaycommon.RelayInfo{}, "task", time.Hour, time.Millisecond, 3,
		func(context.Context, *relaycommon.RelayInfo, string) (*AliResponse, error, []byte) {
			attempts.Add(1)
			return nil, nil, nil
		})

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, attempts.Load())
}
