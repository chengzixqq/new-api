/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  Banner,
  Button,
  Card,
  Empty,
  Select,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { Activity, AlertTriangle, Gauge, RefreshCw, Timer } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { API } from '../../helpers';

const { Text, Title } = Typography;

const STATUS_ORDER = ['unstable', 'fluctuating', 'healthy', 'idle'];
const STATUS_META = {
  healthy: { color: 'green' },
  fluctuating: { color: 'orange' },
  unstable: { color: 'red' },
  idle: { color: 'grey' },
};
const SIGNAL_META = {
  probe: { color: '#10b981', dasharray: undefined },
  observed: { color: '#3b82f6', dasharray: '5 4' },
};
const TREND_WIDTH = 240;
const TREND_LEFT = 4;
const TREND_RIGHT = 236;
const TREND_TOP = 5;
const TREND_BOTTOM = 59;

function buildMonotonePath(coordinates) {
  if (coordinates.length === 0) return '';
  if (coordinates.length === 1) {
    return `M ${coordinates[0].x.toFixed(2)} ${coordinates[0].y.toFixed(2)}`;
  }

  const slopes = coordinates.slice(0, -1).map((point, index) => {
    const next = coordinates[index + 1];
    return (next.y - point.y) / (next.x - point.x);
  });
  const tangents = coordinates.map((_, index) => {
    if (index === 0) return slopes[0];
    if (index === coordinates.length - 1) return slopes[slopes.length - 1];
    if (slopes[index - 1] * slopes[index] <= 0) return 0;
    return (slopes[index - 1] + slopes[index]) / 2;
  });

  slopes.forEach((slope, index) => {
    if (slope === 0) {
      tangents[index] = 0;
      tangents[index + 1] = 0;
      return;
    }
    const left = tangents[index] / slope;
    const right = tangents[index + 1] / slope;
    const magnitude = Math.hypot(left, right);
    if (magnitude <= 3) return;
    const scale = 3 / magnitude;
    tangents[index] = scale * left * slope;
    tangents[index + 1] = scale * right * slope;
  });

  let path = `M ${coordinates[0].x.toFixed(2)} ${coordinates[0].y.toFixed(2)}`;
  for (let index = 0; index < coordinates.length - 1; index += 1) {
    const point = coordinates[index];
    const next = coordinates[index + 1];
    const width = next.x - point.x;
    path += ` C ${(point.x + width / 3).toFixed(2)} ${(point.y + (tangents[index] * width) / 3).toFixed(2)}, ${(next.x - width / 3).toFixed(2)} ${(next.y - (tangents[index + 1] * width) / 3).toFixed(2)}, ${next.x.toFixed(2)} ${next.y.toFixed(2)}`;
  }
  return path;
}

function statusLabel(t, status) {
  if (status === 'healthy') return t('Healthy');
  if (status === 'fluctuating') return t('Fluctuating');
  if (status === 'unstable') return t('Unstable');
  return t('Idle');
}

function normalizeStatus(value) {
  const status = String(value || '').toLowerCase();
  if (['healthy', 'normal', 'up', 'ok', 'success'].includes(status)) {
    return 'healthy';
  }
  if (['fluctuating', 'degraded', 'warning', 'warn'].includes(status)) {
    return 'fluctuating';
  }
  if (
    ['unstable', 'unhealthy', 'down', 'error', 'failed', 'failure'].includes(
      status,
    )
  ) {
    return 'unstable';
  }
  return 'idle';
}

function unwrapResponse(response) {
  return response?.data?.data ?? response?.data ?? {};
}

function numberValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined || value === '') continue;
    const number = Number(value);
    if (Number.isFinite(number)) return number;
  }
  return 0;
}

function nullableNumber(...values) {
  for (const value of values) {
    if (value === null || value === undefined || value === '') continue;
    const number = Number(value);
    if (Number.isFinite(number)) return number;
  }
  return null;
}

function aliasValue(value, keys) {
  if (typeof value === 'string') return value;
  for (const key of keys) {
    if (typeof value?.[key] === 'string' && value[key].trim()) {
      return value[key].trim();
    }
  }
  return '';
}

function normalizeCatalog(payload) {
  const groups = Array.isArray(payload?.groups) ? payload.groups : [];
  const models = Array.isArray(payload?.models) ? payload.models : [];
  const pools = Array.isArray(payload?.pools) ? payload.pools : [];
  return {
    groups: groups
      .map((item) => aliasValue(item, ['alias', 'group_alias', 'public_alias']))
      .filter(Boolean),
    models: models
      .map((item) => aliasValue(item, ['alias', 'model_alias', 'public_alias']))
      .filter(Boolean),
    pools: pools
      .map((item) => {
        const key = aliasValue(item, ['key', 'pool_key']);
        const alias = aliasValue(item, [
          'alias',
          'pool_alias',
          'public_pool_alias',
        ]);
        const group = aliasValue(item, ['group', 'group_alias']);
        return key && alias && group ? { key, alias, group } : null;
      })
      .filter(Boolean),
  };
}

