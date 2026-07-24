package model_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesEmptyOutputGuardEnabledPrecedence(t *testing.T) {
	original := globalSettings.ResponsesEmptyOutputGuardEnabled
	t.Cleanup(func() { globalSettings.ResponsesEmptyOutputGuardEnabled = original })

	globalSettings.ResponsesEmptyOutputGuardEnabled = false
	require.False(t, ResponsesEmptyOutputGuardEnabled(nil))
	require.True(t, ResponsesEmptyOutputGuardEnabled(boolPointer(true)))
	require.False(t, ResponsesEmptyOutputGuardEnabled(boolPointer(false)))

	globalSettings.ResponsesEmptyOutputGuardEnabled = true
	require.True(t, ResponsesEmptyOutputGuardEnabled(nil))
	require.False(t, ResponsesEmptyOutputGuardEnabled(boolPointer(false)))
}

func boolPointer(value bool) *bool {
	return &value
}
