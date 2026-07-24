package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_health_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelHealthOverviewExposesOnlyPublicAliases(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Token{}, &model.ModelHealthTarget{}, &model.ModelHealthHistory{}, &model.PerfMetric{},
	))
	require.NoError(t, db.Create(&model.Token{Id: 91, UserId: 1, Key: "TOKEN_SECRET_VALUE", Status: common.TokenStatusEnabled}).Error)
	tokenID := 91
	target := &model.ModelHealthTarget{
		Name: "INTERNAL_TARGET_SECRET", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: model.ModelHealthProtocolOpenAIChat, ObservedGroup: "INTERNAL_GROUP_SECRET",
		PublicGroupAlias: "Public Group", Public: true, Enabled: true,
		IntervalSeconds: 300, TimeoutSeconds: 45,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{
		Name: "INTERNAL_MODEL_SECRET", PublicAlias: "Public Model", Required: true,
	}}))
	require.NoError(t, model.CreateModelHealthTarget(target))
	now := time.Now()
	histories := []model.ModelHealthHistory{
		{TargetID: target.ID, ModelName: "INTERNAL_MODEL_SECRET", ProbeRunID: "PRIVATE_RUN_ID_1", Status: model.ModelHealthProbeSuccess, LatencyMs: 100, CheckedAt: now.Add(-10 * time.Minute).Unix()},
		{TargetID: target.ID, ModelName: "INTERNAL_MODEL_SECRET", ProbeRunID: "PRIVATE_RUN_ID_2", Status: model.ModelHealthProbeSuccess, LatencyMs: 110, CheckedAt: now.Add(-5 * time.Minute).Unix()},
		{TargetID: target.ID, ModelName: "INTERNAL_MODEL_SECRET", ProbeRunID: "PRIVATE_RUN_ID_3", Status: model.ModelHealthProbeSuccess, LatencyMs: 120, CheckedAt: now.Add(-time.Minute).Unix()},
	}
	require.NoError(t, model.InsertModelHealthHistories(histories))
	bucketTs := now.Unix() - now.Unix()%300 - 300
	require.NoError(t, db.Create(&model.PerfMetric{
		ModelName: "INTERNAL_MODEL_SECRET", Group: "INTERNAL_GROUP_SECRET", BucketTs: bucketTs,
		RequestCount: 100, SuccessCount: 99, TotalLatencyMs: 10_000,
		TtftSumMs: 2_000, TtftCount: 100, OutputTokens: 1_000, GenerationMs: 10_000,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h", nil)
	GetModelHealthOverview(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, "Public Group")
	assert.Contains(t, body, "Public Model")
	assert.NotContains(t, body, "INTERNAL_TARGET_SECRET")
	assert.NotContains(t, body, "INTERNAL_GROUP_SECRET")
	assert.NotContains(t, body, "INTERNAL_MODEL_SECRET")
	assert.NotContains(t, body, "TOKEN_SECRET_VALUE")
	assert.NotContains(t, body, "PRIVATE_RUN_ID")
}

func TestModelHealthAdminTargetResponseContainsNoCredentialPlaintext(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "stable-controller-model-health-secret")
	target := &model.ModelHealthTarget{
		ID: 42, Name: "upstream", Mode: model.ModelHealthModeUpstream,
		Protocol: model.ModelHealthProtocolOpenAIChat, IntervalSeconds: 300, TimeoutSeconds: 45,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "model", PublicAlias: "Public", Required: true}}))
	require.NoError(t, service.UpdateModelHealthTargetCredentials(
		target, "https://api.example.com/private/path", "API_KEY_SECRET",
	))

	response, err := modelHealthTargetToResponse(target)
	require.NoError(t, err)
	encoded, err := common.Marshal(response)
	require.NoError(t, err)
	body := string(encoded)
	assert.NotContains(t, body, "API_KEY_SECRET")
	assert.NotContains(t, body, "https://api.example.com/private/path")
	assert.NotContains(t, body, target.APIKeyEncrypted)
	assert.NotContains(t, body, target.EndpointEncrypted)
	assert.True(t, response.HasKey)
	assert.True(t, strings.HasPrefix(response.EndpointMasked, "https://"))
}