function normalizeRows(payload, collection = 'rows', isPool = false) {
  let rows = Array.isArray(payload?.[collection]) ? payload[collection] : [];
  if (collection === 'rows' && rows.length === 0) {
    rows = Array.isArray(payload?.items)
      ? payload.items
      : Array.isArray(payload?.models)
        ? payload.models
        : [];
  }

  return rows
    .map((row) => {
      const probe = row.probe ?? row.active ?? {};
      const observed = row.observed ?? row.passive ?? {};
      const groupAlias = aliasValue(row, ['group_alias', 'group']);
      const modelAlias = aliasValue(row, [
        'model_alias',
        'public_alias',
        'model',
      ]);
      if (!groupAlias || !modelAlias) return null;
      const poolAlias = isPool
        ? aliasValue(row, ['pool_alias', 'pool', 'public_pool_alias'])
        : '';
      const poolKey = isPool
        ? aliasValue(row, ['pool_key', 'key']) ||
          `${groupAlias}\u0000${modelAlias}\u0000${poolAlias}`
        : '';
      if (isPool && (!poolAlias || !poolKey)) return null;
      return {
        key: isPool ? poolKey : `${groupAlias}\u0000${modelAlias}`,
        groupAlias,
        modelAlias,
        poolAlias,
        poolKey,
        isPool,
        status: normalizeStatus(row.status ?? probe.status),
        signalConflict: Boolean(row.signal_conflict),
        probe: {
          status: normalizeStatus(probe.status ?? row.status),
          availability: nullableNumber(
            probe.availability,
            probe.availability_rate,
            probe.success_rate,
          ),
          samples: numberValue(probe.samples, probe.sample_count),
          runCount: numberValue(
            probe.probe_run_count,
            probe.run_count,
            row.probe_run_count,
          ),
          attemptCount: numberValue(
            probe.probe_attempt_count,
            probe.attempt_count,
            row.probe_attempt_count,
          ),
          successCount: numberValue(
            probe.probe_success_count,
            probe.success_count,
            row.probe_success_count,
          ),
          latency: nullableNumber(
            probe.latency_ms,
            probe.latest_latency_ms,
            row.latest_latency_ms,
          ),
          lastCheckedAt:
            probe.last_checked_at ??
            row.last_checked_at ??
            row.updated_at ??
            '',
        },
        observed: {
          status: normalizeStatus(observed.status),
          successRate: nullableNumber(
            observed.success_rate,
            observed.availability,
          ),
          requests: numberValue(
            observed.request_count,
            observed.requests,
            observed.samples,
          ),
          latency: nullableNumber(observed.avg_latency_ms, observed.latency_ms),
          ttft: nullableNumber(observed.avg_ttft_ms, observed.ttft_ms),
          tps: nullableNumber(observed.avg_tps, observed.tps),
        },
        trend: [],
      };
    })
    .filter(Boolean);
}

