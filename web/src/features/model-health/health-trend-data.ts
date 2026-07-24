/*
Copyright (C) 2023-2026 QuantumNous

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
import type { ModelHealthPoint } from './types'

export const HEALTH_TREND_SIGNALS = ['probe', 'observed'] as const

export type HealthTrendSignal = (typeof HEALTH_TREND_SIGNALS)[number]

export const HEALTH_TREND_SIGNAL_STYLES: Record<
  HealthTrendSignal,
  {
    color: string
    lineDash: number[]
    curveType: 'monotone'
  }
> = {
  probe: { color: '#10b981', lineDash: [], curveType: 'monotone' },
  observed: {
    color: '#3b82f6',
    lineDash: [8, 6],
    curveType: 'monotone',
  },
}

export const HEALTH_TREND_BATCH_COLORS = {
  fluctuating: '#f59e0b',
  unstable: '#ef4444',
} as const

export interface HealthTrendDatum {
  ts: number
  timeLabel: string
  signal: HealthTrendSignal
  signalLabel: string
  value: number | null
  probeRunCount: number | null
  probeSuccessCount: number | null
  probeAttemptCount: number | null
  isLast: boolean
}

export type HealthTrendSeriesData = Record<
  HealthTrendSignal,
  HealthTrendDatum[]
>

export function getHealthTrendSignalStyle(signal: unknown) {
  return signal === 'observed'
    ? HEALTH_TREND_SIGNAL_STYLES.observed
    : HEALTH_TREND_SIGNAL_STYLES.probe
}

export function getHealthTrendPointColor(datum?: Partial<HealthTrendDatum>) {
  if (
    datum?.signal === 'probe' &&
    typeof datum.probeSuccessCount === 'number' &&
    typeof datum.probeAttemptCount === 'number' &&
    datum.probeAttemptCount > 0 &&
    datum.probeSuccessCount < datum.probeAttemptCount
  ) {
    return datum.probeSuccessCount === 0
      ? HEALTH_TREND_BATCH_COLORS.unstable
      : HEALTH_TREND_BATCH_COLORS.fluctuating
  }
  return getHealthTrendSignalStyle(datum?.signal).color
}

export function isPartialProbeBatch(datum?: Partial<HealthTrendDatum>) {
  return (
    datum?.signal === 'probe' &&
    typeof datum.probeSuccessCount === 'number' &&
    typeof datum.probeAttemptCount === 'number' &&
    datum.probeAttemptCount > 0 &&
    datum.probeSuccessCount < datum.probeAttemptCount
  )
}

export function getHealthTrendPointSize(datum?: Partial<HealthTrendDatum>) {
  if (datum?.isLast === true) return 5.5
  if (isPartialProbeBatch(datum)) return 4.5
  return 0
}

export function buildHealthTrendData(
  points: ModelHealthPoint[],
  labels: Record<HealthTrendSignal, string>,
  formatTime: (timestamp: number) => string = (timestamp) =>
    new Date(timestamp * 1000).toLocaleString(),
  bucketSeconds = 0
): HealthTrendSeriesData {
  const pointsByTimestamp = new Map<
    number,
    {
      ts: number
      probeAvailability: number | null
      probeRunCount: number | null
      probeSuccessCount: number | null
      probeAttemptCount: number | null
      observedSuccessRate: number | null
    }
  >()
  for (const point of points) {
    if (!Number.isFinite(point.ts)) continue

    const bucket = pointsByTimestamp.get(point.ts) ?? {
      ts: point.ts,
      probeAvailability: null,
      probeRunCount: null,
      probeSuccessCount: null,
      probeAttemptCount: null,
      observedSuccessRate: null,
    }
    if (
      point.probe_availability != null &&
      Number.isFinite(point.probe_availability)
    ) {
      bucket.probeAvailability = point.probe_availability
      if (
        typeof point.probe_run_count === 'number' &&
        Number.isFinite(point.probe_run_count) &&
        point.probe_run_count > 0
      ) {
        bucket.probeRunCount = point.probe_run_count
      }
      const successCount =
        point.probe_success_count ?? point.success_count ?? null
      const attemptCount =
        point.probe_attempt_count ?? point.attempt_count ?? null
      if (
        typeof successCount === 'number' &&
        Number.isFinite(successCount) &&
        typeof attemptCount === 'number' &&
        Number.isFinite(attemptCount) &&
        successCount >= 0 &&
        attemptCount > 0 &&
        successCount <= attemptCount
      ) {
        bucket.probeSuccessCount = successCount
        bucket.probeAttemptCount = attemptCount
      }
    }
    if (
      point.observed_success_rate != null &&
      Number.isFinite(point.observed_success_rate)
    ) {
      bucket.observedSuccessRate = point.observed_success_rate
    }
    pointsByTimestamp.set(point.ts, bucket)
  }

  const orderedPoints = [...pointsByTimestamp.values()].sort(
    (left, right) => left.ts - right.ts
  )
  const inferredBucketSeconds = orderedPoints.reduce(
    (smallest, point, index) => {
      if (index === 0) return smallest
      const delta = point.ts - orderedPoints[index - 1].ts
      return delta > 0 && delta < smallest ? delta : smallest
    },
    Number.POSITIVE_INFINITY
  )
  const expectedBucketSeconds =
    Number.isFinite(bucketSeconds) && bucketSeconds > 0
      ? bucketSeconds
      : inferredBucketSeconds
  const gapAwarePoints: typeof orderedPoints = []
  for (const point of orderedPoints) {
    const previous = gapAwarePoints.at(-1)
    if (
      previous &&
      Number.isFinite(expectedBucketSeconds) &&
      point.ts - previous.ts > expectedBucketSeconds * 1.5
    ) {
      const missingBucketCount = Math.min(
        3,
        Math.max(
          1,
          Math.round((point.ts - previous.ts) / expectedBucketSeconds) - 1
        )
      )
      for (let index = 1; index <= missingBucketCount; index += 1) {
        gapAwarePoints.push({
          ts: previous.ts + expectedBucketSeconds * index,
          probeAvailability: null,
          probeRunCount: null,
          probeSuccessCount: null,
          probeAttemptCount: null,
          observedSuccessRate: null,
        })
      }
    }
    gapAwarePoints.push(point)
  }
  const seriesData: HealthTrendSeriesData = { probe: [], observed: [] }
  const bridgedProbeTimestamps = new Set<number>()

  for (let index = 0; index < gapAwarePoints.length; ) {
    if (gapAwarePoints[index].probeAvailability != null) {
      index += 1
      continue
    }

    const gapStart = index
    while (
      index < gapAwarePoints.length &&
      gapAwarePoints[index].probeAvailability == null
    ) {
      index += 1
    }

    const gapLength = index - gapStart
    const hasValidBounds =
      gapStart > 0 &&
      index < gapAwarePoints.length &&
      gapAwarePoints[gapStart - 1].probeAvailability != null &&
      gapAwarePoints[index].probeAvailability != null
    if (hasValidBounds && gapLength <= 2) {
      for (let gapIndex = gapStart; gapIndex < index; gapIndex += 1) {
        bridgedProbeTimestamps.add(gapAwarePoints[gapIndex].ts)
      }
    }
  }

  for (const point of gapAwarePoints) {
    const timeLabel = formatTime(point.ts)
    const probeValue =
      point.probeAvailability == null
        ? null
        : Math.min(100, Math.max(0, point.probeAvailability))
    const observedValue =
      point.observedSuccessRate == null
        ? null
        : Math.min(100, Math.max(0, point.observedSuccessRate))

    if (!bridgedProbeTimestamps.has(point.ts)) {
      seriesData.probe.push({
        ts: point.ts,
        timeLabel,
        signal: 'probe',
        signalLabel: labels.probe,
        value: probeValue,
        probeRunCount: point.probeRunCount,
        probeSuccessCount: point.probeSuccessCount,
        probeAttemptCount: point.probeAttemptCount,
        isLast: false,
      })
    }
    seriesData.observed.push({
      ts: point.ts,
      timeLabel,
      signal: 'observed',
      signalLabel: labels.observed,
      value: observedValue,
      probeRunCount: null,
      probeSuccessCount: null,
      probeAttemptCount: null,
      isLast: false,
    })
  }

  for (const signal of HEALTH_TREND_SIGNALS) {
    const signalData = seriesData[signal]
    for (let index = signalData.length - 1; index >= 0; index -= 1) {
      if (signalData[index].value == null) continue
      signalData[index].isLast = true
      break
    }
  }

  return seriesData
}

export function getHealthTrendXDomain(
  data: HealthTrendSeriesData
): [number, number] {
  let minTimestamp = Number.POSITIVE_INFINITY
  let maxTimestamp = Number.NEGATIVE_INFINITY
  for (const signal of HEALTH_TREND_SIGNALS) {
    for (const datum of data[signal]) {
      if (!Number.isFinite(datum.ts)) continue
      minTimestamp = Math.min(minTimestamp, datum.ts)
      maxTimestamp = Math.max(maxTimestamp, datum.ts)
    }
  }
  if (!Number.isFinite(minTimestamp) || !Number.isFinite(maxTimestamp)) {
    return [0, 1]
  }
  if (minTimestamp === maxTimestamp) {
    return [minTimestamp - 1, maxTimestamp + 1]
  }
  return [minTimestamp, maxTimestamp]
}

export function getHealthTrendYDomain(
  data: HealthTrendSeriesData
): [number, number] {
  let lowestValue = Number.POSITIVE_INFINITY
  for (const signal of HEALTH_TREND_SIGNALS) {
    for (const datum of data[signal]) {
      if (datum.value != null) lowestValue = Math.min(lowestValue, datum.value)
    }
  }
  if (!Number.isFinite(lowestValue)) return [0, 100]

  const paddedLowerBound = Math.floor(lowestValue - 2)

  return [Math.max(0, Math.min(88, paddedLowerBound)), 100]
}