func TestAdminValidateModelHealthTargetReturnsFailedBatchDetails(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.ModelHealthTarget{}, &model.ModelHealthProbeToken{}))
	require.NoError(t, db.Create(&model.Token{
		Id: 201, UserId: 1, Key: "VALIDATE_TOKEN_SECRET", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 100,
	}).Error)
	require.NoError(t, db.Create(&model.ModelHealthProbeToken{TokenID: 201, MarkedBy: 1}).Error)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	originalAddress := system_setting.ServerAddress
	system_setting.ServerAddress = server.URL
	t.Cleanup(func() { system_setting.ServerAddress = originalAddress })
	body := `{
		"name":"validation","mode":"local","token_id":201,"protocol":"openai_chat",
		"models":[{"name":"internal-model","public_alias":"Public Model","required":true}],
		"public_group_alias":"Public Group","public":true,"enabled":true,
		"interval_seconds":300,"timeout_seconds":10,"sampling_mode":"fixed",
		"samples_per_run":3,"minimum_successes":2,"sample_spacing_seconds":1
	}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/model-health/admin/targets/validate", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	AdminValidateModelHealthTarget(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	responseBody := recorder.Body.String()
	assert.Contains(t, responseBody, `"success":true`)
	assert.Contains(t, responseBody, `"batch_status":"unstable"`)
	assert.Contains(t, responseBody, `"planned_attempts":3`)
	assert.Contains(t, responseBody, `"actual_attempts":1`)
	assert.Contains(t, responseBody, `"attempt_index":1`)
	assert.NotContains(t, responseBody, "VALIDATE_TOKEN_SECRET")
	assert.NotContains(t, responseBody, "probe_run_id")
}

func TestModelHealthAdminTargetResponseDisablesBrokenCredentials(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "stable-controller-broken-health-secret")
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ModelHealthTarget{}, &model.ModelHealthHistory{}))
	endpoint, err := service.EncryptModelHealthSecret("https://api.example.com/v1")
	require.NoError(t, err)
	target := &model.ModelHealthTarget{
		Name: "broken-upstream", Mode: model.ModelHealthModeUpstream,
		Protocol: model.ModelHealthProtocolOpenAIChat, EndpointEncrypted: endpoint,
		APIKeyEncrypted: "v1:broken", PublicGroupAlias: "Public", Public: true,
		Enabled: true, IntervalSeconds: 300, TimeoutSeconds: 45,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "model", PublicAlias: "Model", Required: true}}))
	require.NoError(t, model.CreateModelHealthTarget(target))

	response, err := modelHealthTargetToResponse(target)
	require.NoError(t, err)
	assert.True(t, response.CredentialsNeedReplacement)
	assert.False(t, response.Enabled)
	stored, err := model.GetModelHealthTarget(target.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
}

func TestModelHealthOverviewFiltersModelGroupMatrixByPublicAlias(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.ModelHealthTarget{}, &model.ModelHealthHistory{}, &model.PerfMetric{}))
	require.NoError(t, db.Create(&model.Token{Id: 92, UserId: 1, Key: "matrix-token", Status: common.TokenStatusEnabled}).Error)
	tokenID := 92
	createTarget := func(name, group, modelName, alias string) {
		t.Helper()
		target := &model.ModelHealthTarget{
			Name: name, Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
			Protocol: model.ModelHealthProtocolOpenAIChat, PublicGroupAlias: group,
			Public: true, Enabled: true, IntervalSeconds: 300, TimeoutSeconds: 45,
		}
		require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: modelName, PublicAlias: alias, Required: true}}))
		require.NoError(t, model.CreateModelHealthTarget(target))
	}
	createTarget("target-a-shared", "Group A", "shared-internal-a", "Shared")
	createTarget("target-a-only", "Group A", "a-only-internal", "A only")
	createTarget("target-b-shared", "Group B", "shared-internal-b", "Shared")
	createTarget("target-b-only", "Group B", "b-only-internal", "B only")
	publicTargets, err := model.ListPublicModelHealthTargets()
	require.NoError(t, err)
	require.Len(t, publicTargets, 4)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h&group=Group+B&model=Shared", nil)
	publications, modelAliases, groupAliases, _, err := modelHealthPublications()
	require.NoError(t, err)
	require.Len(t, publications, 4)
	assert.Contains(t, modelAliases, "Shared")
	assert.Contains(t, groupAliases, "Group B")
	assert.Contains(t, queryAliasSet(ctx, "group"), "Group B")
	assert.Contains(t, queryAliasSet(ctx, "model"), "Shared")
	GetModelHealthOverview(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"group":"Group B"`)
	assert.Contains(t, body, `"model":"Shared"`)
	assert.NotContains(t, body, `"group":"Group A"`)
	assert.NotContains(t, body, `"model":"A only"`)
	assert.NotContains(t, body, `"model":"B only"`)
}

