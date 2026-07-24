package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_health_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelHealthTargetTestDB(t *testing.T) {
	t.Helper()
	originalDB := model.DB
	database, err := gorm.Open(sqlite.Open("file:model-health-target-test?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(
		&model.Token{},
		&model.ModelHealthTarget{},
		&model.ModelHealthHistory{},
		&model.ModelHealthProbeToken{},
	))
	model.DB = database
	t.Cleanup(func() {
		model.DB = originalDB
		sqlDB, dbErr := database.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestRunModelHealthTargetPersistsOneSanitizedHistoryPerModel(t *testing.T) {
	setupModelHealthTargetTestDB(t)
	settingValue, ok := config.GlobalConfig.Get("model_health_setting").(*model_health_setting.ModelHealthSetting)
	require.True(t, ok)
	previousSetting := *settingValue
	settingValue.MultiSampleEnabled = false
	t.Cleanup(func() { *settingValue = previousSetting })
	require.NoError(t, model.DB.Create(&model.Token{Id: 1, UserId: 1, Key: "local-token", Status: 1}).Error)
	require.NoError(t, model.DB.Create(&model.ModelHealthProbeToken{TokenID: 1, MarkedBy: 1}).Error)
	now := time.Unix(1_700_000_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"42"}}]}`))
	}))
	defer server.Close()
	originalServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = server.URL
	t.Cleanup(func() { system_setting.ServerAddress = originalServerAddress })
	tokenID := 1
	target := &model.ModelHealthTarget{
		Name: "local", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: model.ModelHealthProtocolOpenAIChat, Enabled: true,
		IntervalSeconds: 300, TimeoutSeconds: 10, SamplingMode: model.ModelHealthSamplingFixed,
		SamplesPerRun: 3, MinimumSuccesses: 2, SampleSpacingSeconds: 1,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{
		{Name: "required-model", Required: true},
		{Name: "observed-model", Required: false},
	}))
	require.NoError(t, model.CreateModelHealthTarget(target))
	executor := fixedModelHealthExecutor(server.Client(), now)

	result, err := runModelHealthTarget(context.Background(), target, executor)

	require.NoError(t, err)
	assert.Equal(t, target.ID, result.TargetID)
	assert.Equal(t, ModelHealthStatusNormal, result.Status)
	require.Len(t, result.Results, 2)
	var histories []model.ModelHealthHistory
	require.NoError(t, model.DB.Order("model_name ASC").Find(&histories).Error)
	require.Len(t, histories, 2)
	assert.Equal(t, model.ModelHealthProbeSuccess, histories[0].Status)
	assert.Empty(t, histories[0].ErrorClass)
	assert.Equal(t, "observed-model", histories[0].ModelName)
	assert.Equal(t, "required-model", histories[1].ModelName)
	assert.Equal(t, 1, histories[0].PlannedAttempts)
	assert.Equal(t, 1, histories[0].AttemptIndex)
	assert.Equal(t, histories[0].ProbeRunID, histories[1].ProbeRunID)
	assert.NotEmpty(t, histories[0].ProbeRunID)

	storedTarget, err := model.GetModelHealthTarget(target.ID)
	require.NoError(t, err)
	assert.Equal(t, now.Unix(), storedTarget.LastCheckedAt)
}

func TestRunModelHealthTargetPersistsCompleteMultiSampleBatch(t *testing.T) {
	setupModelHealthTargetTestDB(t)
	settingValue, ok := config.GlobalConfig.Get("model_health_setting").(*model_health_setting.ModelHealthSetting)
	require.True(t, ok)
	previousSetting := *settingValue
	settingValue.MultiSampleEnabled = true
	t.Cleanup(func() { *settingValue = previousSetting })

	require.NoError(t, model.DB.Create(&model.Token{Id: 12, UserId: 1, Key: "local-token", Status: 1}).Error)
	require.NoError(t, model.DB.Create(&model.ModelHealthProbeToken{TokenID: 12, MarkedBy: 1}).Error)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"42"}}]}`))
	}))
	defer server.Close()
	originalServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = server.URL
	t.Cleanup(func() { system_setting.ServerAddress = originalServerAddress })
	tokenID := 12
	target := &model.ModelHealthTarget{
		Name: "multi", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: model.ModelHealthProtocolOpenAIChat, Enabled: true,
		IntervalSeconds: 300, TimeoutSeconds: 10, SamplingMode: model.ModelHealthSamplingFixed,
		SamplesPerRun: 3, MinimumSuccesses: 2, SampleSpacingSeconds: 1,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "model", Required: true}}))
	require.NoError(t, model.CreateModelHealthTarget(target))
	now := time.Unix(1_700_000_000, 0)

	result, err := runModelHealthTargetWithLimiterAndWait(context.Background(), target, fixedModelHealthExecutor(server.Client(), now), NewModelHealthProbeLimiter(1), func(context.Context, time.Duration) error { return nil })

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, 3, result.Results[0].ActualAttempts)
	var histories []model.ModelHealthHistory
	require.NoError(t, model.DB.Order("attempt_index ASC").Find(&histories).Error)
	require.Len(t, histories, 3)
	probeRunID := histories[0].ProbeRunID
	require.NotEmpty(t, probeRunID)
	for index, history := range histories {
		assert.Equal(t, probeRunID, history.ProbeRunID)
		assert.Equal(t, now.Unix(), history.RunStartedAt)
		assert.Equal(t, index+1, history.AttemptIndex)
		assert.Equal(t, model.ModelHealthSamplingFixed, history.SamplingMode)
		assert.Equal(t, 3, history.PlannedAttempts)
		assert.Equal(t, 2, history.MinimumSuccesses)
	}
}

