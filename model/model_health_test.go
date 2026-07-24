package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_health_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func validLocalModelHealthTarget(t *testing.T) *ModelHealthTarget {
	t.Helper()
	tokenID := 1
	target := &ModelHealthTarget{
		Name:            "Local probe",
		Mode:            ModelHealthModeLocal,
		TokenID:         &tokenID,
		Protocol:        ModelHealthProtocolOpenAIChat,
		IntervalSeconds: 300,
		TimeoutSeconds:  45,
	}
	require.NoError(t, target.SetModels([]ModelHealthTargetModel{{
		Name:        "gpt-test",
		PublicAlias: "GPT Test",
		Required:    true,
	}}))
	return target
}

func TestModelHealthTargetNormalizePreservesExplicitRequiredModels(t *testing.T) {
	target := validLocalModelHealthTarget(t)

	require.NoError(t, target.Normalize())
	models, err := target.Models()
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "gpt-test", models[0].Name)
	assert.Equal(t, "GPT Test", models[0].PublicAlias)
	assert.True(t, models[0].Required)
}

func TestModelHealthTargetNormalizeSamplingCompatibilityAndBudget(t *testing.T) {
	target := validLocalModelHealthTarget(t)
	require.NoError(t, target.Normalize())
	assert.Equal(t, ModelHealthSamplingFixed, target.SamplingMode)
	assert.Equal(t, 1, target.SamplesPerRun)
	assert.Equal(t, 1, target.MinimumSuccesses)
	assert.Equal(t, model_health_setting.DefaultSampleSpacingSeconds, target.SampleSpacingSeconds)

	target.SamplingMode = ModelHealthSamplingConfirmOnFailure
	target.SamplesPerRun = 1
	target.MinimumSuccesses = 5
	require.NoError(t, target.Normalize())
	assert.Equal(t, 1, target.MinimumSuccesses)

	target.SamplesPerRun = 3
	target.MinimumSuccesses = 2
	target.SampleSpacingSeconds = 3
	require.NoError(t, target.Normalize())

	target.IntervalSeconds = 60
	assert.ErrorContains(t, target.Normalize(), "time budget")

	explicitInvalid := validLocalModelHealthTarget(t)
	explicitInvalid.SamplesPerRun = 3
	explicitInvalid.MinimumSuccesses = 2
	explicitInvalid.SampleSpacingSeconds = 3
	assert.ErrorContains(t, explicitInvalid.Normalize(), "sampling_mode")
}

func TestModelHealthTargetNormalizeRejectsDuplicateModels(t *testing.T) {
	target := validLocalModelHealthTarget(t)
	require.NoError(t, target.SetModels([]ModelHealthTargetModel{
		{Name: "same", Required: true},
		{Name: " same ", Required: false},
	}))

	err := target.Normalize()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate model name")
}

func TestModelHealthTargetNormalizeSeparatesLocalAndUpstreamCredentials(t *testing.T) {
	target := validLocalModelHealthTarget(t)
	target.EndpointEncrypted = "ciphertext"
	target.APIKeyEncrypted = "ciphertext"

	require.NoError(t, target.Normalize())
	assert.Empty(t, target.EndpointEncrypted)
	assert.Empty(t, target.APIKeyEncrypted)

	target.Mode = ModelHealthModeUpstream
	target.TokenID = nil
	err := target.Normalize()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypted endpoint")
}

func TestValidModelHealthProtocol(t *testing.T) {
	for _, protocol := range []string{
		ModelHealthProtocolOpenAIChat,
		ModelHealthProtocolOpenAIResponses,
		ModelHealthProtocolAnthropicMessages,
		ModelHealthProtocolGeminiContent,
	} {
		assert.True(t, ValidModelHealthProtocol(protocol), protocol)
	}
	assert.False(t, ValidModelHealthProtocol("image"))
}