func TestObservedCellViewKeepsMetricDenominatorsSeparate(t *testing.T) {
	view := observedCellView(&perfmetrics.MatrixCell{
		RequestCount: 40, SuccessRate: 99, AvgLatencyMs: 125,
	})

	require.NotNil(t, view.SuccessRate)
	require.NotNil(t, view.AvgLatencyMs)
	assert.Nil(t, view.AvgTtftMs)
	assert.Nil(t, view.AvgTps)

	view = observedCellView(&perfmetrics.MatrixCell{
		RequestCount: 40, SuccessRate: 99, AvgLatencyMs: 125,
		TtftCount: 12, AvgTtftMs: 80, GenerationMs: 4_000, AvgTps: 25,
	})
	require.NotNil(t, view.AvgTtftMs)
	require.NotNil(t, view.AvgTps)
	assert.Equal(t, int64(80), *view.AvgTtftMs)
	assert.Equal(t, 25.0, *view.AvgTps)
}

func TestModelHealthAdminOptionsExposeOnlyMarkedUsableOwnedTokens(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Token{}, &model.Ability{}, &model.Channel{}, &model.ModelHealthTarget{}, &model.ModelHealthProbeToken{},
	))
	originalGroups, err := common.Marshal(setting.GetUserUsableGroupsCopy())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(string(originalGroups)))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(
		`{"group-a":"GROUP_A_DESCRIPTION","group-b":"GROUP_B_DESCRIPTION"}`,
	))
	tokens := []model.Token{
		{Id: 101, UserId: 1, Key: "OWNER_MARKED_SECRET", Name: "owner marked", Group: "group-a", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100},
		{Id: 102, UserId: 1, Key: "OWNER_UNMARKED_SECRET", Name: "owner unmarked", Group: "group-a", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100},
		{Id: 103, UserId: 1, Key: "OWNER_DISABLED_SECRET", Name: "owner disabled", Group: "group-a", Status: common.TokenStatusDisabled, ExpiredTime: -1, RemainQuota: 100},
		{Id: 104, UserId: 2, Key: "OTHER_OWNER_SECRET", Name: "other owner", Group: "group-b", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100},
	}
	require.NoError(t, db.Create(&tokens).Error)
	require.NoError(t, db.Create(&[]model.ModelHealthProbeToken{
		{TokenID: 101, MarkedBy: 1}, {TokenID: 103, MarkedBy: 1}, {TokenID: 104, MarkedBy: 2},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "group-a", Model: "model-a", ChannelId: 1, Enabled: true},
		{Group: "group-a", Model: "model-shared", ChannelId: 1, Enabled: true},
		{Group: "group-b", Model: "model-shared", ChannelId: 2, Enabled: true},
		{Group: "group-b", Model: "model-disabled", ChannelId: 2, Enabled: false},
	}).Error)
	channelURL := "https://channel-secret.example"
	require.NoError(t, db.Create(&model.Channel{
		Id: 84, Name: "CC-A", Status: common.ChannelStatusEnabled, Group: "group-a,group-b",
		Models: "model-a,model-shared", Key: "CHANNEL_SECRET", BaseURL: &channelURL, Remark: &channelURL,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/admin/options", nil)
	AdminGetModelHealthOptions(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, "owner marked")
	assert.NotContains(t, body, "owner unmarked")
	assert.NotContains(t, body, "owner disabled")
	assert.NotContains(t, body, "other owner")
	assert.NotContains(t, body, "SECRET")
	assert.Contains(t, body, `"name":"group-a","display_name":"group-a"`)
	assert.NotContains(t, body, "GROUP_A_DESCRIPTION")
	assert.NotContains(t, body, "GROUP_B_DESCRIPTION")
	assert.Contains(t, body, `"models":["model-a","model-shared"]`)
	assert.NotContains(t, body, "model-disabled")
	assert.Contains(t, body, `"channels":[{"id":84,"name":"CC-A","status":1,"groups":["group-a","group-b"],"models":["model-a","model-shared"]}]`)
	assert.NotContains(t, body, "channel-secret.example")
}

func TestModelHealthOverviewAggregatesMultipleObservedGroupsWithoutDuplicateCells(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Token{}, &model.ModelHealthTarget{}, &model.ModelHealthHistory{}, &model.PerfMetric{},
	))
	require.NoError(t, db.Create(&model.Token{Id: 105, UserId: 1, Key: "token", Status: common.TokenStatusEnabled}).Error)
	tokenID := 105
	for _, targetName := range []string{"target-one", "target-two"} {
		target := &model.ModelHealthTarget{
			Name: targetName, Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
			Protocol: model.ModelHealthProtocolOpenAIChat, PublicGroupAlias: "Combined",
			Public: true, Enabled: true, IntervalSeconds: 300, TimeoutSeconds: 45,
		}
		require.NoError(t, target.SetObservedGroups([]string{"group-a", "group-b"}))
		require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "internal-model", PublicAlias: "Public Model", Required: true}}))
		require.NoError(t, model.CreateModelHealthTarget(target))
	}
	now := time.Now().Unix()
	bucketTs := now - now%300 - 300
	require.NoError(t, db.Create(&[]model.PerfMetric{
		{ModelName: "internal-model", Group: "group-a", BucketTs: bucketTs, RequestCount: 10, SuccessCount: 10, TotalLatencyMs: 1_000, TtftSumMs: 500, TtftCount: 10, OutputTokens: 100, GenerationMs: 1_000},
		{ModelName: "internal-model", Group: "group-b", BucketTs: bucketTs, RequestCount: 90, SuccessCount: 81, TotalLatencyMs: 18_000, TtftSumMs: 9_000, TtftCount: 90, OutputTokens: 900, GenerationMs: 9_000},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h", nil)
	GetModelHealthOverview(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Summary modelHealthSummary     `json:"summary"`
			Rows    []modelHealthPublicRow `json:"rows"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data.Rows, 1)
	row := response.Data.Rows[0]
	assert.Equal(t, "Combined", row.Group)
	assert.Equal(t, int64(100), row.Observed.RequestCount)
	require.NotNil(t, row.Observed.SuccessRate)
	assert.Equal(t, 91.0, *row.Observed.SuccessRate)
	require.NotNil(t, row.Observed.AvgLatencyMs)
	assert.Equal(t, int64(190), *row.Observed.AvgLatencyMs)
	assert.Equal(t, int64(100), response.Data.Summary.ObservedRequestCount)
}

func TestBuildLocalModelHealthTargetRequiresOwnedMarkedTokenAndNormalizesGroups(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Token{}, &model.ModelHealthTarget{}, &model.ModelHealthHistory{}, &model.ModelHealthProbeToken{},
	))
	require.NoError(t, db.Create(&[]model.Token{
		{Id: 106, UserId: 1, Key: "marked", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100},
		{Id: 107, UserId: 1, Key: "unmarked", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100},
	}).Error)
	require.NoError(t, model.SetModelHealthProbeTokenMarked(106, 1, true))
	groups := []string{" group-b ", "group-a", "group-b"}
	tokenID := 106
	request := modelHealthTargetRequest{
		Name: "local target", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol:       model.ModelHealthProtocolOpenAIChat,
		Models:         []model.ModelHealthTargetModel{{Name: "model", PublicAlias: "Public", Required: true}},
		ObservedGroups: &groups, PublicGroupAlias: "Public Group", Enabled: true, Public: true,
	}

	target, err := buildModelHealthTarget(request, nil, 1)
	require.NoError(t, err)
	observedGroups, err := target.ObservedGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"group-b", "group-a"}, observedGroups)
	assert.Equal(t, "group-b", target.ObservedGroup)
	assert.Equal(t, model.ModelHealthSamplingConfirmOnFailure, target.SamplingMode)
	assert.Equal(t, 3, target.SamplesPerRun)
	assert.Equal(t, 2, target.MinimumSuccesses)
	assert.Equal(t, 3, target.SampleSpacingSeconds)
	target.LatencySLOMs = 2_500
	updated, err := buildModelHealthTarget(request, target, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2_500, updated.LatencySLOMs, "an omitted latency_slo_ms preserves the stored target value")
	zeroSLO := int64(0)
	request.LatencySLOMs = &zeroSLO
	updated, err = buildModelHealthTarget(request, target, 1)
	require.NoError(t, err)
	assert.Zero(t, updated.LatencySLOMs)
	request.LatencySLOMs = nil

	emptySamplingMode := ""
	zeroSamplingValue := 0
	request.SamplingMode = &emptySamplingMode
	request.SamplesPerRun = &zeroSamplingValue
	request.MinimumSuccesses = &zeroSamplingValue
	request.SampleSpacingSeconds = &zeroSamplingValue
	_, err = buildModelHealthTarget(request, nil, 1)
	assert.ErrorContains(t, err, "sampling_mode")
	request.SamplingMode = nil
	request.SamplesPerRun = nil
	request.MinimumSuccesses = nil
	request.SampleSpacingSeconds = nil
	validSamplingMode := model.ModelHealthSamplingFixed
	request.SamplingMode = &validSamplingMode
	request.SamplesPerRun = &zeroSamplingValue
	_, err = buildModelHealthTarget(request, nil, 1)
	assert.ErrorContains(t, err, "samples_per_run")
	request.SamplesPerRun = nil
	request.MinimumSuccesses = &zeroSamplingValue
	_, err = buildModelHealthTarget(request, nil, 1)
	assert.ErrorContains(t, err, "minimum_successes")
	request.MinimumSuccesses = nil
	request.SampleSpacingSeconds = &zeroSamplingValue
	_, err = buildModelHealthTarget(request, nil, 1)
	assert.ErrorContains(t, err, "sample_spacing_seconds")
	request.SamplingMode = nil
	request.SampleSpacingSeconds = nil

	_, err = buildModelHealthTarget(request, nil, 2)
	assert.ErrorContains(t, err, "dedicated probe token")
	unmarkedID := 107
	request.TokenID = &unmarkedID
	_, err = buildModelHealthTarget(request, nil, 1)
	assert.ErrorContains(t, err, "dedicated probe token")
}

func TestBuildModelHealthSeriesRowAggregatesAttemptsByEqualWeightRun(t *testing.T) {
	now := time.Now().Unix()
	bucket := now - now%300
	state := &modelHealthRowState{
		row: modelHealthPublicRow{Group: "Public Group", Model: "Public Model"},
		histories: []model.ModelHealthHistory{
			{ID: 1, TargetID: 1, ModelName: "internal", ProbeRunID: "run-a", RunStartedAt: bucket, AttemptIndex: 1, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeFailure, ErrorClass: service.ModelHealthErrorServer, CheckedAt: bucket + 1},
			{ID: 2, TargetID: 1, ModelName: "internal", ProbeRunID: "run-a", RunStartedAt: bucket, AttemptIndex: 2, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeSuccess, CheckedAt: bucket + 2},
			{ID: 3, TargetID: 1, ModelName: "internal", ProbeRunID: "run-a", RunStartedAt: bucket, AttemptIndex: 3, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeSuccess, CheckedAt: bucket + 3},
			{ID: 4, TargetID: 1, ModelName: "internal", ProbeRunID: "run-b", RunStartedAt: bucket, AttemptIndex: 1, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeSuccess, CheckedAt: bucket + 4},
		},
	}

	series := buildModelHealthSeriesRow(state, 300)

	require.Len(t, series.Points, 1)
	point := series.Points[0]
	require.NotNil(t, point.ProbeAvailability)
	assert.Equal(t, 83.34, *point.ProbeAvailability)
	assert.Equal(t, service.ModelHealthStatusFluctuating, point.ProbeStatus)
	assert.EqualValues(t, 2, point.ProbeRunCount)
	assert.EqualValues(t, 4, point.ProbeAttemptCount)
	assert.EqualValues(t, 3, point.ProbeSuccessCount)
}

func TestBuildModelHealthTargetValidatesPoolChannelBindings(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Token{}, &model.Channel{}, &model.ModelHealthTarget{}, &model.ModelHealthHistory{}, &model.ModelHealthProbeToken{},
	))
	require.NoError(t, db.Create(&model.Token{
		Id: 109, UserId: 1, Key: "marked", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100,
	}).Error)
	require.NoError(t, model.SetModelHealthProbeTokenMarked(109, 1, true))
	require.NoError(t, db.Create(&[]model.Channel{
		{Id: 84, Name: "CC-A", Status: common.ChannelStatusEnabled, Group: "CC-MAX", Models: "model", Key: "a"},
		{Id: 58, Name: "CC-B", Status: common.ChannelStatusManuallyDisabled, Group: "CC-MAX", Models: "model", Key: "b"},
	}).Error)
	tokenID, publish, poolAlias := 109, true, "CCMAX-A池"
	channelIDs := []int{84}
	request := modelHealthTargetRequest{
		Name: "CCMAX-1", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol:         model.ModelHealthProtocolOpenAIChat,
		Models:           []model.ModelHealthTargetModel{{Name: "model", PublicAlias: "Public", Required: true}},
		PublicGroupAlias: "CC-MAX", PublicPoolAlias: &poolAlias, PublishPoolDetail: &publish,
		ObservedChannelIDs: &channelIDs, Enabled: true, Public: true,
	}

	target, err := buildModelHealthTarget(request, nil, 1)
	require.NoError(t, err)
	assert.True(t, target.PublishPoolDetail)
	assert.Equal(t, "CCMAX-A池", target.PublicPoolAlias)

	disabledIDs := []int{58}
	request.ObservedChannelIDs = &disabledIDs
	_, err = buildModelHealthTarget(request, nil, 1)
	assert.ErrorContains(t, err, "at least one enabled channel")

	require.NoError(t, target.SetObservedChannelIDs(disabledIDs))
	request.PublicPoolAlias = nil
	_, err = buildModelHealthTarget(request, target, 1)
	require.NoError(t, err, "an unchanged binding remains editable after its channel is disabled")
}

func TestModelHealthOverviewDoesNotCreateCrossTargetAliasCombinations(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Token{}, &model.ModelHealthTarget{}, &model.ModelHealthHistory{}, &model.PerfMetric{},
	))
	require.NoError(t, db.Create(&model.Token{Id: 108, UserId: 1, Key: "token", Status: common.TokenStatusEnabled}).Error)
	tokenID := 108
	createTarget := func(name, internalGroup, publicGroup, internalModel, publicModel string) {
		t.Helper()
		target := &model.ModelHealthTarget{
			Name: name, Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
			Protocol: model.ModelHealthProtocolOpenAIChat, PublicGroupAlias: publicGroup,
			Public: true, Enabled: true, IntervalSeconds: 300, TimeoutSeconds: 45,
		}
		require.NoError(t, target.SetObservedGroups([]string{internalGroup}))
		require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{
			Name: internalModel, PublicAlias: publicModel, Required: true,
		}}))
		require.NoError(t, model.CreateModelHealthTarget(target))
	}
	createTarget("target-a", "group-a", "Group A", "model-a", "Model A")
	createTarget("target-b", "group-b", "Group B", "model-b", "Model B")
	now := time.Now().Unix()
	bucketTs := now - now%300 - 300
	require.NoError(t, db.Create(&[]model.PerfMetric{
		{ModelName: "model-a", Group: "group-a", BucketTs: bucketTs, RequestCount: 10, SuccessCount: 10},
		{ModelName: "model-b", Group: "group-b", BucketTs: bucketTs, RequestCount: 20, SuccessCount: 20},
		{ModelName: "model-a", Group: "group-b", BucketTs: bucketTs, RequestCount: 1_000, SuccessCount: 0},
		{ModelName: "model-b", Group: "group-a", BucketTs: bucketTs, RequestCount: 2_000, SuccessCount: 0},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h", nil)
	GetModelHealthOverview(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Summary modelHealthSummary     `json:"summary"`
			Rows    []modelHealthPublicRow `json:"rows"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data.Rows, 2)
	assert.Equal(t, int64(30), response.Data.Summary.ObservedRequestCount)
	for _, row := range response.Data.Rows {
		assert.NotEqual(t, "Group A\x00Model B", publicHealthRowKey(row.Group, row.Model))
		assert.NotEqual(t, "Group B\x00Model A", publicHealthRowKey(row.Group, row.Model))
	}
}

func TestModelHealthOverviewKeepsPoolDetailsSeparateUnderOneSummary(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.ModelHealthTarget{}, &model.ModelHealthHistory{}, &model.PerfMetric{}, &model.PerfChannelMetric{},
	))
	settingValue, ok := config.GlobalConfig.Get("model_health_setting").(*model_health_setting.ModelHealthSetting)
	require.True(t, ok)
	previousSetting := *settingValue
	settingValue.PoolDetailsEnabled = true
	t.Cleanup(func() { *settingValue = previousSetting })
	require.NoError(t, db.Create(&[]model.Channel{
		{Id: 84, Name: "CC-A", Status: common.ChannelStatusEnabled, Group: "CC-MAX", Models: "claude-opus-4-8", Key: "a"},
		{Id: 58, Name: "CC-B", Status: common.ChannelStatusEnabled, Group: "CC-MAX", Models: "claude-opus-4-8", Key: "b"},
	}).Error)

	tokenID := 110
	createPoolTarget := func(name, pool string, channelID int) {
		t.Helper()
		target := &model.ModelHealthTarget{
			Name: name, Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
			Protocol: model.ModelHealthProtocolOpenAIChat, PublicGroupAlias: "CC-MAX",
			PublicPoolAlias: pool, PublishPoolDetail: true,
			Public: true, Enabled: true, IntervalSeconds: 300, TimeoutSeconds: 45,
		}
		require.NoError(t, target.SetObservedGroups([]string{"CC-MAX"}))
		require.NoError(t, target.SetObservedChannelIDs([]int{channelID}))
		require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{
			Name: "claude-opus-4-8", PublicAlias: "claude-opus-4-8", Required: true,
		}}))
		require.NoError(t, model.CreateModelHealthTarget(target))
	}
	createPoolTarget("CCMAX-1", "CCMAX-A池", 84)
	createPoolTarget("CCMAX-2", "CCMAX-B池", 58)

	now := time.Now().Unix()
	bucketTs := now - now%300 - 300
	require.NoError(t, db.Create(&model.PerfMetric{
		ModelName: "claude-opus-4-8", Group: "CC-MAX", BucketTs: bucketTs,
		RequestCount: 30, SuccessCount: 27, TotalLatencyMs: 3_000,
	}).Error)
	require.NoError(t, db.Create(&[]model.PerfChannelMetric{
		{ChannelId: 84, ModelName: "claude-opus-4-8", Group: "CC-MAX", BucketTs: bucketTs,
			RequestCount: 20, SuccessCount: 20, TotalLatencyMs: 2_000},
		{ChannelId: 58, ModelName: "claude-opus-4-8", Group: "CC-MAX", BucketTs: bucketTs,
			RequestCount: 10, SuccessCount: 5, TotalLatencyMs: 2_000},
	}).Error)
	catalogRecorder := httptest.NewRecorder()
	catalogContext, _ := gin.CreateTestContext(catalogRecorder)
	catalogContext.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/catalog", nil)
	GetModelHealthCatalog(catalogContext)
	require.Equal(t, http.StatusOK, catalogRecorder.Code)
	var catalogResponse struct {
		Data struct {
			Pools []modelHealthPoolCatalogItem `json:"pools"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(catalogRecorder.Body.Bytes(), &catalogResponse))
	require.Len(t, catalogResponse.Data.Pools, 2)
	assert.Equal(t, "CCMAX-A池", catalogResponse.Data.Pools[0].Alias)
	assert.NotContains(t, catalogRecorder.Body.String(), `"channel_id"`)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h", nil)
	GetModelHealthOverview(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Rows     []modelHealthPublicRow `json:"rows"`
			PoolRows []modelHealthPublicRow `json:"pool_rows"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data.Rows, 1)
	require.Len(t, response.Data.PoolRows, 2)
	assert.Equal(t, "CC-MAX", response.Data.Rows[0].Group)
	assert.Equal(t, "CCMAX-A池", response.Data.PoolRows[0].Pool)
	assert.Equal(t, int64(20), response.Data.PoolRows[0].Observed.RequestCount)
	assert.Equal(t, "CCMAX-B池", response.Data.PoolRows[1].Pool)
	assert.Equal(t, int64(10), response.Data.PoolRows[1].Observed.RequestCount)
	assert.NotContains(t, recorder.Body.String(), `"target_id"`)
	assert.NotContains(t, recorder.Body.String(), `"channel_id"`)

	poolKey := modelHealthPublicPoolKey("CC-MAX", "CCMAX-B池")
	filteredRecorder := httptest.NewRecorder()
	filteredContext, _ := gin.CreateTestContext(filteredRecorder)
	filteredContext.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h&pool="+poolKey, nil)
	GetModelHealthOverview(filteredContext)
	require.Equal(t, http.StatusOK, filteredRecorder.Code)
	response = struct {
		Data struct {
			Rows     []modelHealthPublicRow `json:"rows"`
			PoolRows []modelHealthPublicRow `json:"pool_rows"`
		} `json:"data"`
	}{}
	require.NoError(t, common.Unmarshal(filteredRecorder.Body.Bytes(), &response))
	require.Len(t, response.Data.Rows, 1, "pool filters retain their summary parent")
	require.Len(t, response.Data.PoolRows, 1)
	assert.Equal(t, "CCMAX-B池", response.Data.PoolRows[0].Pool)

	seriesRecorder := httptest.NewRecorder()
	seriesContext, _ := gin.CreateTestContext(seriesRecorder)
	seriesContext.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/series?window=12h", nil)
	GetModelHealthSeries(seriesContext)
	require.Equal(t, http.StatusOK, seriesRecorder.Code)
	var seriesResponse struct {
		Data struct {
			Rows     []modelHealthSeriesRow `json:"rows"`
			PoolRows []modelHealthSeriesRow `json:"pool_rows"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(seriesRecorder.Body.Bytes(), &seriesResponse))
	require.Len(t, seriesResponse.Data.Rows, 1)
	require.Len(t, seriesResponse.Data.PoolRows, 2)
	assert.Equal(t, "CCMAX-A池", seriesResponse.Data.PoolRows[0].Pool)
	assert.NotEmpty(t, seriesResponse.Data.PoolRows[0].Points)

	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 58).Update("status", common.ChannelStatusManuallyDisabled).Error)
	disabledRecorder := httptest.NewRecorder()
	disabledContext, _ := gin.CreateTestContext(disabledRecorder)
	disabledContext.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h&pool="+poolKey, nil)
	GetModelHealthOverview(disabledContext)
	require.Equal(t, http.StatusOK, disabledRecorder.Code)
	require.NoError(t, common.Unmarshal(disabledRecorder.Body.Bytes(), &response))
	require.Len(t, response.Data.PoolRows, 1)
	assert.Zero(t, response.Data.PoolRows[0].Observed.RequestCount)
	assert.Equal(t, service.ModelHealthStatusIdle, response.Data.PoolRows[0].Observed.Status)

	settingValue.PoolDetailsEnabled = false
	disabledGlobalRecorder := httptest.NewRecorder()
	disabledGlobalContext, _ := gin.CreateTestContext(disabledGlobalRecorder)
	disabledGlobalContext.Request = httptest.NewRequest(http.MethodGet, "/api/model-health/overview?window=12h", nil)
	GetModelHealthOverview(disabledGlobalContext)
	require.Equal(t, http.StatusOK, disabledGlobalRecorder.Code)
	require.NoError(t, common.Unmarshal(disabledGlobalRecorder.Body.Bytes(), &response))
	require.Len(t, response.Data.Rows, 1)
	assert.Empty(t, response.Data.PoolRows)
}