function normalizeSeries(payload, collection = 'rows', isPool = false) {
  let rows = Array.isArray(payload?.[collection]) ? payload[collection] : [];
  if (collection === 'rows' && rows.length === 0) {
    rows = Array.isArray(payload?.series)
      ? payload.series
      : Array.isArray(payload?.items)
        ? payload.items
        : [];
  }
  const bucketSeconds = numberValue(
    payload?.bucket_seconds,
    payload?.bucketSeconds,
  );
  const seriesByKey = new Map();
  for (const row of rows) {
    const groupAlias = aliasValue(row, ['group_alias', 'group']);
    const modelAlias = aliasValue(row, [
      'model_alias',
      'public_alias',
      'model',
    ]);
    if (!groupAlias || !modelAlias) continue;
    const poolAlias = isPool
      ? aliasValue(row, ['pool_alias', 'pool', 'public_pool_alias'])
      : '';
    const poolKey = isPool
      ? aliasValue(row, ['pool_key', 'key']) ||
        `${groupAlias}\u0000${modelAlias}\u0000${poolAlias}`
      : '';
    if (isPool && (!poolAlias || !poolKey)) continue;
    const rawPoints = Array.isArray(row.probe)
      ? row.probe
      : Array.isArray(row.active)
        ? row.active
        : Array.isArray(row.points)
          ? row.points
          : [];
    const pointsBySignal = {
      probe: new Map(),
      observed: new Map(),
    };
    rawPoints.forEach((point, index) => {
      const ts = nullableNumber(point.ts, point.timestamp, point.checked_at);
      const timestamp = ts ?? index;
      const observed = nullableNumber(point.observed_success_rate);
      const probeSuccessCount = nullableNumber(
        point.probe_success_count,
        point.success_count,
      );
      const probeAttemptCount = nullableNumber(
        point.probe_attempt_count,
        point.attempt_count,
      );
      const probeRunCount = nullableNumber(
        point.probe_run_count,
        point.run_count,
      );
      const probe = nullableNumber(
        point.probe_availability,
        point.availability,
        observed === null ? point.success_rate : Number.NaN,
        point.ok === true ? 100 : Number.NaN,
      );
      if (probe !== null || !pointsBySignal.probe.has(timestamp)) {
        pointsBySignal.probe.set(timestamp, {
          ts: timestamp,
          signal: 'probe',
          value: probe,
          runCount: probeRunCount,
          successCount: probeSuccessCount,
          attemptCount: probeAttemptCount,
        });
      }
      if (observed !== null || !pointsBySignal.observed.has(timestamp)) {
        pointsBySignal.observed.set(timestamp, {
          ts: timestamp,
          signal: 'observed',
          value: observed,
        });
      }
    });
    const probePoints = Array.from(pointsBySignal.probe.values()).sort(
      (left, right) => left.ts - right.ts,
    );
    const observedPoints = Array.from(pointsBySignal.observed.values()).sort(
      (left, right) => left.ts - right.ts,
    );
    const points = [...probePoints, ...observedPoints];
    seriesByKey.set(
      isPool ? poolKey : `${groupAlias}\u0000${modelAlias}`,
      points,
    );
  }
  return { bucketSeconds, seriesByKey };
}

function buildQuery(windowValue, groups, models, pools) {
  const params = new URLSearchParams({ window: windowValue });
  groups.forEach((group) => params.append('group', group));
  models.forEach((model) => params.append('model', model));
  pools.forEach((pool) => params.append('pool', pool));
  return params.toString();
}

function formatPercent(value) {
  return Number.isFinite(value) ? `${value.toFixed(2)}%` : '--';
}

function formatLatency(value) {
  if (!Number.isFinite(value) || value <= 0) return '--';
  return value >= 1000
    ? `${(value / 1000).toFixed(2)} s`
    : `${Math.round(value)} ms`;
}

function formatTps(value) {
  return Number.isFinite(value) && value > 0
    ? `${value.toFixed(1)} tok/s`
    : '--';
}

function formatCount(value, locale) {
  return new Intl.NumberFormat(locale).format(value);
}

function batchPointColor(point) {
  if (
    point.signal !== 'probe' ||
    !Number.isFinite(point.successCount) ||
    !Number.isFinite(point.attemptCount) ||
    point.attemptCount <= 0
  ) {
    return SIGNAL_META[point.signal].color;
  }
  if (point.successCount <= 0) return '#ef4444';
  if (point.successCount < point.attemptCount) return '#f59e0b';
  return SIGNAL_META.probe.color;
}

function formatTrendTimestamp(timestamp, locale) {
  if (!Number.isFinite(timestamp) || timestamp < 100000000) return '';
  const milliseconds = timestamp > 100000000000 ? timestamp : timestamp * 1000;
  const date = new Date(milliseconds);
  return Number.isNaN(date.getTime())
    ? ''
    : date.toLocaleString(locale, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      });
}

