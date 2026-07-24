package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestGenerateTextOtherInfoIncludesUserGroupRatioOverrideAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	now := time.Now()
	relayInfo := &relaycommon.RelayInfo{
		StartTime:         now,
		FirstResponseTime: now,
		ChannelMeta:       &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio:                0.75,
				GroupRatioSource:          types.GroupRatioSourceUserOverride,
				UserGroupRatioOverride:    0.75,
				HasUserGroupRatioOverride: true,
			},
		},
	}

	other := GenerateTextOtherInfo(ctx, relayInfo, 1, 0.75, 1, 0, 1, 0, -1)

	assert.Equal(t, 0.75, other["user_group_ratio_override"])
	assert.Equal(t, types.GroupRatioSourceUserOverride, other["group_ratio_source"])
}