func TestModelHealthTargetObservedGroupsNormalizeAndLegacyFallback(t *testing.T) {
	target := validLocalModelHealthTarget(t)
	target.ObservedGroup = " legacy "

	groups, err := target.ObservedGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"legacy"}, groups)

	require.NoError(t, target.SetObservedGroups([]string{" group-b ", "group-a", "group-b", ""}))
	require.NoError(t, target.Normalize())
	groups, err = target.ObservedGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"group-b", "group-a"}, groups)
	assert.Equal(t, "group-b", target.ObservedGroup)

	tooMany := make([]string, MaxObservedGroupsPerTarget+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("group-%d", index)
	}
	require.NoError(t, target.SetObservedGroups(tooMany))
	assert.ErrorContains(t, target.Normalize(), "at most 20")
}

func TestModelHealthTargetPoolBindingsNormalizeAndRequirePublicIdentity(t *testing.T) {
	target := validLocalModelHealthTarget(t)
	require.NoError(t, target.SetObservedChannelIDs([]int{84, 58, 84}))
	target.PublishPoolDetail = true
	target.PublicGroupAlias = " CC-MAX "
	target.PublicPoolAlias = " CCMAX-A池 "

	require.NoError(t, target.Normalize())
	channelIDs, err := target.ObservedChannelIDs()
	require.NoError(t, err)
	assert.Equal(t, []int{84, 58}, channelIDs)
	assert.Equal(t, "CC-MAX", target.PublicGroupAlias)
	assert.Equal(t, "CCMAX-A池", target.PublicPoolAlias)

	target.PublicPoolAlias = ""
	assert.ErrorContains(t, target.Normalize(), "public_pool_alias")
	target.PublicPoolAlias = "pool"
	require.NoError(t, target.SetObservedChannelIDs([]int{}))
	assert.ErrorContains(t, target.Normalize(), "observed_channel_ids")
}

