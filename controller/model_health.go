package controller

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_health_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

type modelHealthCatalogItem struct {
	Alias string `json:"alias"`
}

type modelHealthPoolCatalogItem struct {
	Key   string `json:"key"`
	Alias string `json:"alias"`
	Group string `json:"group"`
}

type modelHealthProbeView struct {
	Status            string   `json:"status"`
	Availability      *float64 `json:"availability"`
	SampleCount       int64    `json:"sample_count"`
	ProbeRunCount     int64    `json:"probe_run_count"`
	ProbeAttemptCount int64    `json:"probe_attempt_count"`
	ProbeSuccessCount int64    `json:"probe_success_count"`
	LatestLatencyMs   *int64   `json:"latest_latency_ms"`
	LastCheckedAt     *string  `json:"last_checked_at,omitempty"`
}

type modelHealthObservedView struct {
	Status       string   `json:"status"`
	SuccessRate  *float64 `json:"success_rate"`
	RequestCount int64    `json:"request_count"`
	AvgLatencyMs *int64   `json:"avg_latency_ms"`
	AvgTtftMs    *int64   `json:"avg_ttft_ms"`
	AvgTps       *float64 `json:"avg_tps"`
}

type modelHealthPublicRow struct {
	Group          string                  `json:"group"`
	Model          string                  `json:"model"`
	PoolKey        string                  `json:"pool_key,omitempty"`
	Pool           string                  `json:"pool,omitempty"`
	PoolAlias      string                  `json:"pool_alias,omitempty"`
	Status         string                  `json:"status"`
	SignalConflict bool                    `json:"signal_conflict"`
	Probe          modelHealthProbeView    `json:"probe"`
	Observed       modelHealthObservedView `json:"observed"`
}

type modelHealthSummary struct {
	ProbeAvailability    *float64 `json:"probe_availability"`
	ProbeRunCount        int64    `json:"probe_run_count"`
	ProbeAttemptCount    int64    `json:"probe_attempt_count"`
	ProbeSuccessCount    int64    `json:"probe_success_count"`
	ObservedSuccessRate  *float64 `json:"observed_success_rate"`
	ObservedRequestCount int64    `json:"observed_request_count"`
	Healthy              int      `json:"healthy"`
	Fluctuating          int      `json:"fluctuating"`
	Unstable             int      `json:"unstable"`
	Idle                 int      `json:"idle"`
}

type modelHealthPoint struct {
	Ts                   int64    `json:"ts"`
	ProbeAvailability    *float64 `json:"probe_availability"`
	ProbeStatus          string   `json:"probe_status"`
	ProbeRunCount        int64    `json:"probe_run_count"`
	ProbeAttemptCount    int64    `json:"probe_attempt_count"`
	ProbeSuccessCount    int64    `json:"probe_success_count"`
	ObservedSuccessRate  *float64 `json:"observed_success_rate"`
	ObservedRequestCount int64    `json:"observed_request_count"`
}

type modelHealthSeriesRow struct {
	Group     string             `json:"group"`
	Model     string             `json:"model"`
	PoolKey   string             `json:"pool_key,omitempty"`
	Pool      string             `json:"pool,omitempty"`
	PoolAlias string             `json:"pool_alias,omitempty"`
	Points    []modelHealthPoint `json:"points"`
}

type modelHealthPublication struct {
	targetID         int64
	internalModel    string
	internalGroups   []string
	modelAlias       string
	groupAlias       string
	poolAlias        string
	poolKey          string
	publishPool      bool
	activeChannelIDs []int
	required         bool
	interval         int
	latencySLO       int64
}

type modelHealthRowState struct {
	row           modelHealthPublicRow
	histories     []model.ModelHealthHistory
	publications  []modelHealthPublication
	observedCells []perfmetrics.MatrixCell
	observedKeys  map[string]struct{}
	observed      *perfmetrics.MatrixCell
}

type modelHealthPublicData struct {
	generatedAt   time.Time
	window        string
	bucketSeconds int64
	rows          []modelHealthPublicRow
	poolRows      []modelHealthPublicRow
	series        []modelHealthSeriesRow
	poolSeries    []modelHealthSeriesRow
	summary       modelHealthSummary
}

func GetModelHealthCatalog(c *gin.Context) {
	_, models, groups, pools, err := modelHealthPublications()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	modelItems := make([]modelHealthCatalogItem, 0, len(models))
	for _, alias := range sortedAliasIndexKeys(models) {
		modelItems = append(modelItems, modelHealthCatalogItem{Alias: alias})
	}
	groupItems := make([]modelHealthCatalogItem, 0, len(groups))
	for _, alias := range sortedAliasIndexKeys(groups) {
		groupItems = append(groupItems, modelHealthCatalogItem{Alias: alias})
	}
	poolItems := make([]modelHealthPoolCatalogItem, 0, len(pools))
	poolKeys := make([]string, 0, len(pools))
	for key := range pools {
		poolKeys = append(poolKeys, key)
	}
	sort.Slice(poolKeys, func(i, j int) bool {
		left, right := pools[poolKeys[i]], pools[poolKeys[j]]
		if left.Group == right.Group {
			return left.Alias < right.Alias
		}
		return left.Group < right.Group
	})
	for _, key := range poolKeys {
		poolItems = append(poolItems, pools[key])
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"models": modelItems, "groups": groupItems, "pools": poolItems,
	}})
}

func GetModelHealthOverview(c *gin.Context) {
	data, err := buildModelHealthPublicData(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"generated_at": data.generatedAt.UTC().Format(time.RFC3339),
		"window":       data.window, "bucket_seconds": data.bucketSeconds,
		"summary": data.summary, "rows": data.rows, "pool_rows": data.poolRows,
	}})
}

func GetModelHealthSeries(c *gin.Context) {
	data, err := buildModelHealthPublicData(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"bucket_seconds": data.bucketSeconds, "rows": data.series, "pool_rows": data.poolSeries,
	}})
}

