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
export type WarmupHostStatus = {
  host: string
  proxy?: string
  last_status_code: number
  last_latency_ms: number
  last_error?: string
  success_count: number
  failure_count: number
  last_success_at?: number
  last_check_at: number
  connect_success_count?: number
  reusable_success_count?: number
  drain_failure_count?: number
  last_reusable_at?: number
}

export type AggregatedWarmupHostStatus = {
  host: string
  connectionCount: number
  state: 'reusable' | 'failed' | 'partial'
  statusCodes: number[]
  minLatencyMs?: number
  maxLatencyMs?: number
  reusableCount: number
  failureCount: number
  lastCheckAt?: number
  lastReusableAt?: number
  lastError?: string
}

type WarmupStatusAccumulator = AggregatedWarmupHostStatus & {
  failedConnectionCount: number
  reusableConnectionCount: number
  lastErrorAt: number
  statusCodeSet: Set<number>
}

function maxTimestamp(
  current: number | undefined,
  candidate: number | undefined
) {
  if (!candidate || candidate <= 0) return current
  return Math.max(current ?? 0, candidate)
}

export function aggregateWarmupHostStatuses(
  statuses: WarmupHostStatus[]
): AggregatedWarmupHostStatus[] {
  const grouped = new Map<string, WarmupStatusAccumulator>()

  for (const status of statuses) {
    const host = status.host.trim().toLowerCase()
    let aggregate = grouped.get(host)
    if (!aggregate) {
      aggregate = {
        host,
        connectionCount: 0,
        state: 'reusable',
        statusCodes: [],
        reusableCount: 0,
        failureCount: 0,
        failedConnectionCount: 0,
        reusableConnectionCount: 0,
        lastErrorAt: 0,
        statusCodeSet: new Set<number>(),
      }
      grouped.set(host, aggregate)
    }

    aggregate.connectionCount += 1
    aggregate.reusableCount +=
      status.reusable_success_count ?? status.success_count ?? 0
    aggregate.failureCount += status.failure_count ?? 0
    aggregate.lastCheckAt = maxTimestamp(
      aggregate.lastCheckAt,
      status.last_check_at
    )
    aggregate.lastReusableAt = maxTimestamp(
      aggregate.lastReusableAt,
      status.last_reusable_at ?? status.last_success_at
    )

    if (
      Number.isFinite(status.last_status_code) &&
      status.last_status_code > 0
    ) {
      aggregate.statusCodeSet.add(status.last_status_code)
    }

    if (Number.isFinite(status.last_latency_ms) && status.last_latency_ms > 0) {
      aggregate.minLatencyMs = Math.min(
        aggregate.minLatencyMs ?? status.last_latency_ms,
        status.last_latency_ms
      )
      aggregate.maxLatencyMs = Math.max(
        aggregate.maxLatencyMs ?? status.last_latency_ms,
        status.last_latency_ms
      )
    }

    if (status.last_error) {
      aggregate.failedConnectionCount += 1
      if (status.last_check_at >= aggregate.lastErrorAt) {
        aggregate.lastError = status.last_error
        aggregate.lastErrorAt = status.last_check_at
      }
    } else {
      aggregate.reusableConnectionCount += 1
    }
  }

  return [...grouped.values()]
    .sort((a, b) => a.host.localeCompare(b.host))
    .map((aggregate) => {
      if (aggregate.failedConnectionCount === 0) {
        aggregate.state = 'reusable'
      } else if (aggregate.reusableConnectionCount === 0) {
        aggregate.state = 'failed'
      } else {
        aggregate.state = 'partial'
      }

      return {
        host: aggregate.host,
        connectionCount: aggregate.connectionCount,
        state: aggregate.state,
        statusCodes: [...aggregate.statusCodeSet].sort((a, b) => a - b),
        minLatencyMs: aggregate.minLatencyMs,
        maxLatencyMs: aggregate.maxLatencyMs,
        reusableCount: aggregate.reusableCount,
        failureCount: aggregate.failureCount,
        lastCheckAt: aggregate.lastCheckAt,
        lastReusableAt: aggregate.lastReusableAt,
        lastError: aggregate.lastError,
      }
    })
}

export function formatWarmupLatency(
  status: Pick<AggregatedWarmupHostStatus, 'minLatencyMs' | 'maxLatencyMs'>
): string {
  if (status.minLatencyMs === undefined || status.maxLatencyMs === undefined) {
    return '-'
  }
  if (status.minLatencyMs === status.maxLatencyMs) {
    return `${status.minLatencyMs}ms`
  }
  return `${status.minLatencyMs}–${status.maxLatencyMs}ms`
}
