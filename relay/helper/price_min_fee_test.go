package helper

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinFeeToQuota(t *testing.T) {
	// 期望值锚定 QuotaPerUnit=500000，若该常量变动需重算（避免浮点 vs decimal 边界不等）。
	require.Equal(t, float64(500000), common.QuotaPerUnit)

	quota, clamp := minFeeToQuota(0.05, 1.0)
	assert.Equal(t, 25000, quota)
	assert.Nil(t, clamp)

	quota, clamp = minFeeToQuota(0.05, 0.8)
	assert.Equal(t, 20000, quota)
	assert.Nil(t, clamp)

	quota, clamp = minFeeToQuota(0.0000001, 1)
	assert.Equal(t, 1, quota, "positive minimum fees must continue rounding upward")
	assert.Nil(t, clamp)

	for _, input := range []struct {
		fee   float64
		ratio float64
	}{{fee: 0, ratio: 1}, {fee: -1, ratio: 1}, {fee: 1, ratio: 0}, {fee: 1, ratio: -1}} {
		quota, clamp = minFeeToQuota(input.fee, input.ratio)
		assert.Zero(t, quota)
		assert.Nil(t, clamp)
	}
}

func TestMinFeeToQuotaSaturatesWithoutWrappingNegative(t *testing.T) {
	quota, clamp := minFeeToQuota(math.MaxFloat64, math.MaxFloat64)
	require.NotNil(t, clamp)
	assert.Equal(t, common.MaxQuota, quota)
	assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	assert.Equal(t, "QuotaFromDecimal", clamp.Op)

	quota, clamp = minFeeToQuota(math.NaN(), 1)
	require.NotNil(t, clamp)
	assert.Zero(t, quota)
	assert.Equal(t, common.QuotaClampNaN, clamp.Kind)
}

func TestComputeMinQuotaPrefersGroupForcedOverModel(t *testing.T) {
	groupFee := 0.10
	override := &types.ModelGroupPricing{MinFee: &groupFee}
	// 分组强制 $0.10 不乘普通分组倍率；没有个人价格时保持 50000。
	quota, clamp := computeMinQuota(override, "any-model-without-model-min-fee", 0.5, 1)
	assert.Equal(t, 50000, quota)
	assert.Nil(t, clamp)

	quota, clamp = computeMinQuota(override, "any-model-without-model-min-fee", 0.5, 0.8)
	assert.Equal(t, 40000, quota)
	assert.Nil(t, clamp)
}

func TestModelPriceHelperRecordsMinimumFeeClampOnRelayInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedRatios := ratio_setting.ModelRatio2JSONString()
	savedMinFees := ratio_setting.ModelMinFee2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateModelMinFeeByJSONString(savedMinFees))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"minimum-fee-overflow-model":1}`))
	minFees, err := common.Marshal(map[string]float64{"minimum-fee-overflow-model": math.MaxFloat64})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelMinFeeByJSONString(string(minFees)))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("group", "default")
	info := &relaycommon.RelayInfo{
		UserId:          7,
		OriginModelName: "minimum-fee-overflow-model",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := ModelPriceHelper(c, info, 1000, &types.TokenCountMeta{})

	require.NoError(t, err)
	assert.Equal(t, common.MaxQuota, priceData.MinQuota)
	require.NotNil(t, info.QuotaClamp)
	assert.Equal(t, common.QuotaClampOverflow, info.QuotaClamp.Kind)
	assert.Equal(t, "QuotaFromDecimal", info.QuotaClamp.Op)
}
