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
import { describe, it } from 'node:test'

import type { PerformanceGroup } from '@/features/performance-metrics/types'

import {
  aggregateModelPerformance,
  buildModelLatencySeries,
  buildModelUptimeSeries,
} from './model-performance-aggregation'

const groups: PerformanceGroup[] = [
  {
    group: 'large',
    request_count: 90,
    success_count: 90,
    total_latency_ms: 9000,
    ttft_sum_ms: 4500,
    ttft_count: 90,
    output_tokens: 900,
    generation_ms: 9000,
    avg_ttft_ms: 50,
    avg_latency_ms: 100,
    success_rate: 100,
    avg_tps: 100,
    series: [
      {
        ts: 1_700_000_000,
        request_count: 90,
        success_count: 90,
        total_latency_ms: 9000,
        ttft_sum_ms: 4500,
        ttft_count: 90,
        output_tokens: 900,
        generation_ms: 9000,
        avg_ttft_ms: 50,
        avg_latency_ms: 100,
        success_rate: 100,
        avg_tps: 100,
      },
    ],
  },
  {
    group: 'small',
    request_count: 10,
    success_count: 0,
    total_latency_ms: 10_000,
    ttft_sum_ms: 5000,
    ttft_count: 10,
    output_tokens: 100,
    generation_ms: 1000,
    avg_ttft_ms: 500,
    avg_latency_ms: 1000,
    success_rate: 0,
    avg_tps: 100,
    series: [
      {
        ts: 1_700_000_000,
        request_count: 10,
        success_count: 0,
        total_latency_ms: 10_000,
        ttft_sum_ms: 5000,
        ttft_count: 10,
        output_tokens: 100,
        generation_ms: 1000,
        avg_ttft_ms: 500,
        avg_latency_ms: 1000,
        success_rate: 0,
        avg_tps: 100,
      },
    ],
  },
]

describe('model performance aggregation', () => {
  it('uses the raw numerator and denominator for cross-group totals', () => {
    assert.deepEqual(aggregateModelPerformance(groups), {
      avgLatencyMs: 190,
      avgTps: 100,
      successRate: 90,
    })
  })

  it('weights TTFT and success-rate trend buckets by their own counts', () => {
    assert.equal(buildModelLatencySeries(groups)[0]?.ttft_ms, 95)
    assert.equal(buildModelUptimeSeries(groups)[0]?.uptime_pct, 90)
  })
})
