package helper

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{
		BillingRatios: map[string]float64{"n": 3},
	})
	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
}

func TestModelPriceHelperGroupPerRequestOverridesRatioModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	helperSeedGroupPricingModel(t,
		"grp-mode-model",
		map[string]float64{"grp-mode-model": 2}, // model ratio (per-token)
		`{"c":{"billing_mode":"per-request","model_price":0.03}}`,
		[]string{"c"},
	)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "c")

	info := &relaycommon.RelayInfo{
		OriginModelName: "grp-mode-model",
		UserGroup:       "c",
		UsingGroup:      "c",
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.True(t, priceData.UsePrice, "group pinned per-request -> UsePrice forced true")
	require.Equal(t, 0.03, priceData.ModelPrice, "group model_price applied")
}

func TestModelPriceHelperGroupTieredExprUsesGroupExpression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	helperSeedGroupPricingModel(t,
		"grp-expr-model",
		map[string]float64{"grp-expr-model": 2},
		`{"c":{"billing_mode":"tiered_expr","billing_expr":"tier(\"base\", p * 7)"}}`,
		[]string{"c"},
	)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Body = nil
	req.ContentLength = 0
	ctx.Request = req
	ctx.Set("group", "c")

	info := &relaycommon.RelayInfo{
		OriginModelName: "grp-expr-model",
		UserGroup:       "c",
		UsingGroup:      "c",
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{},
			Body:    []byte(`{}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, `tier("base", p * 7)`, info.TieredBillingSnapshot.ExprString)
	require.Equal(t, int(7000.0/1_000_000*common.QuotaPerUnit), priceData.QuotaToPreConsume)
}

func helperSeedGroupPricingModel(t *testing.T, modelName string, ratios map[string]float64, groupPricingJSON string, groups []string) {
	t.Helper()

	oldDB := model.DB
	oldUsingSQLite := common.UsingSQLite
	oldRedis := common.RedisEnabled
	common.UsingSQLite = true
	common.RedisEnabled = false

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Model{}, &model.Vendor{}, &model.Channel{}, &model.Ability{}))
	model.InvalidatePricingCache()

	require.NoError(t, db.Create(&model.Model{
		ModelName: modelName, Status: 1, SyncOfficial: 1, GroupPricing: groupPricingJSON,
	}).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 1, Type: constant.ChannelTypeOpenAI, Key: "k", Status: 1, Name: "ch",
	}).Error)
	for _, g := range groups {
		require.NoError(t, db.Create(&model.Ability{
			Group: g, Model: modelName, ChannelId: 1, Enabled: true,
		}).Error)
	}

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(mustJSON(t, ratios)))
	model.GetPricing()

	t.Cleanup(func() {
		model.InvalidatePricingCache()
		model.DB = oldDB
		common.UsingSQLite = oldUsingSQLite
		common.RedisEnabled = oldRedis
		_ = ratio_setting.UpdateModelRatioByJSONString("{}")
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestModelPriceHelperPerCallGroupPerRequestForcesPerCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	helperSeedGroupPricingModel(t,
		"task-grp-model",
		map[string]float64{"task-grp-model": 2},
		`{"c":{"billing_mode":"per-request","model_price":0.05}}`,
		[]string{"c"},
	)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/mj/submit", nil)
	ctx.Set("group", "c")

	info := &relaycommon.RelayInfo{
		OriginModelName: "task-grp-model",
		UserGroup:       "c",
		UsingGroup:      "c",
	}

	priceData, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)
	require.True(t, priceData.UsePrice, "group per-request forces per-call billing on task surface")
	require.Equal(t, 0.05, priceData.ModelPrice)
	require.Equal(t, int(0.05*common.QuotaPerUnit), priceData.Quota)
}

func TestModelPriceHelperTieredPreConsumeMaxTokensFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-fallback-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-fallback-model":"tier(\"base\", p * 3 + c * 15)"}`,
		"group_ratio_setting.group_ratio": `{"default":1,"free":0}`,
	}))

	const promptTokens = 1000
	cases := []struct {
		name      string
		group     string
		maxTokens int
		expected  int
	}{
		{
			name:      "non-free group falls back to 8192 completion tokens",
			group:     "default",
			maxTokens: 0,
			expected:  62940,
		},
		{
			name:      "explicit max_tokens is used verbatim",
			group:     "default",
			maxTokens: 100,
			expected:  2250,
		},
		{
			name:      "free group stays zero without fallback",
			group:     "free",
			maxTokens: 0,
			expected:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req
			ctx.Set("group", tc.group)

			info := &relaycommon.RelayInfo{
				OriginModelName: "tiered-fallback-model",
				UserGroup:       tc.group,
				UsingGroup:      tc.group,
				RequestHeaders:  map[string]string{"Content-Type": "application/json"},
				BillingRequestInput: &billingexpr.RequestInput{
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    []byte(`{}`),
				},
			}

			priceData, err := ModelPriceHelper(ctx, info, promptTokens, &types.TokenCountMeta{MaxTokens: tc.maxTokens})
			require.NoError(t, err)
			require.Equal(t, tc.expected, priceData.QuotaToPreConsume)
		})
	}
}