func TestModelHealthTargetRejectsDuplicatePoolAliasWithinPublicGroup(t *testing.T) {
	previousDB := DB
	db, err := gorm.Open(sqlite.Open("file:model-health-pool-alias?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&ModelHealthTarget{}))

	first := validLocalModelHealthTarget(t)
	first.Name = "pool-a"
	first.PublicGroupAlias = "CC-MAX"
	first.PublicPoolAlias = "CCMAX-A池"
	first.PublishPoolDetail = true
	require.NoError(t, first.SetObservedChannelIDs([]int{84}))
	require.NoError(t, CreateModelHealthTarget(first))

	duplicate := validLocalModelHealthTarget(t)
	duplicate.Name = "pool-b"
	duplicate.PublicGroupAlias = first.PublicGroupAlias
	duplicate.PublicPoolAlias = first.PublicPoolAlias
	duplicate.PublishPoolDetail = true
	require.NoError(t, duplicate.SetObservedChannelIDs([]int{58}))
	err = CreateModelHealthTarget(duplicate)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrModelHealthPoolAliasConflict)

	duplicate.PublicPoolAlias = "CCMAX-B池"
	require.NoError(t, CreateModelHealthTarget(duplicate))
}

func TestModelHealthProbeTokenOwnershipAndReferenceProtection(t *testing.T) {
	previousDB := DB
	db, err := gorm.Open(sqlite.Open("file:model-health-probe-token?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&Token{}, &Option{}, &ModelHealthTarget{}, &ModelHealthHistory{}, &ModelHealthProbeToken{}))
	require.NoError(t, db.Create(&Token{
		Id: 10, UserId: 1, Key: "secret", Name: "health", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 100,
	}).Error)

	require.Error(t, SetModelHealthProbeTokenMarked(10, 2, true))
	require.NoError(t, SetModelHealthProbeTokenMarked(10, 1, true))
	marked, err := IsModelHealthProbeTokenMarkedForOwner(10, 1)
	require.NoError(t, err)
	assert.True(t, marked)

	tokenID := 10
	target := &ModelHealthTarget{
		Name: "local", Mode: ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: ModelHealthProtocolOpenAIChat, IntervalSeconds: 300, TimeoutSeconds: 45,
	}
	require.NoError(t, target.SetModels([]ModelHealthTargetModel{{Name: "gpt-test", Required: true}}))
	require.NoError(t, CreateModelHealthTarget(target))

	err = SetModelHealthProbeTokenMarked(10, 1, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrModelHealthProbeTokenInUse))
	err = DeleteTokenById(10, 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrModelHealthProbeTokenInUse))

	require.NoError(t, DeleteModelHealthTarget(target.ID))
	require.NoError(t, SetModelHealthProbeTokenMarked(10, 1, false))
	require.NoError(t, DeleteTokenById(10, 1))
}

func TestMigrateLegacyModelHealthConfigurationBackfillsAliasesAndProbeMark(t *testing.T) {
	previousDB := DB
	db, err := gorm.Open(sqlite.Open("file:model-health-legacy-migration?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&Token{}, &Option{}, &ModelHealthTarget{}, &ModelHealthHistory{}, &ModelHealthProbeToken{}))

	configValue, ok := config.GlobalConfig.Get("model_health_setting").(*model_health_setting.ModelHealthSetting)
	require.True(t, ok)
	previousSetting := *configValue
	configValue.PublicModels = []model_health_setting.PublicAlias{{Name: "internal-model", Alias: "Public Model"}}
	configValue.PublicGroups = []model_health_setting.PublicAlias{{Name: "internal-group", Alias: "Public Group"}}
	t.Cleanup(func() { *configValue = previousSetting })

	require.NoError(t, db.Create(&Token{
		Id: 20, UserId: 7, Key: "legacy-secret", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 100,
	}).Error)
	tokenID := 20
	target := &ModelHealthTarget{
		Name: "legacy", Mode: ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: ModelHealthProtocolOpenAIChat, ObservedGroup: "internal-group",
		IntervalSeconds: 300, TimeoutSeconds: 45,
	}
	require.NoError(t, target.SetModels([]ModelHealthTargetModel{{Name: "internal-model", Required: true}}))
	require.NoError(t, CreateModelHealthTarget(target))
	require.NoError(t, db.Model(&ModelHealthTarget{}).Where("id = ?", target.ID).Updates(map[string]any{
		"sampling_mode": "", "samples_per_run": 0, "minimum_successes": 0, "sample_spacing_seconds": 0,
	}).Error)

	require.NoError(t, MigrateLegacyModelHealthConfiguration())
	stored, err := GetModelHealthTarget(target.ID)
	require.NoError(t, err)
	assert.Equal(t, "Public Group", stored.PublicGroupAlias)
	assert.Equal(t, ModelHealthSamplingFixed, stored.SamplingMode)
	assert.Equal(t, 1, stored.SamplesPerRun)
	assert.Equal(t, 1, stored.MinimumSuccesses)
	assert.Equal(t, model_health_setting.DefaultSampleSpacingSeconds, stored.SampleSpacingSeconds)
	models, err := stored.Models()
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "Public Model", models[0].PublicAlias)
	mark, err := GetModelHealthProbeTokenMark(20)
	require.NoError(t, err)
	assert.Equal(t, 7, mark.MarkedBy)
	assert.Equal(t, "Public Model", configValue.PublicModels[0].Alias)

	models[0].PublicAlias = ""
	require.NoError(t, stored.SetModels(models))
	require.NoError(t, db.Model(&ModelHealthTarget{}).Where("id = ?", stored.ID).Updates(map[string]any{
		"models": stored.ModelsJSON, "public_group_alias": "",
	}).Error)
	require.NoError(t, MigrateLegacyModelHealthConfiguration())
	stored, err = GetModelHealthTarget(target.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.PublicGroupAlias)
	models, err = stored.Models()
	require.NoError(t, err)
	assert.Empty(t, models[0].PublicAlias)
}