func buildModelHealthPublicData(c *gin.Context) (modelHealthPublicData, error) {
	now := time.Now()
	window, hours, bucketSeconds := parseModelHealthWindow(c.Query("window"))
	publications, modelAliases, groupAliases, poolCatalog, err := modelHealthPublications()
	if err != nil {
		return modelHealthPublicData{}, err
	}
	selectedModels := queryAliasSet(c, "model")
	selectedGroups := queryAliasSet(c, "group")
	selectedPools := queryAliasSet(c, "pool")
	if len(selectedModels) > 0 {
		for alias := range selectedModels {
			if _, ok := modelAliases[alias]; !ok {
				return emptyModelHealthPublicData(now, window, bucketSeconds), nil
			}
		}
	}
	if len(selectedGroups) > 0 {
		for alias := range selectedGroups {
			if _, ok := groupAliases[alias]; !ok {
				return emptyModelHealthPublicData(now, window, bucketSeconds), nil
			}
		}
	}
	if len(selectedPools) > 0 {
		for key := range selectedPools {
			if _, ok := poolCatalog[key]; !ok {
				return emptyModelHealthPublicData(now, window, bucketSeconds), nil
			}
		}
	}

	basePublications := make([]modelHealthPublication, 0, len(publications))
	for _, publication := range publications {
		if aliasSelected(selectedModels, publication.modelAlias) && aliasSelected(selectedGroups, publication.groupAlias) {
			basePublications = append(basePublications, publication)
		}
	}
	visibleParents := map[string]struct{}{}
	if len(selectedPools) > 0 {
		for _, publication := range basePublications {
			if publication.publishPool && aliasSelected(selectedPools, publication.poolKey) {
				visibleParents[publicHealthRowKey(publication.groupAlias, publication.modelAlias)] = struct{}{}
			}
		}
		if len(visibleParents) == 0 {
			return emptyModelHealthPublicData(now, window, bucketSeconds), nil
		}
	}
	filteredPublications := make([]modelHealthPublication, 0, len(basePublications))
	poolPublications := make([]modelHealthPublication, 0, len(basePublications))
	internalModels := map[string]struct{}{}
	internalGroups := map[string]struct{}{}
	for _, publication := range basePublications {
		parentKey := publicHealthRowKey(publication.groupAlias, publication.modelAlias)
		if len(visibleParents) > 0 {
			if _, ok := visibleParents[parentKey]; !ok {
				continue
			}
		}
		filteredPublications = append(filteredPublications, publication)
		if !publication.publishPool || !aliasSelected(selectedPools, publication.poolKey) {
			continue
		}
		poolPublications = append(poolPublications, publication)
	}
	for _, publication := range filteredPublications {
		if publication.internalModel != "" {
			internalModels[publication.internalModel] = struct{}{}
		}
		for _, internalGroup := range publication.internalGroups {
			if internalGroup != "" {
				internalGroups[internalGroup] = struct{}{}
			}
		}
	}
	matrix, err := perfmetrics.QueryMatrix(perfmetrics.MatrixParams{
		Models: sortedSetKeys(internalModels), Groups: sortedSetKeys(internalGroups),
		Hours: hours, BucketSeconds: bucketSeconds,
	})
	if err != nil {
		return modelHealthPublicData{}, err
	}
	channelMatrix := perfmetrics.MatrixResult{}
	if len(poolPublications) > 0 {
		poolModels := map[string]struct{}{}
		poolGroups := map[string]struct{}{}
		poolChannelIDs := map[int]struct{}{}
		for _, publication := range poolPublications {
			poolModels[publication.internalModel] = struct{}{}
			for _, group := range publication.internalGroups {
				poolGroups[group] = struct{}{}
			}
			for _, channelID := range publication.activeChannelIDs {
				poolChannelIDs[channelID] = struct{}{}
			}
		}
		channelIDs := make([]int, 0, len(poolChannelIDs))
		for channelID := range poolChannelIDs {
			channelIDs = append(channelIDs, channelID)
		}
		sort.Ints(channelIDs)
		channelMatrix, err = perfmetrics.QueryChannelMatrix(perfmetrics.ChannelMatrixParams{
			ChannelIDs: channelIDs, Models: sortedSetKeys(poolModels), Groups: sortedSetKeys(poolGroups),
			Hours: hours, BucketSeconds: bucketSeconds,
		})
		if err != nil {
			return modelHealthPublicData{}, err
		}
	}
	targetIDs := map[int64]struct{}{}
	for _, publication := range filteredPublications {
		if publication.targetID > 0 {
			targetIDs[publication.targetID] = struct{}{}
		}
	}
	historyStart := now.Add(-time.Duration(hours) * time.Hour).Unix()
	if hours < 12 {
		historyStart = now.Add(-12 * time.Hour).Unix()
	}
	histories, err := model.GetModelHealthHistories(sortedInt64SetKeys(targetIDs), nil, historyStart, now.Unix())
	if err != nil {
		return modelHealthPublicData{}, err
	}
	historyByTargetModel := map[string][]model.ModelHealthHistory{}
	for _, history := range histories {
		key := strconv.FormatInt(history.TargetID, 10) + "\x00" + history.ModelName
		historyByTargetModel[key] = append(historyByTargetModel[key], history)
	}

	states := map[string]*modelHealthRowState{}
	for _, publication := range filteredPublications {
		key := publicHealthRowKey(publication.groupAlias, publication.modelAlias)
		state := ensureModelHealthRowState(states, publication.groupAlias, publication.modelAlias)
		state.publications = append(state.publications, publication)
		historyKey := strconv.FormatInt(publication.targetID, 10) + "\x00" + publication.internalModel
		state.histories = append(state.histories, historyByTargetModel[historyKey]...)
		states[key] = state
	}
	poolStates := map[string]*modelHealthRowState{}
	poolRowsByInternal := map[string]map[string]struct{}{}
	for _, publication := range poolPublications {
		key := publicPoolHealthRowKey(publication.groupAlias, publication.modelAlias, publication.poolAlias)
		state := poolStates[key]
		if state == nil {
			state = &modelHealthRowState{
				row: modelHealthPublicRow{Group: publication.groupAlias, Model: publication.modelAlias,
					PoolKey: publication.poolKey, Pool: publication.poolAlias, PoolAlias: publication.poolAlias},
				observedKeys: map[string]struct{}{},
			}
			poolStates[key] = state
		}
		state.publications = append(state.publications, publication)
		historyKey := strconv.FormatInt(publication.targetID, 10) + "\x00" + publication.internalModel
		state.histories = append(state.histories, historyByTargetModel[historyKey]...)
		for _, channelID := range publication.activeChannelIDs {
			for _, internalGroup := range publication.internalGroups {
				internalKey := modelHealthChannelCellKey(channelID, internalGroup, publication.internalModel)
				if poolRowsByInternal[internalKey] == nil {
					poolRowsByInternal[internalKey] = map[string]struct{}{}
				}
				poolRowsByInternal[internalKey][key] = struct{}{}
			}
		}
	}
	for _, cell := range channelMatrix.Cells {
		rowKeys := poolRowsByInternal[modelHealthChannelCellKey(cell.ChannelID, cell.Group, cell.Model)]
		for rowKey := range rowKeys {
			addModelHealthObservedCell(poolStates[rowKey], cell)
		}
	}

	publicRowsByInternal := map[string]map[string]struct{}{}
	for _, publication := range filteredPublications {
		rowKey := publicHealthRowKey(publication.groupAlias, publication.modelAlias)
		for _, internalGroup := range publication.internalGroups {
			internalKey := publicHealthRowKey(internalGroup, publication.internalModel)
			if publicRowsByInternal[internalKey] == nil {
				publicRowsByInternal[internalKey] = map[string]struct{}{}
			}
			publicRowsByInternal[internalKey][rowKey] = struct{}{}
		}
	}
	summaryObservedCells := make([]perfmetrics.MatrixCell, 0, len(matrix.Cells))
	for _, cell := range matrix.Cells {
		rowKeys := publicRowsByInternal[publicHealthRowKey(cell.Group, cell.Model)]
		if len(rowKeys) == 0 {
			continue
		}
		summaryObservedCells = append(summaryObservedCells, cell)
		for rowKey := range rowKeys {
			addModelHealthObservedCell(states[rowKey], cell)
		}
	}

	requiredAvailabilities := make([]float64, 0)
	var requiredProbeRuns, requiredProbeAttempts, requiredProbeSuccesses int64
	for _, publication := range filteredPublications {
		if !publication.required {
			continue
		}
		aggregate := service.SummarizeModelHealthProbe(
			historyByTargetModel[strconv.FormatInt(publication.targetID, 10)+"\x00"+publication.internalModel],
			publication.interval, publication.latencySLO, now,
		)
		if aggregate.Status != service.ModelHealthStatusIdle {
			requiredAvailabilities = append(requiredAvailabilities, aggregate.Availability)
		}
		requiredProbeRuns += aggregate.RunCount
		requiredProbeAttempts += aggregate.AttemptCount
		requiredProbeSuccesses += aggregate.SuccessCount
	}

	rowKeys := make([]string, 0, len(states))
	for key := range states {
		rowKeys = append(rowKeys, key)
	}
	sort.Slice(rowKeys, func(i, j int) bool {
		left, right := states[rowKeys[i]].row, states[rowKeys[j]].row
		if left.Group == right.Group {
			return left.Model < right.Model
		}
		return left.Group < right.Group
	})
	rows := make([]modelHealthPublicRow, 0, len(rowKeys))
	series := make([]modelHealthSeriesRow, 0, len(rowKeys))
	summary := modelHealthSummary{}
	summary.ProbeRunCount = requiredProbeRuns
	summary.ProbeAttemptCount = requiredProbeAttempts
	summary.ProbeSuccessCount = requiredProbeSuccesses
	var observedSuccesses int64
	for _, cell := range summaryObservedCells {
		summary.ObservedRequestCount += cell.RequestCount
		observedSuccesses += cell.SuccessCount
	}
	for _, key := range rowKeys {
		state := states[key]
		if observed, ok := perfmetrics.AggregateMatrixCells(state.observedCells); ok {
			state.observed = &observed
		}
		interval, latencySLO := publicationStatusSettings(state.publications)
		aggregate := service.SummarizeModelHealthProbe(state.histories, interval, latencySLO, now)
		state.row.Probe = probeAggregateView(aggregate)
		state.row.Observed = observedCellView(state.observed)
		state.row.SignalConflict = service.ModelHealthStatusesConflict(state.row.Probe.Status, state.row.Observed.Status)
		if len(state.publications) > 0 {
			state.row.Status = state.row.Probe.Status
		} else {
			state.row.Status = state.row.Observed.Status
		}
		switch state.row.Status {
		case service.ModelHealthStatusNormal:
			summary.Healthy++
		case service.ModelHealthStatusFluctuating:
			summary.Fluctuating++
		case service.ModelHealthStatusAbnormal:
			summary.Unstable++
		default:
			summary.Idle++
		}
		rows = append(rows, state.row)
		series = append(series, buildModelHealthSeriesRow(state, bucketSeconds))
	}
	poolRowKeys := make([]string, 0, len(poolStates))
	for key := range poolStates {
		poolRowKeys = append(poolRowKeys, key)
	}
	sort.Slice(poolRowKeys, func(i, j int) bool {
		left, right := poolStates[poolRowKeys[i]].row, poolStates[poolRowKeys[j]].row
		if left.Group != right.Group {
			return left.Group < right.Group
		}
		if left.Model != right.Model {
			return left.Model < right.Model
		}
		return left.Pool < right.Pool
	})
	poolRows := make([]modelHealthPublicRow, 0, len(poolRowKeys))
	poolSeries := make([]modelHealthSeriesRow, 0, len(poolRowKeys))
	for _, key := range poolRowKeys {
		state := poolStates[key]
		if observed, ok := perfmetrics.AggregateMatrixCells(state.observedCells); ok {
			state.observed = &observed
		}
		interval, latencySLO := publicationStatusSettings(state.publications)
		aggregate := service.SummarizeModelHealthProbe(state.histories, interval, latencySLO, now)
		state.row.Probe = probeAggregateView(aggregate)
		state.row.Observed = observedCellView(state.observed)
		state.row.SignalConflict = service.ModelHealthStatusesConflict(state.row.Probe.Status, state.row.Observed.Status)
		state.row.Status = state.row.Probe.Status
		poolRows = append(poolRows, state.row)
		poolSeries = append(poolSeries, buildModelHealthSeriesRow(state, bucketSeconds))
	}
	if len(requiredAvailabilities) > 0 {
		value := roundedMean(requiredAvailabilities)
		summary.ProbeAvailability = &value
	}
	if summary.ObservedRequestCount > 0 {
		value := math.Round(float64(observedSuccesses)/float64(summary.ObservedRequestCount)*10000) / 100
		summary.ObservedSuccessRate = &value
	}
	return modelHealthPublicData{generatedAt: now, window: window, bucketSeconds: bucketSeconds,
		rows: rows, poolRows: poolRows, series: series, poolSeries: poolSeries, summary: summary}, nil
}

