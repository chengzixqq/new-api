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
import type { PerformanceGroup } from '@/features/performance-metrics/types'

import type { UptimeDayPoint } from './mock-stats'

export type ModelPerformanceAggregate = {
  avgLatencyMs: number
  avgTps: number
  successRate: number
}

export type ModelLatencyPoint = {
  timestamp: string
  group: string
  ttft_ms: number
}

function percentage(successCount: number, requestCount: number): number {
  if (requestCount <= 0) return 0
  return Math.min(100, Math.max(0, (successCount / requestCount) * 100))
}

export function aggregateModelPerformance(
  groups: PerformanceGroup[]
): ModelPerformanceAggregate {
  let requestCount = 0
  let successCount = 0
  let totalLatencyMs = 0
  let outputTokens = 0
  let generationMs = 0
  for (const group of groups) {
    requestCount += group.request_count
    successCount += group.success_count
    totalLatencyMs += group.total_latency_ms
    outputTokens += group.output_tokens
    generationMs += group.generation_ms
  }
  return {
    avgLatencyMs:
      requestCount > 0 ? Math.round(totalLatencyMs / requestCount) : 0,
    avgTps:
      outputTokens > 0 && generationMs > 0
        ? outputTokens / (generationMs / 1000)
        : 0,
    successRate: percentage(successCount, requestCount),
  }
}

export function buildModelLatencySeries(
  groups: PerformanceGroup[]
): ModelLatencyPoint[] {
  const byTs = new Map<number, { ttftSumMs: number; ttftCount: number }>()
  for (const group of groups) {
    for (const point of group.series) {
      const current = byTs.get(point.ts) ?? { ttftSumMs: 0, ttftCount: 0 }
      current.ttftSumMs += point.ttft_sum_ms
      current.ttftCount += point.ttft_count
      byTs.set(point.ts, current)
    }
  }
  return [...byTs.entries()]
    .filter(([, value]) => value.ttftCount > 0)
    .sort(([left], [right]) => left - right)
    .map(([ts, value]) => ({
      timestamp: new Date(ts * 1000).toISOString(),
      group: 'latency',
      ttft_ms: Math.round(value.ttftSumMs / value.ttftCount),
    }))
}

export function buildModelUptimeSeries(
  groups: PerformanceGroup[]
): UptimeDayPoint[] {
  const byTs = new Map<number, { requestCount: number; successCount: number }>()
  for (const group of groups) {
    for (const point of group.series) {
      const current = byTs.get(point.ts) ?? {
        requestCount: 0,
        successCount: 0,
      }
      current.requestCount += point.request_count
      current.successCount += point.success_count
      byTs.set(point.ts, current)
    }
  }
  return [...byTs.entries()]
    .filter(([, value]) => value.requestCount > 0)
    .sort(([left], [right]) => left - right)
    .map(([ts, value]) => {
      const uptime = percentage(value.successCount, value.requestCount)
      return {
        date: new Date(ts * 1000).toISOString(),
        uptime_pct: Math.round(uptime * 100) / 100,
        incidents: uptime < 100 ? 1 : 0,
        outage_minutes: 0,
      }
    })
}
