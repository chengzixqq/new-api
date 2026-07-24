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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  aggregateWarmupHostStatuses,
  formatWarmupLatency,
  type WarmupHostStatus,
} from './upstream-warmup-status.ts'

function warmupStatus(
  overrides: Partial<WarmupHostStatus> = {}
): WarmupHostStatus {
  return {
    host: 'api.example.com',
    last_status_code: 200,
    last_latency_ms: 20,
    success_count: 1,
    failure_count: 0,
    last_check_at: 100,
    ...overrides,
  }
}

test('merges connections by case-insensitive host and reports their range', () => {
  const result = aggregateWarmupHostStatuses([
    warmupStatus({
      host: 'API.Example.com:443',
      proxy: 'proxy-a',
      last_status_code: 401,
      last_latency_ms: 20,
      success_count: 2,
      reusable_success_count: 3,
      last_reusable_at: 90,
    }),
    warmupStatus({
      host: 'api.example.com:443',
      proxy: 'proxy-b',
      last_status_code: 200,
      last_latency_ms: 50,
      success_count: 4,
      failure_count: 2,
      last_check_at: 110,
      last_success_at: 95,
    }),
    warmupStatus({ host: 'other.example.com', last_latency_ms: 0 }),
  ])

  assert.equal(result.length, 2)
  assert.deepEqual(result[0], {
    host: 'api.example.com:443',
    connectionCount: 2,
    state: 'reusable',
    statusCodes: [200, 401],
    minLatencyMs: 20,
    maxLatencyMs: 50,
    reusableCount: 7,
    failureCount: 2,
    lastCheckAt: 110,
    lastReusableAt: 95,
    lastError: undefined,
  })
  assert.equal(formatWarmupLatency(result[0]), '20–50ms')
  assert.equal(formatWarmupLatency(result[1]), '-')
})

test('reports partial and failed states and keeps the latest error', () => {
  const partial = aggregateWarmupHostStatuses([
    warmupStatus({ last_latency_ms: 30, last_check_at: 100 }),
    warmupStatus({
      last_latency_ms: 0,
      last_error: 'older error',
      last_check_at: 101,
    }),
    warmupStatus({
      last_latency_ms: 0,
      last_error: 'latest error',
      last_check_at: 102,
    }),
  ])[0]
  const failed = aggregateWarmupHostStatuses([
    warmupStatus({ last_error: 'first error' }),
    warmupStatus({ last_error: 'second error' }),
  ])[0]

  assert.equal(partial.state, 'partial')
  assert.equal(partial.lastError, 'latest error')
  assert.equal(formatWarmupLatency(partial), '30ms')
  assert.equal(failed.state, 'failed')
})