func emptyModelHealthPublicData(now time.Time, window string, bucketSeconds int64) modelHealthPublicData {
	return modelHealthPublicData{generatedAt: now, window: window, bucketSeconds: bucketSeconds,
		rows: []modelHealthPublicRow{}, poolRows: []modelHealthPublicRow{},
		series: []modelHealthSeriesRow{}, poolSeries: []modelHealthSeriesRow{}}
}

func modelHealthPublications() ([]modelHealthPublication, map[string]map[string]struct{}, map[string]map[string]struct{}, map[string]modelHealthPoolCatalogItem, error) {
	models := map[string]map[string]struct{}{}
	groups := map[string]map[string]struct{}{}
	pools := map[string]modelHealthPoolCatalogItem{}
	targets, err := model.ListPublicModelHealthTargets()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	poolDetailsEnabled := model_health_setting.GetSetting().PoolDetailsEnabled
	enabledChannelIDs := map[int]struct{}{}
	if poolDetailsEnabled {
		channels, channelErr := model.ListModelHealthChannelOptions()
		if channelErr != nil {
			return nil, nil, nil, nil, channelErr
		}
		for _, channel := range channels {
			if channel.Status == common.ChannelStatusEnabled {
				enabledChannelIDs[channel.Id] = struct{}{}
			}
		}
	}
	publications := make([]modelHealthPublication, 0)
	for _, target := range targets {
		groupAlias := strings.TrimSpace(target.PublicGroupAlias)
		if groupAlias == "" {
			continue
		}
		if groups[groupAlias] == nil {
			groups[groupAlias] = map[string]struct{}{}
		}
		internalGroups, groupErr := target.ObservedGroups()
		if groupErr != nil {
			continue
		}
		channelIDs, channelErr := target.ObservedChannelIDs()
		if channelErr != nil {
			channelIDs = nil
		}
		poolAlias := strings.TrimSpace(target.PublicPoolAlias)
		publishPool := poolDetailsEnabled && target.PublishPoolDetail && poolAlias != "" && len(channelIDs) > 0
		poolKey := ""
		activeChannelIDs := make([]int, 0, len(channelIDs))
		if publishPool {
			poolKey = modelHealthPublicPoolKey(groupAlias, poolAlias)
			pools[poolKey] = modelHealthPoolCatalogItem{Key: poolKey, Alias: poolAlias, Group: groupAlias}
			for _, channelID := range channelIDs {
				if _, ok := enabledChannelIDs[channelID]; ok {
					activeChannelIDs = append(activeChannelIDs, channelID)
				}
			}
		}
		for _, internalGroup := range internalGroups {
			addAliasIndex(groups, groupAlias, internalGroup)
		}
		targetModels, parseErr := target.Models()
		if parseErr != nil {
			continue
		}
		for _, targetModel := range targetModels {
			modelAlias := strings.TrimSpace(targetModel.PublicAlias)
			if modelAlias == "" {
				continue
			}
			addAliasIndex(models, modelAlias, targetModel.Name)
			publications = append(publications, modelHealthPublication{
				targetID: target.ID, internalModel: targetModel.Name, internalGroups: internalGroups,
				modelAlias: modelAlias, groupAlias: groupAlias, required: targetModel.Required,
				poolAlias: poolAlias, poolKey: poolKey, publishPool: publishPool,
				activeChannelIDs: activeChannelIDs,
				interval:         target.IntervalSeconds, latencySLO: target.LatencySLOMs,
			})
		}
	}
	return publications, models, groups, pools, nil
}