const TrendChart = memo(function TrendChart({
  points,
  bucketSeconds,
  observedLabel,
  t,
  locale,
}) {
  const [hoveredPoint, setHoveredPoint] = useState(null);
  const { endpoints, lines, markers } = useMemo(() => {
    const timestampedPoints = points.filter(
      (point) =>
        Number.isFinite(point.ts) &&
        ['probe', 'observed'].includes(point.signal),
    );
    const validPoints = timestampedPoints.filter((point) =>
      Number.isFinite(point.value),
    );
    if (!validPoints.length) {
      return { endpoints: [], lines: [], markers: [] };
    }

    const timestamps = Array.from(
      new Set(timestampedPoints.map((point) => point.ts)),
    ).sort((left, right) => left - right);
    const inferredBucketSeconds = timestamps.reduce((smallest, ts, index) => {
      if (index === 0) return smallest;
      const delta = ts - timestamps[index - 1];
      return delta > 0 && delta < smallest ? delta : smallest;
    }, Number.POSITIVE_INFINITY);
    const expectedBucketSeconds =
      bucketSeconds > 0
        ? bucketSeconds
        : Number.isFinite(inferredBucketSeconds)
          ? inferredBucketSeconds
          : 0;
    const minTimestamp = timestamps[0];
    const maxTimestamp = timestamps[timestamps.length - 1];
    const minValue = Math.min(
      ...validPoints.map((point) => Math.max(0, Math.min(100, point.value))),
    );
    const domainMin = Math.max(0, Math.min(88, Math.floor(minValue - 2)));
    const domainSpan = 100 - domainMin;
    const chartLines = [];
    const chartEndpoints = [];
    const chartMarkers = [];

    const toCoordinate = (point) => {
      const x =
        minTimestamp === maxTimestamp
          ? TREND_WIDTH / 2
          : TREND_LEFT +
            ((point.ts - minTimestamp) / (maxTimestamp - minTimestamp)) *
              (TREND_RIGHT - TREND_LEFT);
      const y =
        TREND_TOP +
        ((100 - point.value) / domainSpan) * (TREND_BOTTOM - TREND_TOP);
      return { x, y };
    };

    ['probe', 'observed'].forEach((signal) => {
      const sortedSeries = timestampedPoints
        .filter((point) => point.signal === signal)
        .sort((left, right) => left.ts - right.ts);
      if (!sortedSeries.length) return;

      const series = [];
      sortedSeries.forEach((point) => {
        const value = Number.isFinite(point.value)
          ? Math.max(0, Math.min(100, point.value))
          : null;
        const normalizedPoint = {
          ts: point.ts,
          signal,
          value,
          runCount: point.runCount,
          successCount: point.successCount,
          attemptCount: point.attemptCount,
        };
        if (series[series.length - 1]?.ts === point.ts) {
          series[series.length - 1] = normalizedPoint;
        } else {
          series.push(normalizedPoint);
        }
      });

      const segments = [];
      let segment = [];
      let missingBuckets = 0;
      let previousTimestamp = null;
      const maxBridgeBuckets = signal === 'probe' ? 2 : 0;
      const flushSegment = () => {
        if (segment.length) segments.push(segment);
        segment = [];
      };
      series.forEach((point) => {
        const implicitMissingBuckets =
          previousTimestamp !== null && expectedBucketSeconds > 0
            ? Math.max(
                0,
                Math.round(
                  (point.ts - previousTimestamp) / expectedBucketSeconds,
                ) - 1,
              )
            : 0;
        missingBuckets += implicitMissingBuckets;
        if (missingBuckets > maxBridgeBuckets) flushSegment();
        if (point.value === null) {
          missingBuckets += 1;
          if (missingBuckets > maxBridgeBuckets) flushSegment();
        } else {
          if (missingBuckets > maxBridgeBuckets) flushSegment();
          segment.push(point);
          missingBuckets = 0;
        }
        previousTimestamp = point.ts;
      });
      flushSegment();

      segments.forEach((pointsInSegment, segmentIndex) => {
        const coordinates = pointsInSegment.map(toCoordinate);
        chartLines.push({
          signal,
          segmentIndex,
          path: buildMonotonePath(coordinates),
        });
        pointsInSegment.forEach((point, index) => {
          chartMarkers.push({
            ...point,
            ...coordinates[index],
            color: batchPointColor(point),
          });
        });
      });

      const lastSegment = segments[segments.length - 1];
      if (lastSegment?.length) {
        const point = lastSegment[lastSegment.length - 1];
        chartEndpoints.push({
          signal,
          color: batchPointColor(point),
          ...toCoordinate(point),
        });
      }
    });

    return {
      endpoints: chartEndpoints,
      lines: chartLines,
      markers: chartMarkers,
    };
  }, [bucketSeconds, points]);

  const hoveredTimestamp = hoveredPoint
    ? formatTrendTimestamp(hoveredPoint.ts, locale)
    : '';
  const hoveredLabel =
    hoveredPoint?.signal === 'probe' ? t('Active availability') : observedLabel;
  const hasBatchCounts =
    hoveredPoint?.signal === 'probe' &&
    Number.isFinite(hoveredPoint.successCount) &&
    Number.isFinite(hoveredPoint.attemptCount) &&
    hoveredPoint.attemptCount > 0;
  const hasMultipleRuns =
    hasBatchCounts &&
    Number.isFinite(hoveredPoint.runCount) &&
    hoveredPoint.runCount > 1;

  return (
    <figure className='min-w-0'>
      <figcaption className='mb-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-semi-color-text-2 lg:justify-end'>
        {[
          { signal: 'probe', label: t('Active availability') },
          { signal: 'observed', label: observedLabel },
        ].map(({ signal, label }) => (
          <span key={signal} className='inline-flex items-center gap-1.5'>
            <svg
              aria-hidden='true'
              className='h-2 w-7 shrink-0'
              preserveAspectRatio='none'
              viewBox='0 0 28 8'
            >
              <line
                x1='1'
                x2='27'
                y1='4'
                y2='4'
                stroke={SIGNAL_META[signal].color}
                strokeDasharray={SIGNAL_META[signal].dasharray}
                strokeLinecap='round'
                strokeWidth='2.25'
              />
            </svg>
            <span>{label}</span>
          </span>
        ))}
      </figcaption>
      <div className='relative'>
        <svg
          aria-label={t('Availability trend')}
          className='block h-16 w-full overflow-visible'
          preserveAspectRatio='none'
          viewBox='0 0 240 64'
          onPointerLeave={() => setHoveredPoint(null)}
        >
          {[TREND_TOP, (TREND_TOP + TREND_BOTTOM) / 2, TREND_BOTTOM].map(
            (y, index) => (
              <line
                key={y}
                x1='4'
                x2='236'
                y1={y}
                y2={y}
                stroke='currentColor'
                strokeDasharray={index === 1 ? '2 5' : undefined}
                strokeOpacity={index === 0 ? '0.12' : '0.08'}
                vectorEffect='non-scaling-stroke'
              />
            ),
          )}
          {lines.map((line) => (
            <path
              key={`${line.signal}-${line.segmentIndex}`}
              d={line.path}
              fill='none'
              stroke={SIGNAL_META[line.signal].color}
              strokeDasharray={SIGNAL_META[line.signal].dasharray}
              strokeLinecap='round'
              strokeLinejoin='round'
              strokeWidth='2'
              vectorEffect='non-scaling-stroke'
            />
          ))}
          {markers
            .filter(
              (marker) =>
                marker.signal === 'probe' &&
                Number.isFinite(marker.attemptCount) &&
                marker.attemptCount > 0 &&
                marker.successCount < marker.attemptCount,
            )
            .map((marker) => (
              <line
                key={`${marker.signal}-${marker.ts}-state`}
                x1={marker.x}
                x2={marker.x}
                y1={marker.y - 0.01}
                y2={marker.y + 0.01}
                stroke={marker.color}
                strokeLinecap='round'
                strokeWidth='4.5'
                vectorEffect='non-scaling-stroke'
              />
            ))}
          {endpoints.flatMap((endpoint) => [
            <line
              key={`${endpoint.signal}-endpoint-halo`}
              x1={endpoint.x}
              x2={endpoint.x}
              y1={endpoint.y - 0.01}
              y2={endpoint.y + 0.01}
              stroke={endpoint.color}
              strokeLinecap='round'
              strokeOpacity='0.2'
              strokeWidth='7'
              vectorEffect='non-scaling-stroke'
            />,
            <line
              key={`${endpoint.signal}-endpoint`}
              x1={endpoint.x}
              x2={endpoint.x}
              y1={endpoint.y - 0.01}
              y2={endpoint.y + 0.01}
              stroke={endpoint.color}
              strokeLinecap='round'
              strokeWidth='3.5'
              vectorEffect='non-scaling-stroke'
            />,
          ])}
          {[...markers]
            .sort((left, right) =>
              left.signal === right.signal
                ? 0
                : left.signal === 'probe'
                  ? 1
                  : -1,
            )
            .map((marker) => (
              <line
                key={`${marker.signal}-${marker.ts}-hit`}
                x1={marker.x}
                x2={marker.x}
                y1={marker.y - 4}
                y2={marker.y + 4}
                stroke='transparent'
                strokeWidth='9'
                vectorEffect='non-scaling-stroke'
                pointerEvents='stroke'
                tabIndex='0'
                role='img'
                aria-label={`${marker.signal === 'probe' ? t('Active availability') : observedLabel}: ${formatPercent(marker.value)}`}
                onFocus={() => setHoveredPoint(marker)}
                onBlur={() => setHoveredPoint(null)}
                onPointerEnter={() => setHoveredPoint(marker)}
              />
            ))}
        </svg>
        {hoveredPoint && (
          <div
            className='pointer-events-none absolute z-10 min-w-40 max-w-56 rounded-md border border-semi-color-border bg-semi-color-bg-0 px-3 py-2 text-xs shadow-lg'
            style={{
              left: `${(hoveredPoint.x / TREND_WIDTH) * 100}%`,
              top: `${(hoveredPoint.y / 64) * 100}%`,
              transform: `translate(${hoveredPoint.x > TREND_WIDTH / 2 ? 'calc(-100% - 8px)' : '8px'}, ${hoveredPoint.y > 32 ? 'calc(-100% - 8px)' : '8px'})`,
            }}
          >
            {hoveredTimestamp && (
              <div className='mb-1 text-semi-color-text-2'>
                {hoveredTimestamp}
              </div>
            )}
            <div className='flex items-center justify-between gap-3'>
              <span className='inline-flex min-w-0 items-center gap-1.5'>
                <span
                  className='h-2 w-2 shrink-0 rounded-full'
                  style={{ backgroundColor: hoveredPoint.color }}
                />
                <span className='truncate'>{hoveredLabel}</span>
              </span>
              {!hasBatchCounts && (
                <span className='font-mono font-semibold tabular-nums'>
                  {formatPercent(hoveredPoint.value)}
                </span>
              )}
            </div>
            {hasBatchCounts && (
              <div className='mt-1 text-semi-color-text-2'>
                {hasMultipleRuns
                  ? t(
                      '{{runs}} runs · {{success}}/{{attempts}} attempts succeeded · {{percent}} equal-weighted per run',
                      {
                        runs: hoveredPoint.runCount,
                        success: hoveredPoint.successCount,
                        attempts: hoveredPoint.attemptCount,
                        percent: formatPercent(hoveredPoint.value),
                      },
                    )
                  : t('{{success}}/{{attempts}} succeeded · {{percent}}', {
                      success: hoveredPoint.successCount,
                      attempts: hoveredPoint.attemptCount,
                      percent: formatPercent(hoveredPoint.value),
                    })}
              </div>
            )}
          </div>
        )}
      </div>
    </figure>
  );
});

