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
  buildUsageLogApiPath,
  buildUsageLogQueryParams,
  createUsageLogsFilterSourceKey,
  createUsageLogsQueryKey,
  createUsageLogStatsQueryKey,
  normalizeUsageLogPage,
  normalizeUsageLogPageSize,
} from './query-state.ts'

test('query serialization keeps explicit zero values and omits empty fields', () => {
  assert.equal(
    buildUsageLogQueryParams({
      type: 0,
      model_name: '',
      channel: undefined,
    }).toString(),
    'type=0'
  )
})

test('admin list path keeps the canonical trailing slash without changing stat paths', () => {
  assert.equal(buildUsageLogApiPath('/api/log', true, 'list'), '/api/log/')
  assert.equal(buildUsageLogApiPath('/api/log/', true, 'stat'), '/api/log/stat')
  assert.equal(buildUsageLogApiPath('/api/log', false, 'list'), '/api/log/self')
  assert.equal(
    buildUsageLogApiPath('/api/log/', false, 'stat'),
    '/api/log/self/stat'
  )
})

test('list query keys are stable across object insertion order', () => {
  assert.deepEqual(
    createUsageLogsQueryKey('common', true, {
      page_size: 50,
      p: 2,
      model_name: 'gpt-test',
    }),
    createUsageLogsQueryKey('common', true, {
      model_name: 'gpt-test',
      p: 2,
      page_size: 50,
    })
  )
})

test('stats identity excludes list pagination while retaining filters', () => {
  const firstPage = createUsageLogStatsQueryKey(true, {
    p: 1,
    page_size: 20,
    model_name: 'gpt-test',
  })
  const laterPage = createUsageLogStatsQueryKey(true, {
    p: 9,
    page_size: 100,
    model_name: 'gpt-test',
  })
  const otherModel = createUsageLogStatsQueryKey(true, {
    p: 9,
    page_size: 100,
    model_name: 'gpt-other',
  })

  assert.deepEqual(firstPage, laterPage)
  assert.notDeepEqual(firstPage, otherModel)
})

test('filter draft identity follows URL filters and ignores pagination', () => {
  const base = {
    page: 1,
    pageSize: 20,
    model: 'gpt-test',
    startTime: 10,
    endTime: 20,
  }

  assert.equal(
    createUsageLogsFilterSourceKey(base),
    createUsageLogsFilterSourceKey({ ...base, page: 8, pageSize: 100 })
  )
  assert.notEqual(
    createUsageLogsFilterSourceKey(base),
    createUsageLogsFilterSourceKey({ ...base, model: 'gpt-other' })
  )
})

test('pagination values are normalized to backend bounds', () => {
  assert.equal(normalizeUsageLogPage(0), 1)
  assert.equal(normalizeUsageLogPage(3.9), 3)
  assert.equal(normalizeUsageLogPage(Number.POSITIVE_INFINITY), 1)
  assert.equal(normalizeUsageLogPageSize(undefined), undefined)
  assert.equal(normalizeUsageLogPageSize(0), 1)
  assert.equal(normalizeUsageLogPageSize(50.9), 50)
  assert.equal(normalizeUsageLogPageSize(1000), 100)
})

test('admin and self scopes never share list or stats query identities', () => {
  const params = { p: 1, page_size: 50 }
  assert.notDeepEqual(
    createUsageLogsQueryKey('common', true, params),
    createUsageLogsQueryKey('common', false, params)
  )
  assert.notDeepEqual(
    createUsageLogStatsQueryKey(true, params),
    createUsageLogStatsQueryKey(false, params)
  )
})