func modelHealthPublicPoolKey(groupAlias, poolAlias string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(groupAlias + "\x00" + poolAlias))
}

func addAliasIndex(index map[string]map[string]struct{}, alias, internal string) {
	if index[alias] == nil {
		index[alias] = map[string]struct{}{}
	}
	index[alias][internal] = struct{}{}
}

func parseModelHealthWindow(raw string) (string, int, int64) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "24h":
		return "24h", 24, 300
	case "7d":
		return "7d", 24 * 7, 3600
	case "30d":
		return "30d", 24 * 30, 86400
	default:
		return "12h", 12, 300
	}
}

func queryAliasSet(c *gin.Context, key string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, raw := range c.QueryArray(key) {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value != "" {
				result[value] = struct{}{}
			}
		}
	}
	return result
}

func aliasSelected(selected map[string]struct{}, alias string) bool {
	if len(selected) == 0 {
		return true
	}
	_, ok := selected[alias]
	return ok
}

func ensureModelHealthRowState(states map[string]*modelHealthRowState, groupAlias, modelAlias string) *modelHealthRowState {
	key := publicHealthRowKey(groupAlias, modelAlias)
	if state := states[key]; state != nil {
		return state
	}
	state := &modelHealthRowState{
		row:          modelHealthPublicRow{Group: groupAlias, Model: modelAlias},
		observedKeys: map[string]struct{}{},
	}
	states[key] = state
	return state
}

func addModelHealthObservedCell(state *modelHealthRowState, cell perfmetrics.MatrixCell) {
	if state == nil {
		return
	}
	if state.observedKeys == nil {
		state.observedKeys = map[string]struct{}{}
	}
	key := publicHealthRowKey(cell.Group, cell.Model)
	if cell.ChannelID > 0 {
		key = strconv.Itoa(cell.ChannelID) + "\x00" + key
	}
	if _, exists := state.observedKeys[key]; exists {
		return
	}
	state.observedKeys[key] = struct{}{}
	state.observedCells = append(state.observedCells, cell)
}

func probeAggregateView(aggregate service.ModelHealthProbeAggregate) modelHealthProbeView {
	view := modelHealthProbeView{
		Status: aggregate.Status, SampleCount: aggregate.SampleCount,
		ProbeRunCount: aggregate.RunCount, ProbeAttemptCount: aggregate.AttemptCount,
		ProbeSuccessCount: aggregate.SuccessCount,
	}
	if aggregate.SampleCount > 0 {
		availability, latency := aggregate.Availability, aggregate.LatestLatencyMs
		view.Availability, view.LatestLatencyMs = &availability, &latency
		checkedAt := time.Unix(aggregate.LatestCheckedAt, 0).UTC().Format(time.RFC3339)
		view.LastCheckedAt = &checkedAt
	}
	return view
}

func observedCellView(cell *perfmetrics.MatrixCell) modelHealthObservedView {
	view := modelHealthObservedView{Status: service.ModelHealthStatusIdle}
	if cell == nil || cell.RequestCount == 0 {
		return view
	}
	view.Status = service.ModelHealthPassiveStatus(cell.RequestCount, cell.SuccessRate)
	view.RequestCount = cell.RequestCount
	successRate, latency := cell.SuccessRate, cell.AvgLatencyMs
	view.SuccessRate, view.AvgLatencyMs = &successRate, &latency
	if cell.TtftCount > 0 {
		ttft := cell.AvgTtftMs
		view.AvgTtftMs = &ttft
	}
	if cell.GenerationMs > 0 {
		tps := cell.AvgTps
		view.AvgTps = &tps
	}
	return view
}

