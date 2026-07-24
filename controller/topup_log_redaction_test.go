package controller

import (
	"bytes"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyCreemSignatureDoesNotLogSignatureOrPayload(t *testing.T) {
	const (
		payload   = `{"request_id":"order-secret","customer":{"email":"private@example.com"}}`
		signature = "webhook-signature-secret"
	)

	previousTestMode := setting.CreemTestMode
	setting.CreemTestMode = false
	t.Cleanup(func() {
		setting.CreemTestMode = previousTestMode
	})

	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	require.False(t, verifyCreemSignature(payload, signature, ""))
	logOutput := output.String()
	assert.Contains(t, logOutput, "Creem webhook secret")
	assert.NotContains(t, logOutput, signature)
	assert.NotContains(t, logOutput, "order-secret")
	assert.NotContains(t, logOutput, "private@example.com")
}
