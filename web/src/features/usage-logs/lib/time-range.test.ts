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
  buildTimeRangeParams,
  canonicalizeUsageLogTimeRange,
} from './time-range.ts'

test('missing default timestamps are canonicalized once and survive pagination', () => {
  const now = new Date(2026, 6, 24, 17, 30, 45, 123)
  const canonical = canonicalizeUsageLogTimeRange({}, now)
  const pageOne = { ...canonical, p: 1, page_size: 50 }
  const pageTwo = { ...canonical, p: 2, page_size: 50 }

  assert.equal(canonical.startTime, new Date(2026, 6, 24).getTime())
  assert.equal(canonical.endTime, now.getTime() + 3600 * 1000)
  assert.equal(pageOne.startTime, pageTwo.startTime)
  assert.equal(pageOne.endTime, pageTwo.endTime)
})

test('explicit and partial timestamps remain canonical URL state', () => {
  const now = new Date(2026, 6, 24, 17, 30)
  assert.deepEqual(
    canonicalizeUsageLogTimeRange({ startTime: 1000, endTime: 2000 }, now),
    { startTime: 1000, endTime: 2000 }
  )
  const partial = canonicalizeUsageLogTimeRange({ startTime: 1000 }, now)
  assert.equal(partial.startTime, 1000)
  assert.equal(partial.endTime, now.getTime() + 3600 * 1000)

  assert.deepEqual(
    buildTimeRangeParams({ startTime: 0, endTime: 2000 }, false, now),
    { start_timestamp: 0, end_timestamp: 2 }
  )
})