func TestRunModelHealthTargetDisablesTargetWhenCredentialEnvelopeBreaks(t *testing.T) {
	setupModelHealthTargetTestDB(t)
	target := &model.ModelHealthTarget{
		Name: "upstream", Mode: model.ModelHealthModeUpstream,
		Protocol: model.ModelHealthProtocolOpenAIChat, Enabled: true,
		EndpointEncrypted: "v1:broken", APIKeyEncrypted: "v1:broken",
		IntervalSeconds: 300, TimeoutSeconds: 10,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "model", Required: true}}))
	require.NoError(t, model.CreateModelHealthTarget(target))

	_, err := runModelHealthTarget(context.Background(), target, fixedModelHealthExecutor(http.DefaultClient, time.Now()))

	require.ErrorIs(t, err, ErrModelHealthCredentialsNeedReplacement)
	storedTarget, getErr := model.GetModelHealthTarget(target.ID)
	require.NoError(t, getErr)
	assert.False(t, storedTarget.Enabled)
}

func TestRunModelHealthTargetRecordsMissingLocalTokenAtNormalCadence(t *testing.T) {
	setupModelHealthTargetTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	missingTokenID := 404
	target := &model.ModelHealthTarget{
		Name: "missing-token", Mode: model.ModelHealthModeLocal, TokenID: &missingTokenID,
		Protocol: model.ModelHealthProtocolOpenAIChat, Enabled: true,
		IntervalSeconds: 300, TimeoutSeconds: 10,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "required-model", Required: true}}))
	require.NoError(t, model.CreateModelHealthTarget(target))

	result, err := runModelHealthTarget(context.Background(), target, fixedModelHealthExecutor(http.DefaultClient, now))

	require.NoError(t, err)
	assert.Equal(t, ModelHealthStatusAbnormal, result.Status)
	require.Len(t, result.Results, 1)
	assert.Equal(t, model.ModelHealthProbeAuthFailure, result.Results[0].Status)
	assert.Equal(t, ModelHealthErrorCredential, result.Results[0].ErrorClass)
	storedTarget, err := model.GetModelHealthTarget(target.ID)
	require.NoError(t, err)
	assert.Equal(t, now.Unix(), storedTarget.LastCheckedAt)
	var histories []model.ModelHealthHistory
	require.NoError(t, model.DB.Find(&histories).Error)
	require.Len(t, histories, 1)
	assert.Equal(t, ModelHealthErrorCredential, histories[0].ErrorClass)
}