func publicationStatusSettings(publications []modelHealthPublication) (int, int64) {
	interval := model_health_setting.GetSetting().DefaultIntervalSeconds
	latencySLO := int64(0)
	for _, publication := range publications {
		if publication.interval > 0 && publication.interval < interval {
			interval = publication.interval
		}
		if publication.latencySLO > 0 && (latencySLO == 0 || publication.latencySLO < latencySLO) {
			latencySLO = publication.latencySLO
		}
	}
	return interval, latencySLO
}

func buildModelHealthSeriesRow(state *modelHealthRowState, bucketSeconds int64) modelHealthSeriesRow {
	points := map[int64]*modelHealthPoint{}
	if state.observed != nil {
		for _, observed := range state.observed.Series {
			point := ensureModelHealthPoint(points, observed.Ts)
			rate := observed.SuccessRate
			point.ObservedSuccessRate = &rate
			point.ObservedRequestCount = observed.RequestCount
		}
	}
	type probeBucket struct {
		runCount, attempts, successes int64
		availabilitySum               float64
		statuses                      []string
	}
	probeBuckets := map[int64]probeBucket{}
	for _, batch := range service.BuildModelHealthProbeBatches(state.histories) {
		ts := batch.RunStartedAt - batch.RunStartedAt%bucketSeconds
		bucket := probeBuckets[ts]
		bucket.runCount++
		bucket.attempts += batch.AttemptCount
		bucket.successes += batch.SuccessCount
		bucket.availabilitySum += batch.Availability
		bucket.statuses = append(bucket.statuses, batch.Status)
		probeBuckets[ts] = bucket
	}
	for ts, bucket := range probeBuckets {
		point := ensureModelHealthPoint(points, ts)
		availability := math.Round(bucket.availabilitySum/float64(bucket.runCount)*100) / 100
		point.ProbeAvailability = &availability
		point.ProbeRunCount = bucket.runCount
		point.ProbeAttemptCount = bucket.attempts
		point.ProbeSuccessCount = bucket.successes
		point.ProbeStatus = service.WorstModelHealthStatus(bucket.statuses)
	}
	timestamps := make([]int64, 0, len(points))
	for ts := range points {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	result := modelHealthSeriesRow{Group: state.row.Group, Model: state.row.Model,
		PoolKey: state.row.PoolKey, Pool: state.row.Pool, PoolAlias: state.row.PoolAlias,
		Points: make([]modelHealthPoint, 0, len(timestamps))}
	for _, ts := range timestamps {
		result.Points = append(result.Points, *points[ts])
	}
	return result
}

func ensureModelHealthPoint(points map[int64]*modelHealthPoint, ts int64) *modelHealthPoint {
	if point := points[ts]; point != nil {
		return point
	}
	point := &modelHealthPoint{Ts: ts, ProbeStatus: service.ModelHealthStatusIdle}
	points[ts] = point
	return point
}

func publicHealthRowKey(group, modelName string) string { return group + "\x00" + modelName }

func publicPoolHealthRowKey(group, modelName, pool string) string {
	return group + "\x00" + modelName + "\x00" + pool
}

func modelHealthChannelCellKey(channelID int, group, modelName string) string {
	return strconv.Itoa(channelID) + "\x00" + group + "\x00" + modelName
}

func roundedMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return math.Round(sum/float64(len(values))*100) / 100
}

