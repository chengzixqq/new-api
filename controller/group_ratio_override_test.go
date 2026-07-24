package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	} `json:"data"`
}

func decodeUserListResponse(t *testing.T, recorder *httptest.ResponseRecorder) userListResponse {
	t.Helper()

	var payload userListResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload
}

func userListItemByUsername(t *testing.T, items []map[string]any, username string) map[string]any {
	t.Helper()

	for _, item := range items {
		if item["username"] == username {
			return item
		}
	}
	t.Fatalf("user %q not found in response", username)
	return nil
}

func TestGetUserReturnsPersonalGroupPricingWithoutRawStorageField(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	user := &model.User{
		Id:                     1100,
		Username:               "personal-pricing-detail-user",
		Password:               "password",
		Role:                   common.RoleCommonUser,
		Group:                  "default",
		Status:                 common.UserStatusEnabled,
		GroupRatioOverridesRaw: `{"vip":0.8}`,
	}
	require.NoError(t, db.Create(user).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/"+strconv.Itoa(user.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(user.Id)}}
	ctx.Set("role", common.RoleRootUser)

	GetUser(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			GroupRatioOverrides map[string]float64 `json:"group_ratio_overrides"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	assert.Equal(t, map[string]float64{"vip": 0.8}, payload.Data.GroupRatioOverrides)
	assert.NotContains(t, recorder.Body.String(), "group_ratio_overrides_raw")
}

func TestGetAllUsersConditionallyReturnsPersonalGroupPricing(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	users := []model.User{
		{
			Id:                     1102,
			Username:               "list-personal-pricing-user",
			Password:               "password",
			Group:                  "default",
			Status:                 common.UserStatusEnabled,
			AffCode:                "list-personal-pricing-aff",
			GroupRatioOverridesRaw: `{"CC-MAX":0.6,"A":0.1}`,
		},
		{
			Id:                     1103,
			Username:               "list-invalid-personal-pricing-user",
			Password:               "password",
			Group:                  "default",
			Status:                 common.UserStatusEnabled,
			AffCode:                "list-invalid-personal-pricing-aff",
			GroupRatioOverridesRaw: `{invalid`,
		},
		{
			Id:       1104,
			Username: "list-empty-personal-pricing-user",
			Password: "password",
			Group:    "default",
			Status:   common.UserStatusEnabled,
			AffCode:  "list-empty-personal-pricing-aff",
		},
	}
	require.NoError(t, db.Create(&users).Error)

	withoutOverridesRecorder := httptest.NewRecorder()
	withoutOverridesContext, _ := gin.CreateTestContext(withoutOverridesRecorder)
	withoutOverridesContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/?p=1&page_size=20", nil)

	GetAllUsers(withoutOverridesContext)

	require.Equal(t, http.StatusOK, withoutOverridesRecorder.Code)
	withoutOverrides := decodeUserListResponse(t, withoutOverridesRecorder)
	require.Equal(t, 3, withoutOverrides.Data.Total)
	for _, item := range withoutOverrides.Data.Items {
		assert.NotContains(t, item, "group_ratio_overrides")
	}
	assert.NotContains(t, withoutOverridesRecorder.Body.String(), "group_ratio_overrides_raw")

	withOverridesRecorder := httptest.NewRecorder()
	withOverridesContext, _ := gin.CreateTestContext(withOverridesRecorder)
	withOverridesContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/?p=1&page_size=20&include_group_ratio_overrides=true", nil)

	GetAllUsers(withOverridesContext)

	require.Equal(t, http.StatusOK, withOverridesRecorder.Code)
	withOverrides := decodeUserListResponse(t, withOverridesRecorder)
	require.Equal(t, 3, withOverrides.Data.Total)
	pricedUser := userListItemByUsername(t, withOverrides.Data.Items, "list-personal-pricing-user")
	assert.Equal(t, map[string]any{"CC-MAX": 0.6, "A": 0.1}, pricedUser["group_ratio_overrides"])
	assert.NotContains(t, userListItemByUsername(t, withOverrides.Data.Items, "list-invalid-personal-pricing-user"), "group_ratio_overrides")
	assert.NotContains(t, userListItemByUsername(t, withOverrides.Data.Items, "list-empty-personal-pricing-user"), "group_ratio_overrides")
	assert.NotContains(t, withOverridesRecorder.Body.String(), "group_ratio_overrides_raw")
}

func TestSearchUsersConditionallyReturnsPersonalGroupPricing(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	user := model.User{
		Id:                     1105,
		Username:               "search-personal-pricing-user",
		Password:               "password",
		Group:                  "default",
		Status:                 common.UserStatusEnabled,
		AffCode:                "search-personal-pricing-aff",
		GroupRatioOverridesRaw: `{"vip":0.8}`,
	}
	require.NoError(t, db.Create(&user).Error)

	withoutOverridesRecorder := httptest.NewRecorder()
	withoutOverridesContext, _ := gin.CreateTestContext(withoutOverridesRecorder)
	withoutOverridesContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/search?keyword=search-personal-pricing-user&p=1&page_size=20", nil)

	SearchUsers(withoutOverridesContext)

	require.Equal(t, http.StatusOK, withoutOverridesRecorder.Code)
	withoutOverrides := decodeUserListResponse(t, withoutOverridesRecorder)
	require.Len(t, withoutOverrides.Data.Items, 1)
	assert.NotContains(t, withoutOverrides.Data.Items[0], "group_ratio_overrides")

	withOverridesRecorder := httptest.NewRecorder()
	withOverridesContext, _ := gin.CreateTestContext(withOverridesRecorder)
	withOverridesContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/search?keyword=search-personal-pricing-user&p=1&page_size=20&include_group_ratio_overrides=true", nil)

	SearchUsers(withOverridesContext)

	require.Equal(t, http.StatusOK, withOverridesRecorder.Code)
	withOverrides := decodeUserListResponse(t, withOverridesRecorder)
	require.Len(t, withOverrides.Data.Items, 1)
	assert.Equal(t, map[string]any{"vip": 0.8}, withOverrides.Data.Items[0]["group_ratio_overrides"])
	assert.NotContains(t, withOverridesRecorder.Body.String(), "group_ratio_overrides_raw")
}

func TestUserFacingPricingEndpointsLoadPersonalGroupRatioFromUserCache(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	user := &model.User{
		Id:                     1101,
		Username:               "personal-pricing-user",
		Password:               "password",
		Group:                  "default",
		Status:                 common.UserStatusEnabled,
		GroupRatioOverridesRaw: `{"default":0.7}`,
	}
	require.NoError(t, db.Create(user).Error)

	groupsRecorder := httptest.NewRecorder()
	groupsContext, _ := gin.CreateTestContext(groupsRecorder)
	groupsContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/groups", nil)
	groupsContext.Set("id", user.Id)

	GetUserGroups(groupsContext)

	require.Equal(t, http.StatusOK, groupsRecorder.Code)
	var groupsPayload struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Ratio float64 `json:"ratio"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(groupsRecorder.Body.Bytes(), &groupsPayload))
	require.True(t, groupsPayload.Success)
	assert.Equal(t, 0.7, groupsPayload.Data["default"].Ratio)

	pricingRecorder := httptest.NewRecorder()
	pricingContext, _ := gin.CreateTestContext(pricingRecorder)
	pricingContext.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	pricingContext.Set("id", user.Id)

	GetPricing(pricingContext)

	require.Equal(t, http.StatusOK, pricingRecorder.Code)
	var pricingPayload struct {
		Success    bool               `json:"success"`
		GroupRatio map[string]float64 `json:"group_ratio"`
	}
	require.NoError(t, common.Unmarshal(pricingRecorder.Body.Bytes(), &pricingPayload))
	require.True(t, pricingPayload.Success)
	assert.Equal(t, 0.7, pricingPayload.GroupRatio["default"])
}

func TestApplyPersonalGroupPricingRatiosOverridesModelGroupRatioWithoutMutatingCache(t *testing.T) {
	modelGroupRatio := 0.5
	modelPrice := 0.03
	promptPrice := 1.25
	minFee := 0.01
	pricing := []model.Pricing{{
		ModelName: "personal-pricing-model",
		GroupPricing: map[string]types.ModelGroupPricing{
			"vip": {
				Ratio:       &modelGroupRatio,
				ModelPrice:  &modelPrice,
				PromptPrice: &promptPrice,
				MinFee:      &minFee,
			},
		},
	}}

	adjusted := applyPersonalGroupPricingRatios(pricing, map[string]float64{"vip": 0.8})

	require.Len(t, adjusted, 1)
	require.NotNil(t, adjusted[0].GroupPricing["vip"].Ratio)
	assert.Equal(t, 0.8, *adjusted[0].GroupPricing["vip"].Ratio)
	assert.InDelta(t, 0.024, *adjusted[0].GroupPricing["vip"].ModelPrice, 1e-12)
	assert.InDelta(t, 1, *adjusted[0].GroupPricing["vip"].PromptPrice, 1e-12)
	assert.InDelta(t, 0.008, *adjusted[0].GroupPricing["vip"].MinFee, 1e-12)
	require.NotNil(t, pricing[0].GroupPricing["vip"].Ratio)
	assert.Equal(t, 0.5, *pricing[0].GroupPricing["vip"].Ratio)
	assert.Equal(t, modelPrice, *pricing[0].GroupPricing["vip"].ModelPrice)
	assert.Equal(t, promptPrice, *pricing[0].GroupPricing["vip"].PromptPrice)
	assert.Equal(t, minFee, *pricing[0].GroupPricing["vip"].MinFee)
}