func TestRunModelHealthTargetRejectsUnmarkedLocalToken(t *testing.T) {
	setupModelHealthTargetTestDB(t)
	require.NoError(t, model.DB.Create(&model.Token{Id: 1, UserId: 1, Key: "local-token", Status: 1}).Error)
	now := time.Unix(1_700_000_000, 0)
	tokenID := 1
	target := &model.ModelHealthTarget{
		Name: "unmarked-token", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: model.ModelHealthProtocolOpenAIChat, Enabled: true,
		IntervalSeconds: 300, TimeoutSeconds: 10,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "required-model", Required: true}}))
	require.NoError(t, model.CreateModelHealthTarget(target))

	result, err := runModelHealthTarget(context.Background(), target, fixedModelHealthExecutor(http.DefaultClient, now))

	require.NoError(t, err)
	assert.Equal(t, ModelHealthStatusAbnormal, result.Status)
	require.Len(t, result.Results, 1)
	assert.Equal(t, model.ModelHealthProbeAuthFailure, result.Results[0].Status)
	assert.Equal(t, ModelHealthErrorCredential, result.Results[0].ErrorClass)
}

func TestRollupRequiredModelHealthProbeResultsIgnoresOptionalFailures(t *testing.T) {
	models := []model.ModelHealthTargetModel{
		{Name: "required-a", Required: true},
		{Name: "required-b", Required: true},
		{Name: "optional", Required: false},
	}
	results := []ModelHealthProbeResult{
		{Model: "required-a", Status: model.ModelHealthProbeSuccess},
		{Model: "required-b", Status: model.ModelHealthProbeSuccess},
		{Model: "optional", Status: model.ModelHealthProbeFailure},
	}
	assert.Equal(t, ModelHealthStatusNormal, RollupRequiredModelHealthProbeResults(models, results))

	results[1].Status = model.ModelHealthProbeFailure
	assert.Equal(t, ModelHealthStatusAbnormal, RollupRequiredModelHealthProbeResults(models, results))
}

func TestModelHealthProbeLimiterEnforcesGlobalBound(t *testing.T) {
	limiter := NewModelHealthProbeLimiter(2)
	require.True(t, limiter.acquire(context.Background()))
	require.True(t, limiter.acquire(context.Background()))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, limiter.acquire(canceled))
	limiter.release()
	limiter.release()
}

func TestModelHealthProbeLimiterShrinksWithoutOverlappingCapacity(t *testing.T) {
	limiter := NewModelHealthProbeLimiter(2)
	require.True(t, limiter.acquire(context.Background()))
	require.True(t, limiter.acquire(context.Background()))
	limiter.setConcurrency(1)
	acquired := make(chan struct{}, 1)
	go func() {
		if limiter.acquire(context.Background()) {
			acquired <- struct{}{}
		}
	}()

	limiter.release()
	select {
	case <-acquired:
		t.Fatal("lowering the limit admitted a new probe while the old active count still met the new limit")
	case <-time.After(20 * time.Millisecond):
	}
	limiter.release()
	select {
	case <-acquired:
		limiter.release()
	case <-time.After(time.Second):
		t.Fatal("waiting probe was not admitted after the old limiter became idle")
	}
}

