package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestResolveMaxRetries(t *testing.T) {
	original := common.RetryTimes
	t.Cleanup(func() { common.RetryTimes = original })

	common.RetryTimes = 3
	require.Equal(t, 3, ResolveMaxRetries(nil))
	require.Equal(t, 0, ResolveMaxRetries(intPointer(0)))
	require.Equal(t, 7, ResolveMaxRetries(intPointer(7)))
	require.Equal(t, 10, ResolveMaxRetries(intPointer(99)))
	require.Equal(t, 0, ResolveMaxRetries(intPointer(-1)))
}

func intPointer(value int) *int {
	return &value
}