function SummaryCard({ icon: Icon, label, value, accent }) {
  return (
    <Card bodyStyle={{ padding: 16 }} className='h-full !rounded-xl'>
      <div className='flex items-center justify-between gap-3'>
        <div>
          <Text type='tertiary' size='small'>
            {label}
          </Text>
          <div className={`mt-1 text-2xl font-semibold ${accent || ''}`}>
            {value}
          </div>
        </div>
        <Icon size={22} className='text-semi-color-primary' />
      </div>
    </Card>
  );
}

function HealthMetric({ label, value }) {
  return (
    <div className='min-w-0'>
      <Text type='tertiary' size='small' className='leading-tight'>
        {label}
      </Text>
      <div className='mt-1 whitespace-nowrap font-mono font-semibold tabular-nums'>
        {value}
      </div>
    </div>
  );
}

function HealthRow({ row, t, locale, pool = false }) {
  const mismatch =
    row.signalConflict ||
    (row.probe.status !== 'idle' &&
      row.observed.status !== 'idle' &&
      row.probe.status !== row.observed.status);
  const title = pool ? row.poolAlias : row.groupAlias;
  const subtitle = pool
    ? `${row.groupAlias} · ${row.modelAlias}`
    : row.modelAlias;
  const observedSuccessLabel = pool
    ? t('Channel attempt success rate')
    : t('Observed success rate');
  const observedCountLabel = pool ? t('Channel attempts') : t('Real requests');
  return (
    <div
      className={`grid grid-cols-1 gap-4 px-4 py-4 ${
        pool ? 'border-t border-semi-color-border bg-semi-color-fill-0' : ''
      }`}
    >
      <div className='grid grid-cols-1 gap-3 lg:grid-cols-[minmax(210px,0.7fr)_minmax(260px,2fr)] lg:items-center'>
        <div className='min-w-0'>
          <div className='flex flex-wrap items-center gap-2'>
            <span className='truncate font-semibold' title={title}>
              {title}
            </span>
            <Tag color={STATUS_META[row.status].color}>
              {statusLabel(t, row.status)}
            </Tag>
            {mismatch && (
              <Tag color='orange' prefixIcon={<AlertTriangle size={12} />}>
                {t('Signal mismatch')}
              </Tag>
            )}
          </div>
          <div className='truncate' title={subtitle}>
            <Text type='tertiary' size='small'>
              {subtitle}
            </Text>
          </div>
        </div>

        <TrendChart
          points={row.trend}
          bucketSeconds={row.trendBucketSeconds}
          observedLabel={observedSuccessLabel}
          t={t}
          locale={locale}
        />
      </div>

      <div className='grid grid-cols-[repeat(auto-fit,minmax(8rem,1fr))] gap-x-5 gap-y-3 text-xs'>
        <HealthMetric
          label={t('Active availability')}
          value={formatPercent(row.probe.availability)}
        />
        <HealthMetric
          label={t('Latest latency')}
          value={formatLatency(row.probe.latency)}
        />
        <HealthMetric
          label={observedSuccessLabel}
          value={
            row.observed.requests > 0
              ? formatPercent(row.observed.successRate)
              : '--'
          }
        />
        <HealthMetric
          label={observedCountLabel}
          value={formatCount(row.observed.requests, locale)}
        />
        <HealthMetric
          label={t('Observed latency')}
          value={formatLatency(row.observed.latency)}
        />
        <HealthMetric label='TTFT' value={formatLatency(row.observed.ttft)} />
        <HealthMetric label='TPS' value={formatTps(row.observed.tps)} />
      </div>
    </div>
  );
}

