package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentLogQueryOptionsHonorsDisabledByDefaultAndRefreshWindow(t *testing.T) {
	oldLogType := common.LogDatabaseType()
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() { common.SetLogDatabaseType(oldLogType) })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/log/", nil)

	t.Setenv("LOG_ROLLUP_ENABLED", "false")
	t.Setenv("LOG_ROLLUP_READ_ENABLED", "false")
	options := currentLogQueryOptions(ctx)
	assert.False(t, options.UseRollup)
	require.Equal(t, ctx.Request.Context(), options.Context)

	t.Setenv("LOG_ROLLUP_ENABLED", "true")
	t.Setenv("LOG_ROLLUP_READ_ENABLED", "true")
	t.Setenv("LOG_ROLLUP_REFRESH_SECONDS", "2")
	options = currentLogQueryOptions(ctx)
	assert.True(t, options.UseRollup)
	assert.Equal(t, 15*time.Second, options.StaleAfter)
}

func TestLogStatResponseDataKeepsLegacyFieldsAndAddsRollupMetadata(t *testing.T) {
	data := logStatResponseData(model.Stat{
		Quota:     123,
		Rpm:       4,
		Tpm:       567,
		UpdatedAt: 1_700_000_000,
		Stale:     true,
		Source:    "minute_rollup",
	})

	assert.Equal(t, int64(123), data["quota"])
	assert.Equal(t, int64(4), data["rpm"])
	assert.Equal(t, int64(567), data["tpm"])
	assert.Equal(t, int64(1_700_000_000), data["updated_at"])
	assert.Equal(t, true, data["stale"])
	assert.Equal(t, "minute_rollup", data["source"])
}