func TestValidationAndScheduledRunShareProcessLimiter(t *testing.T) {
	setupModelHealthTargetTestDB(t)
	settingValue, ok := config.GlobalConfig.Get("model_health_setting").(*model_health_setting.ModelHealthSetting)
	require.True(t, ok)
	previousSetting := *settingValue
	settingValue.Concurrency = 1
	settingValue.MultiSampleEnabled = false
	t.Cleanup(func() {
		*settingValue = previousSetting
		sharedModelHealthProbeLimiter.setConcurrency(previousSetting.Concurrency)
	})

	var active, maximum atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	originalServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = server.URL
	t.Cleanup(func() { system_setting.ServerAddress = originalServerAddress })
	require.NoError(t, model.DB.Create(&model.Token{Id: 21, UserId: 1, Key: "local-token", Status: 1}).Error)
	require.NoError(t, model.DB.Create(&model.ModelHealthProbeToken{TokenID: 21, MarkedBy: 1}).Error)
	tokenID := 21
	target := &model.ModelHealthTarget{
		Name: "scheduled", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: model.ModelHealthProtocolOpenAIChat, Enabled: true,
		IntervalSeconds: 300, TimeoutSeconds: 1,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "model", Required: true}}))
	require.NoError(t, model.CreateModelHealthTarget(target))
	type outcome struct{ err error }
	scheduledDone := make(chan outcome, 1)
	go func() {
		_, err := RunModelHealthTargetWithLimiter(context.Background(), target, SharedModelHealthProbeLimiter())
		scheduledDone <- outcome{err: err}
	}()
	<-entered
	validationDone := make(chan outcome, 1)
	go func() {
		_, err := ExecuteModelHealthProbeBatch(context.Background(), ModelHealthProbeConfig{
			Protocol: model.ModelHealthProtocolOpenAIChat, BaseURL: server.URL, APIKey: "local-token",
			Model: "model", Timeout: time.Second, Local: true,
		}, ModelHealthSamplingConfig{Mode: model.ModelHealthSamplingFixed, PlannedAttempts: 1, MinimumSuccesses: 1, Spacing: time.Second})
		validationDone <- outcome{err: err}
	}()
	select {
	case <-entered:
		close(release)
		t.Fatal("validation entered HTTP execution while the scheduled probe held the shared limiter")
	case <-time.After(30 * time.Millisecond):
		close(release)
	}
	require.NoError(t, (<-scheduledDone).err)
	require.NoError(t, (<-validationDone).err)
	assert.EqualValues(t, 1, maximum.Load())
}