function HealthCard({ row, pools, t, locale }) {
  return (
    <Card className='overflow-hidden !rounded-xl' bodyStyle={{ padding: 0 }}>
      <HealthRow row={row} t={t} locale={locale} />
      {pools.length > 0 && (
        <details open className='border-t border-semi-color-border'>
          <summary className='cursor-pointer select-none px-4 py-3 text-sm font-medium hover:bg-semi-color-fill-0'>
            {t('Pool details')} ({pools.length})
          </summary>
          <div>
            {pools.map((poolRow) => (
              <HealthRow
                key={poolRow.key}
                row={poolRow}
                t={t}
                locale={locale}
                pool
              />
            ))}
          </div>
        </details>
      )}
    </Card>
  );
}

export default function Health() {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language;
  const [catalog, setCatalog] = useState({ groups: [], models: [], pools: [] });
  const [rows, setRows] = useState([]);
  const [poolRows, setPoolRows] = useState([]);
  const [summary, setSummary] = useState({});
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [pools, setPools] = useState([]);
  const [statuses, setStatuses] = useState([]);
  const [windowValue, setWindowValue] = useState('12h');
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState(false);
  const [visible, setVisible] = useState(!document.hidden);
  const requestRef = useRef(null);

  const loadData = useCallback(
    async (background = false) => {
      requestRef.current?.abort();
      const controller = new AbortController();
      requestRef.current = controller;
      background ? setRefreshing(true) : setLoading(true);
      setError(false);
      try {
        const query = buildQuery(windowValue, groups, models, pools);
        const [catalogResponse, overviewResponse, seriesResponse] =
          await Promise.all([
            API.get('/api/model-health/catalog', { signal: controller.signal }),
            API.get(`/api/model-health/overview?${query}`, {
              signal: controller.signal,
            }),
            API.get(`/api/model-health/series?${query}`, {
              signal: controller.signal,
            }),
          ]);
        const catalogData = normalizeCatalog(unwrapResponse(catalogResponse));
        const overviewData = unwrapResponse(overviewResponse);
        const seriesPayload = unwrapResponse(seriesResponse);
        const seriesData = normalizeSeries(seriesPayload);
        const poolSeriesData = normalizeSeries(
          seriesPayload,
          'pool_rows',
          true,
        );
        setCatalog(catalogData);
        setSummary(overviewData.summary ?? overviewData.overview ?? {});
        setRows(
          normalizeRows(overviewData).map((row) => ({
            ...row,
            trend: seriesData.seriesByKey.get(row.key) ?? [],
            trendBucketSeconds: seriesData.bucketSeconds,
          })),
        );
        setPoolRows(
          normalizeRows(overviewData, 'pool_rows', true).map((row) => ({
            ...row,
            trend: poolSeriesData.seriesByKey.get(row.key) ?? [],
            trendBucketSeconds: poolSeriesData.bucketSeconds,
          })),
        );
      } catch (requestError) {
        if (requestError?.name !== 'CanceledError') setError(true);
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          setRefreshing(false);
        }
      }
    },
    [groups, models, pools, windowValue],
  );

  useEffect(() => {
    void loadData(false);
    return () => requestRef.current?.abort();
  }, [loadData]);

  useEffect(() => {
    const handleVisibility = () => setVisible(!document.hidden);
    document.addEventListener('visibilitychange', handleVisibility);
    return () =>
      document.removeEventListener('visibilitychange', handleVisibility);
  }, []);

  useEffect(() => {
    if (!visible) return undefined;
    const interval = window.setInterval(() => void loadData(true), 60000);
    return () => window.clearInterval(interval);
  }, [loadData, visible]);

  const visibleCards = useMemo(() => {
    return rows.flatMap((row) => {
      const children = poolRows.filter(
        (poolRow) =>
          poolRow.groupAlias === row.groupAlias &&
          poolRow.modelAlias === row.modelAlias,
      );
      if (statuses.length === 0) return [{ row, pools: children }];
      if (statuses.includes(row.status)) return [{ row, pools: children }];
      const matchingPools = children.filter((poolRow) =>
        statuses.includes(poolRow.status),
      );
      return matchingPools.length > 0 ? [{ row, pools: matchingPools }] : [];
    });
  }, [poolRows, rows, statuses]);

  const counts = useMemo(() => {
    const source = summary.counts ?? summary.status_counts ?? {};
    return {
      healthy: numberValue(
        source.healthy,
        summary.healthy,
        summary.healthy_count,
      ),
      fluctuating: numberValue(
        source.fluctuating,
        source.degraded,
        summary.fluctuating,
        summary.fluctuating_count,
      ),
      unstable: numberValue(
        source.unstable,
        source.unhealthy,
        summary.unstable,
        summary.unstable_count,
      ),
      idle: numberValue(source.idle, summary.idle, summary.idle_count),
    };
  }, [summary]);

  const activeAvailability = nullableNumber(
    summary.active_availability,
    summary.probe_availability,
    summary.availability,
  );
  const observedSuccess = nullableNumber(
    summary.observed_success_rate,
    summary.passive_success_rate,
  );

  return (
    <div className='min-h-[calc(100vh-64px)] bg-semi-color-bg-0 px-4 py-6 md:px-8'>
      <div className='mx-auto max-w-[1500px]'>
        <div className='mb-5 flex flex-wrap items-start justify-between gap-4'>
          <div>
            <div className='flex items-center gap-2'>
              <Activity size={24} className='text-semi-color-primary' />
              <Title heading={3}>{t('Model health status')}</Title>
            </div>
            <Text type='tertiary'>
              {t(
                'Active probes show current availability while observed traffic provides real-world evidence.',
              )}
            </Text>
          </div>
          <Button
            icon={<RefreshCw size={15} />}
            loading={refreshing}
            onClick={() => void loadData(true)}
          >
            {t('Refresh')}
          </Button>
        </div>

        {error && (
          <Banner
            type='warning'
            className='mb-4'
            description={t('Health data unavailable')}
          />
        )}

        <Spin spinning={loading} size='large'>
          <div className='mb-4 grid grid-cols-2 gap-3 lg:grid-cols-6'>
            <div className='col-span-2'>
              <SummaryCard
                icon={Gauge}
                label={t('Active availability')}
                value={formatPercent(activeAvailability)}
                accent='text-emerald-500'
              />
            </div>
            <SummaryCard
              icon={Activity}
              label={t('Healthy')}
              value={counts.healthy}
              accent='text-emerald-500'
            />
            <SummaryCard
              icon={Activity}
              label={t('Fluctuating')}
              value={counts.fluctuating}
              accent='text-amber-500'
            />
            <SummaryCard
              icon={AlertTriangle}
              label={t('Unstable')}
              value={counts.unstable}
              accent='text-rose-500'
            />
            <SummaryCard
              icon={Timer}
              label={t('Idle')}
              value={counts.idle}
              accent='text-gray-400'
            />
          </div>

          <Card className='mb-4 !rounded-xl' bodyStyle={{ padding: 16 }}>
            <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-5'>
              <Select
                multiple
                filter
                maxTagCount={2}
                value={models}
                optionList={catalog.models.map((model) => ({
                  label: model,
                  value: model,
                }))}
                placeholder={t('All models')}
                onChange={setModels}
              />
              <Select
                multiple
                filter
                maxTagCount={2}
                value={groups}
                optionList={catalog.groups.map((group) => ({
                  label: group,
                  value: group,
                }))}
                placeholder={t('All groups')}
                onChange={setGroups}
              />
              <Select
                multiple
                filter
                maxTagCount={2}
                value={pools}
                optionList={catalog.pools.map((pool) => ({
                  label: `${pool.alias} · ${pool.group}`,
                  value: pool.key,
                }))}
                placeholder={t('All pools')}
                onChange={setPools}
              />
              <Select
                multiple
                maxTagCount={2}
                value={statuses}
                optionList={STATUS_ORDER.map((status) => ({
                  label: statusLabel(t, status),
                  value: status,
                }))}
                placeholder={t('All Status')}
                onChange={setStatuses}
              />
              <Select
                value={windowValue}
                onChange={setWindowValue}
                optionList={[
                  { label: t('Last 12 hours'), value: '12h' },
                  { label: t('Last 24 hours'), value: '24h' },
                  { label: t('Last 7 days'), value: '7d' },
                  { label: t('Last 30 days'), value: '30d' },
                ]}
              />
            </div>
            <div className='mt-3 flex flex-wrap gap-x-6 gap-y-1 text-xs'>
              <Text type='tertiary'>
                {t('Observed success rate')}: {formatPercent(observedSuccess)}
              </Text>
              <Text type='tertiary'>
                {t(
                  'Active and observed percentages stay separate and use the same severity thresholds.',
                )}
              </Text>
            </div>
          </Card>

          {visibleCards.length ? (
            <div className='space-y-4'>
              {visibleCards.map((card) => (
                <HealthCard
                  key={card.row.key}
                  row={card.row}
                  pools={card.pools}
                  t={t}
                  locale={locale}
                />
              ))}
            </div>
          ) : (
            !loading && (
              <Card className='!rounded-xl'>
                <Empty
                  description={t('No models match the selected filters')}
                />
              </Card>
            )
          )}
        </Spin>
      </div>
    </div>
  );
}