func TestModelPriceHelperTieredRejectsPreConsumeOverflow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-overflow-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-overflow-model":"tier(\"overflow\", p * 1000000000000000)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-overflow-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{}`),
		},
	}

	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})

	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaRound", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func TestModelPriceHelperRequestBillingRatiosOnlyApplyToFixedPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	savedModelRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedModelRatios))
	})

	modelPrices, err := common.Marshal(map[string]float64{
		"fixed-image-price":      0.04,
		"fractional-image-price": 0.0000012,
		"overflow-image-price":   float64(common.MaxQuota) / common.QuotaPerUnit / 2,
	})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(modelPrices)))
	modelRatios, err := common.Marshal(map[string]float64{"ratio-image-price": 15})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(modelRatios)))

	tests := []struct {
		name           string
		model          string
		wantQuota      int
		wantUsePrice   bool
		wantImageCount bool
	}{
		{
			name:           "fixed price applies image count",
			model:          "fixed-image-price",
			wantQuota:      180000,
			wantUsePrice:   true,
			wantImageCount: true,
		},
		{
			name:         "ratio price ignores request billing ratios",
			model:        "ratio-image-price",
			wantQuota:    15000,
			wantUsePrice: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("group", "default")
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				UserGroup:       "default",
				UsingGroup:      "default",
			}
			meta := &types.TokenCountMeta{
				ImagePriceRatio: 3,
				BillingRatios:   map[string]float64{"n": 3},
			}

			priceData, err := ModelPriceHelper(ctx, info, 1000, meta)
			require.NoError(t, err)
			require.Equal(t, tt.wantQuota, priceData.QuotaToPreConsume)
			require.Equal(t, tt.wantUsePrice, priceData.UsePrice)
			require.Equal(t, tt.wantImageCount, priceData.HasOtherRatio("n"))
			require.Equal(t, priceData.OtherRatios(), info.PriceData.OtherRatios())
		})
	}

	newInfo := func(model string) (*gin.Context, *relaycommon.RelayInfo) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		return ctx, &relaycommon.RelayInfo{
			OriginModelName: model,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
	}
	meta := &types.TokenCountMeta{BillingRatios: map[string]float64{"n": 3}}

	ctx, info := newInfo("fractional-image-price")
	priceData, err := ModelPriceHelper(ctx, info, 0, meta)
	require.NoError(t, err)
	require.Equal(t, 1, priceData.QuotaToPreConsume)

	ctx, info = newInfo("overflow-image-price")
	_, err = ModelPriceHelper(ctx, info, 0, meta)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaFromFloat", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	require.Nil(t, info.Billing)
}

func TestGroupPriceOverridesRejectPreConsumeOverflow(t *testing.T) {
	huge := math.MaxFloat64
	tests := []struct {
		name      string
		priceData types.PriceData
		apply     func(*types.PriceData) error
	}{
		{
			name: "per-token override",
			priceData: types.PriceData{
				GroupPriceOverride: &types.ModelGroupPricing{PromptPrice: &huge},
			},
			apply: func(data *types.PriceData) error {
				return applyTokenPriceOverrides(data, 1000, 0)
			},
		},
		{
			name: "per-request override",
			priceData: types.PriceData{
				UsePrice:           true,
				GroupPriceOverride: &types.ModelGroupPricing{ModelPrice: &huge},
			},
			apply: applyPerCallPriceOverrides,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.apply(&tt.priceData)
			var clamp *common.QuotaClamp
			require.ErrorAs(t, err, &clamp)
			require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
			require.Zero(t, tt.priceData.QuotaToPreConsume)
		})
	}
}

func TestApplyTokenPriceOverridesPreConsumesPromptAndCompletionSeparately(t *testing.T) {
	promptPrice := 1.0
	completionPrice := 10.0
	tests := []struct {
		name     string
		override types.ModelGroupPricing
		want     int
	}{
		{
			name: "absolute prompt and completion prices",
			override: types.ModelGroupPricing{
				PromptPrice:     &promptPrice,
				CompletionPrice: &completionPrice,
			},
			want: 10500,
		},
		{
			name: "completion-only override keeps ratio prompt fallback",
			override: types.ModelGroupPricing{
				CompletionPrice: &completionPrice,
			},
			want: 12000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priceData := types.PriceData{
				ModelRatio:         1,
				CompletionRatio:    5,
				GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 2},
				GroupPriceOverride: &tt.override,
			}

			require.NoError(t, applyTokenPriceOverrides(&priceData, 1000, 2000))
			require.Equal(t, tt.want, priceData.QuotaToPreConsume)
		})
	}
}

