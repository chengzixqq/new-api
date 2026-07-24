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

import { resolveLogGroupRatio } from './group-ratio.ts'

test('personal group pricing wins every other logged ratio', () => {
  assert.deepEqual(
    resolveLogGroupRatio({
      user_group_ratio_override: 0.8,
      group_ratio: 2,
      user_group_ratio: 0.5,
      group_ratio_source: 'model_group_ratio',
    }),
    { ratio: 0.8, kind: 'personal' }
  )
})

test('effective group ratio wins stale membership metadata', () => {
  assert.deepEqual(
    resolveLogGroupRatio({
      group_ratio: 2,
      user_group_ratio: 0.5,
      group_ratio_source: 'model_group_ratio',
    }),
    { ratio: 2, kind: 'group' }
  )
})

test('membership and legacy sources remain distinguishable', () => {
  assert.deepEqual(
    resolveLogGroupRatio({
      group_ratio: 0.6,
      group_ratio_source: 'group_group_ratio',
    }),
    { ratio: 0.6, kind: 'membership' }
  )
  assert.deepEqual(resolveLogGroupRatio({ user_group_ratio: 0.7 }), {
    ratio: 0.7,
    kind: 'membership',
  })
})
