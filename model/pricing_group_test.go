package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPricingGroupTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	oldLogDB := LOG_DB
	oldUsingSQLite := common.UsingSQLite
	oldUsingMySQL := common.UsingMySQL
	oldUsingPostgreSQL := common.UsingPostgreSQL
	oldRedisEnabled := common.RedisEnabled

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	LOG_DB = db
	require.NoError(t, db.AutoMigrate(&Model{}, &Vendor{}, &Channel{}, &Ability{}))
	InvalidatePricingCache()

	t.Cleanup(func() {
		InvalidatePricingCache()
		DB = oldDB
		LOG_DB = oldLogDB
		common.UsingSQLite = oldUsingSQLite
		common.UsingMySQL = oldUsingMySQL
		common.UsingPostgreSQL = oldUsingPostgreSQL
		common.RedisEnabled = oldRedisEnabled
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestPricingIncludesModelGroupPricing(t *testing.T) {
	setupPricingGroupTestDB(t)

	require.NoError(t, DB.Create(&Model{
		ModelName:    "priced-group-model",
		Status:       1,
		SyncOfficial: 1,
		GroupPricing: `{"vip":1.25,"svip":{"ratio":1.5,"prompt_price":0.1,"cache_price":0.05}}`,
	}).Error)
	require.NoError(t, DB.Create(&Channel{
		Id:     1,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "test-key",
		Status: 1,
		Name:   "test-channel",
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "vip",
		Model:     "priced-group-model",
		ChannelId: 1,
		Enabled:   true,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "svip",
		Model:     "priced-group-model",
		ChannelId: 1,
		Enabled:   true,
	}).Error)

	pricings := GetPricing()
	require.Len(t, pricings, 1)
	require.Equal(t, types.ModelGroupPricing{Ratio: float64Ptr(1.25)}, pricings[0].GroupPricing["vip"])
	require.Equal(t, types.ModelGroupPricing{
		Ratio:       float64Ptr(1.5),
		PromptPrice: float64Ptr(0.1),
		CachePrice:  float64Ptr(0.05),
	}, pricings[0].GroupPricing["svip"])

	ratio, ok := GetModelGroupRatio("priced-group-model", "svip")
	require.True(t, ok)
	require.Equal(t, 1.5, ratio)

	override, ok := GetModelGroupPriceOverrides("priced-group-model", "svip")
	require.True(t, ok)
	require.NotNil(t, override.PromptPrice)
	require.Equal(t, 0.1, *override.PromptPrice)
}

func TestNormalizeModelGroupPricingDropsInvalidAndEmptyValues(t *testing.T) {
	cleaned := NormalizeModelGroupPricing(map[string]types.ModelGroupPricing{
		" vip ": {
			Ratio:       float64Ptr(1.2),
			PromptPrice: float64Ptr(0.1),
		},
		"empty": {},
		"bad": {
			Ratio: float64Ptr(-1),
		},
	})

	require.Len(t, cleaned, 1)
	require.Equal(t, 1.2, *cleaned["vip"].Ratio)
	require.Equal(t, 0.1, *cleaned["vip"].PromptPrice)
}

func float64Ptr(value float64) *float64 {
	return &value
}
