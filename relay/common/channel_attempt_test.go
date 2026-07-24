package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelAttemptSnapshotCapturesDispatchDimensionsAndConsumesOnce(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "MODEL",
		UsingGroup:      "CC-MAX",
		IsStream:        true,
		isFirstResponse: true,
		ChannelMeta:     &ChannelMeta{ChannelId: 84},
	}
	info.MarkChannelAttemptDispatched()
	time.Sleep(time.Millisecond)
	info.SetFirstResponseTime()

	attempt, ok := info.TakeChannelAttempt()
	require.True(t, ok)
	assert.Equal(t, 84, attempt.ChannelID)
	assert.Equal(t, "MODEL", attempt.ModelName)
	assert.Equal(t, "CC-MAX", attempt.Group)
	assert.True(t, attempt.IsStream)
	assert.True(t, attempt.FirstResponseAt.After(attempt.StartedAt))

	_, ok = info.TakeChannelAttempt()
	assert.False(t, ok)
}

func TestChannelAttemptSnapshotFreezesRetryChannel(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "MODEL",
		UsingGroup:      "GROUP-A",
		ChannelMeta:     &ChannelMeta{ChannelId: 84},
	}
	info.MarkChannelAttemptDispatched()
	info.UsingGroup = "GROUP-B"
	info.ChannelMeta = &ChannelMeta{ChannelId: 58}

	attempt, ok := info.TakeChannelAttempt()
	require.True(t, ok)
	assert.Equal(t, 84, attempt.ChannelID)
	assert.Equal(t, "GROUP-A", attempt.Group)
}

func TestChannelAttemptCapturesFirstResponseOnLaterRetry(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "MODEL", UsingGroup: "GROUP", IsStream: true,
		isFirstResponse: true, ChannelMeta: &ChannelMeta{ChannelId: 84},
	}
	info.MarkChannelAttemptDispatched()
	info.SetFirstResponseTime()
	_, ok := info.TakeChannelAttempt()
	require.True(t, ok)

	info.ChannelMeta = &ChannelMeta{ChannelId: 58}
	info.MarkChannelAttemptDispatched()
	time.Sleep(time.Millisecond)
	info.SetFirstResponseTime()
	second, ok := info.TakeChannelAttempt()
	require.True(t, ok)
	assert.Equal(t, 58, second.ChannelID)
	assert.True(t, second.FirstResponseAt.After(second.StartedAt))
}
