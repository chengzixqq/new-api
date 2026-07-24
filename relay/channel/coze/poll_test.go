package coze

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPollCozeChatCountsTransientFailures(t *testing.T) {
	var attempts atomic.Int32
	transientErr := errors.New("temporary network failure")

	err := pollCozeChat(context.Background(), 3, time.Millisecond, func() (error, bool) {
		attempts.Add(1)
		return transientErr, false
	})

	require.ErrorIs(t, err, transientErr)
	assert.Equal(t, int32(3), attempts.Load())
}

func TestPollCozeChatStopsOnTerminalFailure(t *testing.T) {
	var attempts atomic.Int32
	err := pollCozeChat(context.Background(), 3, time.Millisecond, func() (error, bool) {
		attempts.Add(1)
		return errCozeTerminalStatus, false
	})

	require.ErrorIs(t, err, errCozeTerminalStatus)
	assert.Equal(t, int32(1), attempts.Load())
}

func TestPollCozeChatCancellationStopsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var attempts atomic.Int32

	err := pollCozeChat(ctx, 3, time.Hour, func() (error, bool) {
		attempts.Add(1)
		return nil, false
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), attempts.Load())
}
