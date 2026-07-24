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

import { buildModelHealthCards } from './pool-hierarchy'
import type { ModelHealthPoolRow, ModelHealthRow } from './types'

const observed = {
  status: 'healthy' as const,
  success_rate: 100,
  request_count: 60,
  avg_latency_ms: 500,
  avg_ttft_ms: 100,
  avg_tps: 20,
}
const probe = {
  status: 'healthy' as const,
  availability: 100,
  sample_count: 10,
  latest_latency_ms: 400,
}
const summary: ModelHealthRow = {
  group: 'CC-MAX',
  model: 'claude-opus-4-8',
  status: 'healthy',
  signal_conflict: false,
  observed,
  probe,
}
const pools: ModelHealthPoolRow[] = [
  {
    ...summary,
    pool_key: 'pool-a',
    pool: 'CCMAX-A池',
    status: 'unstable',
  },
  {
    ...summary,
    pool_key: 'pool-b',
    pool: 'CCMAX-B池',
  },
]

describe('model health pool hierarchy', () => {
  it('keeps one summary while retaining independent pools', () => {
    const cards = buildModelHealthCards([summary], pools, [], [])
    assert.equal(cards.length, 1)
    assert.deepEqual(
      cards[0]?.pools.map((pool) => pool.pool),
      ['CCMAX-A池', 'CCMAX-B池']
    )
  })

  it('keeps the parent as context when only a pool matches status', () => {
    const cards = buildModelHealthCards([summary], pools, ['unstable'], [])
    assert.equal(cards.length, 1)
    assert.deepEqual(
      cards[0]?.pools.map((pool) => pool.pool_key),
      ['pool-a']
    )
  })

  it('filters cards by selected public pool keys', () => {
    const cards = buildModelHealthCards([summary], pools, [], ['pool-b'])
    assert.equal(cards.length, 1)
    assert.deepEqual(
      cards[0]?.pools.map((pool) => pool.pool_key),
      ['pool-b']
    )
  })
})
