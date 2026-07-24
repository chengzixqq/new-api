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

import type { UsageLog } from '../data/schema'
import { parseLogOther, prepareUsageLogs } from './log-data.ts'

const baseLog: UsageLog = {
  id: 1,
  user_id: 1,
  created_at: 1,
  type: 2,
  content: '',
  username: '',
  token_name: '',
  model_name: 'gpt-test',
  quota: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  use_time: 0,
  is_stream: false,
  channel: 0,
  channel_name: '',
  token_id: 0,
  group: '',
  ip: '',
  other: '{"frt":120}',
  request_id: '',
  upstream_request_id: '',
}

test('preparing rows parses other exactly once per row and retains the result', () => {
  let parseCount = 0
  const rows = prepareUsageLogs([baseLog, { ...baseLog, id: 2 }], (other) => {
    parseCount += 1
    return parseLogOther(other)
  })

  assert.equal(parseCount, 2)
  assert.deepEqual(
    rows.map((row) => row.parsedOther),
    [{ frt: 120 }, { frt: 120 }]
  )
})

test('malformed other data is retained as an explicit null parse result', () => {
  const [row] = prepareUsageLogs([{ ...baseLog, other: '{invalid' }])
  assert.equal(row.parsedOther, null)
})