func TestExecuteModelHealthProbeBatchSamplingModes(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		planned      int
		minimum      int
		statuses     []int
		batchStatus  string
		actual       int
		successes    int
		availability float64
	}{
		{name: "fixed all success", mode: model.ModelHealthSamplingFixed, planned: 3, minimum: 2, statuses: []int{200, 200, 200}, batchStatus: ModelHealthStatusNormal, actual: 3, successes: 3, availability: 100},
		{name: "fixed partial success", mode: model.ModelHealthSamplingFixed, planned: 3, minimum: 2, statuses: []int{500, 200, 200}, batchStatus: ModelHealthStatusFluctuating, actual: 3, successes: 2, availability: 66.67},
		{name: "fixed below minimum", mode: model.ModelHealthSamplingFixed, planned: 3, minimum: 2, statuses: []int{200, 500, 500}, batchStatus: ModelHealthStatusAbnormal, actual: 3, successes: 1, availability: 33.33},
		{name: "fixed all failed", mode: model.ModelHealthSamplingFixed, planned: 3, minimum: 2, statuses: []int{500, 500, 500}, batchStatus: ModelHealthStatusAbnormal, actual: 3, successes: 0, availability: 0},
		{name: "fixed five attempts", mode: model.ModelHealthSamplingFixed, planned: 5, minimum: 3, statuses: []int{200, 500, 200, 500, 200}, batchStatus: ModelHealthStatusFluctuating, actual: 5, successes: 3, availability: 60},
		{name: "confirm first success", mode: model.ModelHealthSamplingConfirmOnFailure, planned: 3, minimum: 2, statuses: []int{200}, batchStatus: ModelHealthStatusNormal, actual: 1, successes: 1, availability: 100},
		{name: "confirm transient failure completes batch", mode: model.ModelHealthSamplingConfirmOnFailure, planned: 3, minimum: 2, statuses: []int{500, 200, 200}, batchStatus: ModelHealthStatusFluctuating, actual: 3, successes: 2, availability: 66.67},
		{name: "deterministic auth failure", mode: model.ModelHealthSamplingFixed, planned: 3, minimum: 2, statuses: []int{401, 200, 200}, batchStatus: ModelHealthStatusAbnormal, actual: 1, successes: 0, availability: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				index := int(requestCount.Add(1)) - 1
				status := tt.statuses[min(index, len(tt.statuses)-1)]
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"42"}}]}`))
				}
			}))
			defer server.Close()
			executor := fixedModelHealthExecutor(server.Client(), time.Unix(1_700_000_000, 0))
			limiter := NewModelHealthProbeLimiter(1)
			var waitCount atomic.Int32
			batch, err := executeModelHealthProbeBatch(context.Background(), executor, limiter, ModelHealthProbeConfig{
				Protocol: model.ModelHealthProtocolOpenAIChat, BaseURL: server.URL, APIKey: "token",
				Model: "model", Timeout: time.Second, Local: true,
			}, ModelHealthSamplingConfig{
				Mode: tt.mode, PlannedAttempts: tt.planned, MinimumSuccesses: tt.minimum, Spacing: time.Second,
			}, func(ctx context.Context, _ time.Duration) error {
				waitCount.Add(1)
				require.True(t, limiter.acquire(ctx), "the per-attempt limiter slot must be released during spacing")
				limiter.release()
				return nil
			})

			require.NoError(t, err)
			assert.Equal(t, tt.batchStatus, batch.BatchStatus)
			assert.Equal(t, tt.actual, batch.ActualAttempts)
			assert.Equal(t, tt.successes, batch.SuccessCount)
			assert.Equal(t, tt.availability, batch.Availability)
			assert.EqualValues(t, max(0, tt.actual-1), waitCount.Load())
			require.Len(t, batch.Attempts, tt.actual)
			for index, attempt := range batch.Attempts {
				assert.Equal(t, index+1, attempt.AttemptIndex)
			}
		})
	}
}

func TestRunModelHealthTargetCancellationDoesNotPersistPartialBatch(t *testing.T) {
	setupModelHealthTargetTestDB(t)
	settingValue, ok := config.GlobalConfig.Get("model_health_setting").(*model_health_setting.ModelHealthSetting)
	require.True(t, ok)
	previousSetting := *settingValue
	settingValue.MultiSampleEnabled = true
	t.Cleanup(func() { *settingValue = previousSetting })

	require.NoError(t, model.DB.Create(&model.Token{Id: 11, UserId: 1, Key: "local-token", Status: 1}).Error)
	require.NoError(t, model.DB.Create(&model.ModelHealthProbeToken{TokenID: 11, MarkedBy: 1}).Error)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"42"}}]}`))
	}))
	defer server.Close()
	originalServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = server.URL
	t.Cleanup(func() { system_setting.ServerAddress = originalServerAddress })
	tokenID := 11
	target := &model.ModelHealthTarget{
		Name: "cancelled", Mode: model.ModelHealthModeLocal, TokenID: &tokenID,
		Protocol: model.ModelHealthProtocolOpenAIChat, Enabled: true,
		IntervalSeconds: 300, TimeoutSeconds: 10, SamplingMode: model.ModelHealthSamplingFixed,
		SamplesPerRun: 3, MinimumSuccesses: 2, SampleSpacingSeconds: 1,
	}
	require.NoError(t, target.SetModels([]model.ModelHealthTargetModel{{Name: "model", Required: true}}))
	require.NoError(t, model.CreateModelHealthTarget(target))
	ctx, cancel := context.WithCancel(context.Background())
	executor := fixedModelHealthExecutor(server.Client(), time.Unix(1_700_000_000, 0))

	_, err := runModelHealthTargetWithLimiterAndWait(ctx, target, executor, NewModelHealthProbeLimiter(1), func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	})

	require.ErrorIs(t, err, context.Canceled)
	var historyCount int64
	require.NoError(t, model.DB.Model(&model.ModelHealthHistory{}).Count(&historyCount).Error)
	assert.Zero(t, historyCount)
	stored, getErr := model.GetModelHealthTarget(target.ID)
	require.NoError(t, getErr)
	assert.Zero(t, stored.LastCheckedAt)
}
