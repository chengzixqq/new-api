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

import { QueryClient } from '@tanstack/react-query'

import {
  createUsageLogsQueryKey,
  createUsageLogStatsQueryKey,
} from './query-state.ts'

test('initial, page, filter, and reset transitions issue only required requests', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  let listRequests = 0
  let statsRequests = 0
  const initial = { p: 1, page_size: 50, model_name: '' }
  const pageTwo = { ...initial, p: 2 }
  const filtered = { ...initial, model_name: 'gpt-test' }

  const fetchList = (params: object) =>
    client.fetchQuery({
      queryKey: createUsageLogsQueryKey('common', true, params),
      queryFn: async () => {
        listRequests += 1
        return { total: 0, items: [] }
      },
      staleTime: 0,
    })
  const fetchStats = (params: object) =>
    client.fetchQuery({
      queryKey: createUsageLogStatsQueryKey(true, params),
      queryFn: async () => {
        statsRequests += 1
        return { quota: 0, rpm: 0, tpm: 0 }
      },
      staleTime: 5_000,
    })

  await Promise.all([fetchList(initial), fetchStats(initial)])
  assert.deepEqual(
    { listRequests, statsRequests },
    { listRequests: 1, statsRequests: 1 }
  )

  await Promise.all([fetchList(pageTwo), fetchStats(pageTwo)])
  assert.deepEqual(
    { listRequests, statsRequests },
    { listRequests: 2, statsRequests: 1 }
  )

  await Promise.all([fetchList(filtered), fetchStats(filtered)])
  assert.deepEqual(
    { listRequests, statsRequests },
    { listRequests: 3, statsRequests: 2 }
  )

  await Promise.all([fetchList(initial), fetchStats(initial)])
  assert.deepEqual(
    { listRequests, statsRequests },
    { listRequests: 4, statsRequests: 2 }
  )
  client.clear()
})
