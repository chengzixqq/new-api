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

import {
  automaticGroupAlias,
  hasPublicPoolAliasConflict,
  modelHealthChannelLabel,
  modelHealthGroupLabel,
  modelsAllowedByProbeToken,
  selectableModelsForGroups,
  selectableChannelsForScope,
  syncTargetModels,
} from './target-options'
import type { ModelHealthAdminGroupOption } from './types'

const groups: ModelHealthAdminGroupOption[] = [
  {
    name: 'default',
    display_name: 'Default',
    models: ['gpt-4.1', 'shared-model'],
  },
  {
    name: 'premium',
    display_name: 'Premium',
    models: ['claude-sonnet', 'shared-model'],
  },
]

describe('model health target selection', () => {
  it('shows the group name instead of its description in selectors', () => {
    assert.equal(modelHealthGroupLabel(groups[0]), 'default')
  })

  it('uses the intersection when more than one observed group is selected', () => {
    assert.deepEqual(
      selectableModelsForGroups(groups, ['default', 'premium']),
      ['shared-model']
    )
    assert.deepEqual(selectableModelsForGroups(groups, []), [
      'claude-sonnet',
      'gpt-4.1',
      'shared-model',
    ])
  })

  it('preserves model aliases and required flags while changing selection', () => {
    assert.deepEqual(
      syncTargetModels(
        [
          {
            name: 'shared-model',
            public_alias: 'Shared',
            required: false,
          },
        ],
        ['shared-model', 'gpt-4.1']
      ),
      [
        { name: 'shared-model', public_alias: 'Shared', required: false },
        { name: 'gpt-4.1', public_alias: 'gpt-4.1', required: true },
      ]
    )
    assert.deepEqual(
      syncTargetModels(
        [
          {
            name: 'legacy-model',
            public_alias: '',
            required: true,
          },
        ],
        ['legacy-model']
      ),
      [{ name: 'legacy-model', public_alias: 'legacy-model', required: true }]
    )
  })

  it('builds public group aliases from group names in selection order', () => {
    assert.equal(
      automaticGroupAlias(groups, ['premium', 'default']),
      'premium / default'
    )
  })

  it('applies the selected probe token model restrictions', () => {
    assert.deepEqual(
      modelsAllowedByProbeToken(['gpt-4.1', 'shared-model'], {
        id: 1,
        name: 'health',
        status: 1,
        group: 'default',
        remain_quota: 100,
        unlimited_quota: false,
        expired_time: -1,
        model_limits_enabled: true,
        model_limits: ['shared-model'],
        available: true,
      }),
      ['shared-model']
    )
  })

  it('filters channel choices by the selected groups and models', () => {
    const channels = [
      {
        id: 84,
        name: 'CC-A',
        status: 1,
        groups: ['CC-MAX', 'premium'],
        models: ['claude-opus-4-8'],
        available: true,
      },
      {
        id: 58,
        name: 'CC-B',
        status: 0,
        groups: ['CC-MAX'],
        models: ['claude-sonnet-4-6'],
        available: false,
      },
    ]

    assert.deepEqual(
      selectableChannelsForScope(channels, ['CC-MAX'], ['claude-opus-4-8']),
      [channels[0]]
    )
    assert.equal(
      modelHealthChannelLabel(channels[1], 'unavailable'),
      'CC-B (#58, unavailable)'
    )
  })

  it('rejects duplicate pool aliases only within the same public group', () => {
    const target = {
      id: 1,
      publish_pool_detail: true,
      public_group_alias: 'CC-MAX',
      public_pool_alias: 'CCMAX-A池',
    }

    assert.equal(
      hasPublicPoolAliasConflict([target], undefined, 'CC-MAX', 'CCMAX-A池'),
      true
    )
    assert.equal(
      hasPublicPoolAliasConflict([target], 1, 'CC-MAX', 'CCMAX-A池'),
      false
    )
    assert.equal(
      hasPublicPoolAliasConflict([target], undefined, 'Other', 'CCMAX-A池'),
      false
    )
  })
})