func TestApplyTokenPriceOverridesIncludesMinimumFeeInPreConsume(t *testing.T) {
	zeroPrice := 0.0
	priceData := types.PriceData{
		MinQuota:           1234,
		GroupPriceOverride: &types.ModelGroupPricing{PromptPrice: &zeroPrice},
		GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
	}

	require.NoError(t, applyTokenPriceOverrides(&priceData, 1, 0))
	require.Equal(t, 1234, priceData.QuotaToPreConsume)
}

func TestHandleGroupRatioUserOverrideWinsMembershipGroupRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		UserGroup:       "vip",
		UsingGroup:      "edit_this",
		OriginModelName: "group-ratio-user-override-test",
		UserGroupRatioOverrides: map[string]float64{
			"edit_this": 0.75,
		},
	}

	ratioInfo := HandleGroupRatio(ctx, info)

	require.True(t, ratioInfo.HasSpecialRatio)
	require.Equal(t, 0.9, ratioInfo.GroupSpecialRatio)
	require.True(t, ratioInfo.HasUserGroupRatioOverride)
	require.Equal(t, 0.75, ratioInfo.UserGroupRatioOverride)
	require.Equal(t, 0.75, ratioInfo.GroupRatio)
	require.Equal(t, types.GroupRatioSourceUserOverride, ratioInfo.GroupRatioSource)
}

func TestHandleGroupRatioUsesFinalAutoGroupForUserOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("auto_group", "discount")
	info := &relaycommon.RelayInfo{
		UsingGroup:      "auto",
		OriginModelName: "group-ratio-auto-user-override-test",
		UserGroupRatioOverrides: map[string]float64{
			"discount": 0.6,
		},
	}

	ratioInfo := HandleGroupRatio(ctx, info)

	require.Equal(t, "discount", info.UsingGroup)
	require.Equal(t, 0.6, ratioInfo.GroupRatio)
	require.Equal(t, types.GroupRatioSourceUserOverride, ratioInfo.GroupRatioSource)
}

func TestHandleGroupRatioUserOverrideWinsModelGroupRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	helperSeedGroupPricingModel(t,
		"group-ratio-model-override-test",
		map[string]float64{"group-ratio-model-override-test": 2},
		`{"c":0.5}`,
		[]string{"c"},
	)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		UsingGroup:      "c",
		OriginModelName: "group-ratio-model-override-test",
		UserGroupRatioOverrides: map[string]float64{
			"c": 0.75,
		},
	}

	ratioInfo := HandleGroupRatio(ctx, info)

	require.True(t, ratioInfo.HasUserGroupRatioOverride)
	require.True(t, ratioInfo.HasModelGroupRatio)
	require.Equal(t, 0.5, ratioInfo.ModelGroupRatio)
	require.Equal(t, 0.75, ratioInfo.UserGroupRatioOverride)
	require.Equal(t, 0.75, ratioInfo.GroupRatio)
	require.Equal(t, types.GroupRatioSourceUserOverride, ratioInfo.GroupRatioSource)
}

func TestModelPriceHelperChargesWithUserOverrideAboveModelGroupRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	helperSeedGroupPricingModel(t,
		"group-ratio-user-priority-billing-test",
		map[string]float64{"group-ratio-user-priority-billing-test": 2},
		`{"c":0.5}`,
		[]string{"c"},
	)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		UsingGroup:      "c",
		OriginModelName: "group-ratio-user-priority-billing-test",
		UserGroupRatioOverrides: map[string]float64{
			"c": 0.75,
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})

	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.Equal(t, 0.75, priceData.GroupRatioInfo.GroupRatio)
	require.Equal(t, types.GroupRatioSourceUserOverride, priceData.GroupRatioInfo.GroupRatioSource)
}

func TestHandleGroupRatioIgnoresInvalidCachedUserOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		UsingGroup:      "default",
		OriginModelName: "invalid-user-group-ratio-override-test",
		UserGroupRatioOverrides: map[string]float64{
			"default": 1000.01,
		},
	}

	ratioInfo := HandleGroupRatio(ctx, info)

	require.False(t, ratioInfo.HasUserGroupRatioOverride)
	require.Equal(t, ratio_setting.GetGroupRatio("default"), ratioInfo.GroupRatio)
	require.Equal(t, types.GroupRatioSourceGroup, ratioInfo.GroupRatioSource)
}