func sortedSetKeys[T ~string](set map[T]struct{}) []T {
	keys := make([]T, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedInt64SetKeys(set map[int64]struct{}) []int64 {
	keys := make([]int64, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedAliasIndexKeys(index map[string]map[string]struct{}) []string {
	keys := make([]string, 0, len(index))
	for key := range index {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type modelHealthTargetRequest struct {
	ID                   int64                          `json:"id,omitempty"`
	Name                 string                         `json:"name"`
	Mode                 string                         `json:"mode"`
	TokenID              *int                           `json:"token_id"`
	Protocol             string                         `json:"protocol"`
	Endpoint             string                         `json:"endpoint"`
	APIKey               string                         `json:"api_key"`
	Models               []model.ModelHealthTargetModel `json:"models"`
	ObservedGroups       *[]string                      `json:"observed_groups"`
	ObservedGroup        string                         `json:"observed_group"`
	ObservedChannelIDs   *[]int                         `json:"observed_channel_ids"`
	PublicGroupAlias     string                         `json:"public_group_alias"`
	PublicPoolAlias      *string                        `json:"public_pool_alias"`
	PublishPoolDetail    *bool                          `json:"publish_pool_detail"`
	Public               bool                           `json:"public"`
	Enabled              bool                           `json:"enabled"`
	IntervalSeconds      int                            `json:"interval_seconds"`
	TimeoutSeconds       int                            `json:"timeout_seconds"`
	SamplingMode         *string                        `json:"sampling_mode"`
	SamplesPerRun        *int                           `json:"samples_per_run"`
	MinimumSuccesses     *int                           `json:"minimum_successes"`
	SampleSpacingSeconds *int                           `json:"sample_spacing_seconds"`
	LatencySLOMs         *int64                         `json:"latency_slo_ms"`
}

type modelHealthTargetResponse struct {
	ID                         int64                          `json:"id"`
	Name                       string                         `json:"name"`
	Mode                       string                         `json:"mode"`
	TokenID                    *int                           `json:"token_id"`
	Protocol                   string                         `json:"protocol"`
	EndpointMasked             string                         `json:"endpoint_masked,omitempty"`
	HasKey                     bool                           `json:"has_key"`
	CredentialFingerprint      string                         `json:"credential_fingerprint,omitempty"`
	CredentialsNeedReplacement bool                           `json:"credentials_need_replacement,omitempty"`
	Models                     []model.ModelHealthTargetModel `json:"models"`
	ObservedGroups             []string                       `json:"observed_groups"`
	ObservedGroup              string                         `json:"observed_group,omitempty"`
	ObservedChannelIDs         []int                          `json:"observed_channel_ids"`
	PublicGroupAlias           string                         `json:"public_group_alias"`
	PublicPoolAlias            string                         `json:"public_pool_alias"`
	PublishPoolDetail          bool                           `json:"publish_pool_detail"`
	Public                     bool                           `json:"public"`
	Enabled                    bool                           `json:"enabled"`
	IntervalSeconds            int                            `json:"interval_seconds"`
	TimeoutSeconds             int                            `json:"timeout_seconds"`
	SamplingMode               string                         `json:"sampling_mode"`
	SamplesPerRun              int                            `json:"samples_per_run"`
	MinimumSuccesses           int                            `json:"minimum_successes"`
	SampleSpacingSeconds       int                            `json:"sample_spacing_seconds"`
	LatencySLOMs               *int64                         `json:"latency_slo_ms"`
	LastCheckedAt              *string                        `json:"last_checked_at"`
	CreatedAt                  int64                          `json:"created_at"`
	UpdatedAt                  int64                          `json:"updated_at"`
}

type modelHealthProbeTokenOption struct {
	ID                 int      `json:"id"`
	Name               string   `json:"name"`
	Group              string   `json:"group"`
	Status             int      `json:"status"`
	RemainQuota        int      `json:"remain_quota"`
	UnlimitedQuota     bool     `json:"unlimited_quota"`
	ExpiredTime        int64    `json:"expired_time"`
	ModelLimitsEnabled bool     `json:"model_limits_enabled"`
	ModelLimits        []string `json:"model_limits"`
	Available          bool     `json:"available"`
	Marked             bool     `json:"marked"`
	ReferenceCount     int64    `json:"reference_count"`
}

type modelHealthGroupOption struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Models      []string `json:"models"`
}

type modelHealthChannelOption struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Status int      `json:"status"`
	Groups []string `json:"groups"`
	Models []string `json:"models"`
}

type modelHealthProbeTokenUpdateRequest struct {
	Marked bool `json:"marked"`
}

func AdminGetModelHealthOptions(c *gin.Context) {
	userID := c.GetInt("id")
	tokens, err := model.ListModelHealthProbeTokenCandidates(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tokenIDs := make([]int, 0, len(tokens))
	for _, token := range tokens {
		tokenIDs = append(tokenIDs, token.Id)
	}
	marks, err := model.ListModelHealthProbeTokenMarks(tokenIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	marked := make(map[int]struct{}, len(marks))
	for _, mark := range marks {
		if mark.MarkedBy == userID {
			marked[mark.TokenID] = struct{}{}
		}
	}
	tokenOptions := make([]modelHealthProbeTokenOption, 0, len(marked))
	for _, token := range tokens {
		if _, ok := marked[token.Id]; !ok || !modelHealthProbeTokenAvailable(token, time.Now().Unix()) {
			continue
		}
		tokenOptions = append(tokenOptions, modelHealthProbeTokenToOption(token))
	}

	abilities, err := model.ListEnabledModelHealthAbilities()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	modelsByGroup := map[string]map[string]struct{}{}
	modelSet := map[string]struct{}{}
	for _, ability := range abilities {
		group, modelName := strings.TrimSpace(ability.Group), strings.TrimSpace(ability.Model)
		if group == "" || modelName == "" {
			continue
		}
		if modelsByGroup[group] == nil {
			modelsByGroup[group] = map[string]struct{}{}
		}
		modelsByGroup[group][modelName] = struct{}{}
		modelSet[modelName] = struct{}{}
	}
	groupSet := make(map[string]struct{}, len(modelsByGroup))
	for group := range modelsByGroup {
		groupSet[group] = struct{}{}
	}
	groupNames := sortedSetKeys(groupSet)
	groupOptions := make([]modelHealthGroupOption, 0, len(groupNames))
	for _, group := range groupNames {
		groupOptions = append(groupOptions, modelHealthGroupOption{
			Name: group, DisplayName: group,
			Models: sortedSetKeys(modelsByGroup[group]),
		})
	}
	channels, err := model.ListModelHealthChannelOptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channelOptions := make([]modelHealthChannelOption, 0, len(channels))
	for i := range channels {
		groupSet := map[string]struct{}{}
		for _, group := range channels[i].GetGroups() {
			if group = strings.TrimSpace(group); group != "" {
				groupSet[group] = struct{}{}
			}
		}
		channelModelSet := map[string]struct{}{}
		for _, modelName := range channels[i].GetModels() {
			if modelName = strings.TrimSpace(modelName); modelName != "" {
				channelModelSet[modelName] = struct{}{}
			}
		}
		channelOptions = append(channelOptions, modelHealthChannelOption{
			ID: channels[i].Id, Name: channels[i].Name, Status: channels[i].Status,
			Groups: sortedSetKeys(groupSet), Models: sortedSetKeys(channelModelSet),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"tokens": tokenOptions, "groups": groupOptions, "models": sortedSetKeys(modelSet), "channels": channelOptions,
	}})
}

func AdminListModelHealthProbeTokens(c *gin.Context) {
	userID := c.GetInt("id")
	tokens, err := model.ListModelHealthProbeTokenCandidates(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tokenIDs := make([]int, 0, len(tokens))
	for _, token := range tokens {
		tokenIDs = append(tokenIDs, token.Id)
	}
	marks, err := model.ListModelHealthProbeTokenMarks(tokenIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	references, err := model.ModelHealthTokenReferenceCounts(tokenIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	marked := make(map[int]struct{}, len(marks))
	for _, mark := range marks {
		if mark.MarkedBy == userID {
			marked[mark.TokenID] = struct{}{}
		}
	}
	options := make([]modelHealthProbeTokenOption, 0, len(tokens))
	for _, token := range tokens {
		option := modelHealthProbeTokenToOption(token)
		_, option.Marked = marked[token.Id]
		option.ReferenceCount = references[token.Id]
		options = append(options, option)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": options})
}

func AdminUpdateModelHealthProbeToken(c *gin.Context) {
	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenID <= 0 {
		common.ApiErrorMsg(c, "invalid probe token id")
		return
	}
	var request modelHealthProbeTokenUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.SetModelHealthProbeTokenMarked(tokenID, c.GetInt("id"), request.Marked); err != nil {
		if errors.Is(err, model.ErrModelHealthProbeTokenInUse) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"id": tokenID, "marked": request.Marked,
	}})
}

func modelHealthProbeTokenToOption(token model.Token) modelHealthProbeTokenOption {
	return modelHealthProbeTokenOption{
		ID: token.Id, Name: token.Name, Group: token.Group, Status: token.Status,
		RemainQuota: token.RemainQuota, UnlimitedQuota: token.UnlimitedQuota,
		ExpiredTime: token.ExpiredTime, ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits: token.GetModelLimits(), Available: modelHealthProbeTokenAvailable(token, time.Now().Unix()),
	}
}

func modelHealthProbeTokenAvailable(token model.Token, now int64) bool {
	if token.Status != common.TokenStatusEnabled {
		return false
	}
	if token.ExpiredTime != -1 && token.ExpiredTime <= now {
		return false
	}
	return token.UnlimitedQuota || token.RemainQuota > 0
}

func AdminListModelHealthTargets(c *gin.Context) {
	targets, err := model.ListModelHealthTargets()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]modelHealthTargetResponse, 0, len(targets))
	for i := range targets {
		response, responseErr := modelHealthTargetToResponse(&targets[i])
		if responseErr != nil {
			common.ApiError(c, responseErr)
			return
		}
		responses = append(responses, response)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": responses})
}

func AdminCreateModelHealthTarget(c *gin.Context) {
	var request modelHealthTargetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	target, err := buildModelHealthTarget(request, nil, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateModelHealthTarget(target); err != nil {
		if errors.Is(err, model.ErrModelHealthPoolAliasConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiError(c, err)
		return
	}
	response, err := modelHealthTargetToResponse(target)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": response})
}

func AdminUpdateModelHealthTarget(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid model health target id")
		return
	}
	existing, err := model.GetModelHealthTarget(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var request modelHealthTargetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	target, err := buildModelHealthTarget(request, existing, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.SaveModelHealthTarget(target); err != nil {
		if errors.Is(err, model.ErrModelHealthPoolAliasConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiError(c, err)
		return
	}
	response, err := modelHealthTargetToResponse(target)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": response})
}

func AdminDeleteModelHealthTarget(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid model health target id")
		return
	}
	if err := model.DeleteModelHealthTarget(id); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": nil})
}

func AdminValidateModelHealthTarget(c *gin.Context) {
	var request modelHealthTargetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	var existing *model.ModelHealthTarget
	if request.ID > 0 {
		existing, _ = model.GetModelHealthTarget(request.ID)
	}
	target, err := buildModelHealthTarget(request, existing, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ValidateModelHealthPoolAlias(target); err != nil {
		if errors.Is(err, model.ErrModelHealthPoolAliasConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiError(c, err)
		return
	}
	probeResult, err := validateModelHealthTarget(c.Request.Context(), target)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": probeResult})
}

func AdminRunModelHealthTarget(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid model health target id")
		return
	}
	if _, err := model.GetModelHealthTarget(id); err != nil {
		common.ApiError(c, err)
		return
	}
	enqueueModelHealthTask(c, modelHealthTaskPayload{TargetIDs: []int64{id}, Force: true})
}

func AdminRunAllModelHealthTargets(c *gin.Context) {
	enqueueModelHealthTask(c, modelHealthTaskPayload{Force: true})
}

func enqueueModelHealthTask(c *gin.Context, payload modelHealthTaskPayload) {
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeModelHealth, payload)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "a model health task is already active",
			"data": gin.H{"task_id": task.TaskID, "status": task.Status}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"task_id": task.TaskID, "status": task.Status}})
}

func buildModelHealthTarget(request modelHealthTargetRequest, existing *model.ModelHealthTarget, userID int) (*model.ModelHealthTarget, error) {
	setting := model_health_setting.GetSetting()
	target := &model.ModelHealthTarget{}
	if existing != nil {
		*target = *existing
	} else {
		target.SamplingMode = setting.DefaultSamplingMode
		target.SamplesPerRun = setting.DefaultSamplesPerRun
		target.MinimumSuccesses = setting.DefaultMinimumSuccesses
		target.SampleSpacingSeconds = setting.DefaultSampleSpacingSeconds
	}
	target.Name, target.Mode, target.TokenID, target.Protocol = request.Name, request.Mode, request.TokenID, request.Protocol
	target.PublicGroupAlias = request.PublicGroupAlias
	if request.PublicPoolAlias != nil {
		target.PublicPoolAlias = *request.PublicPoolAlias
	}
	if request.PublishPoolDetail != nil {
		target.PublishPoolDetail = *request.PublishPoolDetail
	}
	observedGroups := []string{}
	if request.ObservedGroups != nil {
		observedGroups = *request.ObservedGroups
	} else if group := strings.TrimSpace(request.ObservedGroup); group != "" {
		observedGroups = []string{group}
	}
	if err := target.SetObservedGroups(observedGroups); err != nil {
		return nil, err
	}
	if request.ObservedChannelIDs != nil {
		if err := target.SetObservedChannelIDs(*request.ObservedChannelIDs); err != nil {
			return nil, err
		}
	}
	target.Public, target.Enabled = request.Public, request.Enabled
	target.IntervalSeconds, target.TimeoutSeconds = request.IntervalSeconds, request.TimeoutSeconds
	if target.IntervalSeconds == 0 {
		target.IntervalSeconds = setting.DefaultIntervalSeconds
	}
	if target.TimeoutSeconds == 0 {
		target.TimeoutSeconds = setting.DefaultTimeoutSeconds
	}
	if request.SamplingMode != nil {
		mode := strings.TrimSpace(*request.SamplingMode)
		if mode != model.ModelHealthSamplingFixed && mode != model.ModelHealthSamplingConfirmOnFailure {
			return nil, errors.New("sampling_mode must be fixed or confirm_on_failure")
		}
		target.SamplingMode = mode
	}
	if request.SamplesPerRun != nil {
		if *request.SamplesPerRun < 1 || *request.SamplesPerRun > 5 {
			return nil, errors.New("samples_per_run must be between 1 and 5")
		}
		target.SamplesPerRun = *request.SamplesPerRun
	}
	if request.MinimumSuccesses != nil {
		if *request.MinimumSuccesses < 1 {
			return nil, errors.New("minimum_successes must be at least 1")
		}
		target.MinimumSuccesses = *request.MinimumSuccesses
	}
	if request.SampleSpacingSeconds != nil {
		if *request.SampleSpacingSeconds < 1 || *request.SampleSpacingSeconds > 30 {
			return nil, errors.New("sample_spacing_seconds must be between 1 and 30")
		}
		target.SampleSpacingSeconds = *request.SampleSpacingSeconds
	}
	if request.LatencySLOMs != nil {
		target.LatencySLOMs = *request.LatencySLOMs
	}
	if err := target.SetModels(request.Models); err != nil {
		return nil, err
	}
	if target.Mode == model.ModelHealthModeUpstream {
		if !service.ModelHealthStableSecretConfigured() {
			return nil, errors.New("stable CRYPTO_SECRET is required for upstream health targets")
		}
		if err := service.UpdateModelHealthTargetCredentials(target, request.Endpoint, request.APIKey); err != nil {
			return nil, err
		}
	} else {
		target.EndpointEncrypted, target.APIKeyEncrypted, target.CredentialFingerprint = "", "", ""
		if request.TokenID == nil || *request.TokenID <= 0 {
			return nil, errors.New("local target requires token_id")
		}
		token, err := model.GetTokenById(*request.TokenID)
		if err != nil || !modelHealthProbeTokenAvailable(*token, time.Now().Unix()) {
			return nil, errors.New("local model health token is unavailable")
		}
		legacyUnchangedToken := existing != nil && existing.TokenID != nil && *existing.TokenID == *request.TokenID
		marked, err := model.IsModelHealthProbeTokenMarkedForOwner(*request.TokenID, userID)
		if err != nil {
			return nil, err
		}
		if !marked && !legacyUnchangedToken {
			return nil, errors.New("local model health token must be marked as a dedicated probe token")
		}
	}
	if err := target.Normalize(); err != nil {
		return nil, err
	}
	if target.PublishPoolDetail {
		channelIDs, err := target.ObservedChannelIDs()
		if err != nil {
			return nil, err
		}
		enabledCount, err := model.CountEnabledModelHealthChannels(channelIDs)
		if err != nil {
			return nil, err
		}
		if enabledCount == 0 {
			unchangedStaleBinding := false
			if existing != nil && existing.PublishPoolDetail {
				existingIDs, existingErr := existing.ObservedChannelIDs()
				if existingErr != nil {
					return nil, existingErr
				}
				if len(existingIDs) == len(channelIDs) {
					unchangedStaleBinding = true
					selected := make(map[int]struct{}, len(channelIDs))
					for _, channelID := range channelIDs {
						selected[channelID] = struct{}{}
					}
					for _, channelID := range existingIDs {
						if _, ok := selected[channelID]; !ok {
							unchangedStaleBinding = false
							break
						}
					}
				}
			}
			if !unchangedStaleBinding {
				return nil, errors.New("pool details require at least one enabled channel")
			}
		}
	}
	return target, nil
}

func modelHealthTargetToResponse(target *model.ModelHealthTarget) (modelHealthTargetResponse, error) {
	models, err := target.Models()
	if err != nil {
		return modelHealthTargetResponse{}, err
	}
	observedGroups, err := target.ObservedGroups()
	if err != nil {
		return modelHealthTargetResponse{}, err
	}
	observedChannelIDs, err := target.ObservedChannelIDs()
	if err != nil {
		return modelHealthTargetResponse{}, err
	}
	maskedEndpoint, hasKey, fingerprint, credentialErr := service.ModelHealthTargetCredentialMetadata(target)
	needsReplacement := errors.Is(credentialErr, service.ErrModelHealthCredentialsNeedReplacement)
	if needsReplacement && target.Enabled {
		if err := model.DisableModelHealthTarget(target.ID); err != nil {
			return modelHealthTargetResponse{}, err
		}
		target.Enabled = false
	}
	response := modelHealthTargetResponse{ID: target.ID, Name: target.Name, Mode: target.Mode,
		TokenID: target.TokenID, Protocol: target.Protocol, EndpointMasked: maskedEndpoint,
		HasKey: hasKey, CredentialFingerprint: fingerprint, CredentialsNeedReplacement: needsReplacement,
		Models: models, ObservedGroups: observedGroups, ObservedGroup: target.ObservedGroup,
		ObservedChannelIDs: observedChannelIDs, PublicGroupAlias: target.PublicGroupAlias,
		PublicPoolAlias: target.PublicPoolAlias, PublishPoolDetail: target.PublishPoolDetail,
		Public: target.Public, Enabled: target.Enabled, IntervalSeconds: target.IntervalSeconds,
		TimeoutSeconds: target.TimeoutSeconds, SamplingMode: target.SamplingMode,
		SamplesPerRun: target.SamplesPerRun, MinimumSuccesses: target.MinimumSuccesses,
		SampleSpacingSeconds: target.SampleSpacingSeconds,
		CreatedAt:            target.CreatedAt, UpdatedAt: target.UpdatedAt}
	if target.LatencySLOMs > 0 {
		value := target.LatencySLOMs
		response.LatencySLOMs = &value
	}
	if target.LastCheckedAt > 0 {
		value := time.Unix(target.LastCheckedAt, 0).UTC().Format(time.RFC3339)
		response.LastCheckedAt = &value
	}
	return response, nil
}

func validateModelHealthTarget(ctx context.Context, target *model.ModelHealthTarget) (service.ModelHealthProbeBatchResult, error) {
	models, err := target.Models()
	if err != nil || len(models) == 0 {
		return service.ModelHealthProbeBatchResult{}, errors.New("at least one model is required")
	}
	baseURL, apiKey := "", ""
	if target.Mode == model.ModelHealthModeLocal {
		token, tokenErr := model.GetTokenById(*target.TokenID)
		if tokenErr != nil {
			return service.ModelHealthProbeBatchResult{}, errors.New("local model health token is unavailable")
		}
		baseURL, apiKey = system_setting.ServerAddress, token.Key
	} else {
		baseURL, err = service.DecryptModelHealthSecret(target.EndpointEncrypted)
		if err == nil {
			apiKey, err = service.DecryptModelHealthSecret(target.APIKeyEncrypted)
		}
		if err != nil {
			if target.ID > 0 {
				if disableErr := model.DisableModelHealthTarget(target.ID); disableErr != nil {
					return service.ModelHealthProbeBatchResult{}, disableErr
				}
			}
			return service.ModelHealthProbeBatchResult{}, service.ErrModelHealthCredentialsNeedReplacement
		}
	}
	probeModel := models[0].Name
	for _, targetModel := range models {
		if targetModel.Required {
			probeModel = targetModel.Name
			break
		}
	}
	return service.ExecuteModelHealthProbeBatch(ctx, service.ModelHealthProbeConfig{
		Protocol: target.Protocol, BaseURL: baseURL, APIKey: apiKey, Model: probeModel,
		Timeout: time.Duration(target.TimeoutSeconds) * time.Second, Local: target.Mode == model.ModelHealthModeLocal,
	}, service.ModelHealthSamplingConfig{
		Mode: target.SamplingMode, PlannedAttempts: target.SamplesPerRun,
		MinimumSuccesses: target.MinimumSuccesses,
		Spacing:          time.Duration(target.SampleSpacingSeconds) * time.Second,
	})
}
