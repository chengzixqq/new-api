package logger

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestRunLogRotationReleasesClaimAfterOpenFailure(t *testing.T) {
	originalLogDir := *common.LogDir
	t.Cleanup(func() {
		*common.LogDir = originalLogDir
		setupLogWorking.Store(false)
	})

	*common.LogDir = filepath.Join(t.TempDir(), "missing", "directory")
	setupLogWorking.Store(true)

	runLogRotation()

	assert.False(t, setupLogWorking.Load())
}
