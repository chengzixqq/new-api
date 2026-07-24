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

import { channelSchema } from '../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from './channel-form'

test('serializes explicit transport settings and mirrors HTTP/1 for rollback', () => {
  const http1Payload = transformFormDataToUpdatePayload(
    {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      upstream_http_mode: 'http1',
      http2_connection_pool_size: 16,
      http1_body_threshold_kib: 256,
    },
    57
  )
  const http1Settings = JSON.parse(String(http1Payload.settings))

  assert.equal(http1Settings.upstream_http_mode, 'http1')
  assert.equal(http1Settings.http2_connection_pool_size, 16)
  assert.equal(http1Settings.http1_body_threshold_kib, 256)
  assert.equal(http1Settings.force_http1, true)

  const hybridPayload = transformFormDataToUpdatePayload(
    {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      upstream_http_mode: 'hybrid',
      http2_connection_pool_size: 32,
      http1_body_threshold_kib: 64,
    },
    57
  )
  const hybridSettings = JSON.parse(String(hybridPayload.settings))

  assert.equal(hybridSettings.upstream_http_mode, 'hybrid')
  assert.equal(hybridSettings.force_http1, false)

  const roundTripped = transformChannelToFormDefaults(
    channelSchema.parse({
      id: 57,
      type: 1,
      key: '',
      status: 1,
      name: 'hybrid',
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      settings: hybridPayload.settings,
    })
  )

  assert.equal(roundTripped.upstream_http_mode, 'hybrid')
  assert.equal(roundTripped.http2_connection_pool_size, 32)
  assert.equal(roundTripped.http1_body_threshold_kib, 64)
  assert.equal(roundTripped.force_http1, false)
})

test('inherited transport settings remove overrides and stale legacy forcing', () => {
  const payload = transformFormDataToUpdatePayload(
    {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      settings: JSON.stringify({
        upstream_http_mode: 'http1',
        http2_connection_pool_size: 64,
        http1_body_threshold_kib: 1024,
        force_http1: true,
        preserved_setting: 'kept',
      }),
      upstream_http_mode: undefined,
      http2_connection_pool_size: undefined,
      http1_body_threshold_kib: undefined,
    },
    57
  )
  const settings = JSON.parse(String(payload.settings))

  assert.equal(settings.upstream_http_mode, undefined)
  assert.equal(settings.http2_connection_pool_size, undefined)
  assert.equal(settings.http1_body_threshold_kib, undefined)
  assert.equal(settings.force_http1, false)
  assert.equal(settings.preserved_setting, 'kept')
})

test('loads the legacy force_http1 flag as explicit HTTP/1 mode', () => {
  const channel = channelSchema.parse({
    id: 57,
    type: 1,
    key: '',
    status: 1,
    name: 'legacy',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    settings: JSON.stringify({ force_http1: true }),
  })

  const values = transformChannelToFormDefaults(channel)

  assert.equal(values.upstream_http_mode, 'http1')
  assert.equal(values.force_http1, true)
})

test('explicit transport mode wins over a stale legacy HTTP/1 flag', () => {
  const channel = channelSchema.parse({
    id: 57,
    type: 1,
    key: '',
    status: 1,
    name: 'explicit-auto',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    settings: JSON.stringify({
      upstream_http_mode: 'auto',
      force_http1: true,
    }),
  })

  const values = transformChannelToFormDefaults(channel)
  assert.equal(values.upstream_http_mode, 'auto')
  assert.equal(values.force_http1, false)

  const payload = transformFormDataToUpdatePayload(values, channel.id)
  const settings = JSON.parse(String(payload.settings))
  assert.equal(settings.upstream_http_mode, 'auto')
  assert.equal(settings.force_http1, false)
})

test('rejects transport settings outside supported boundaries', () => {
  const validFormValues = {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'channel',
    models: 'gpt-5',
  }

  assert.equal(
    channelFormSchema.safeParse({
      ...validFormValues,
      http2_connection_pool_size: 65,
    }).success,
    false
  )
  assert.equal(
    channelFormSchema.safeParse({
      ...validFormValues,
      http1_body_threshold_kib: 63,
    }).success,
    false
  )
  assert.equal(
    channelFormSchema.safeParse({
      ...validFormValues,
      http2_connection_pool_size: 64,
      http1_body_threshold_kib: 65536,
    }).success,
    true
  )
})

test('preserves explicit empty-output guard overrides and zero retries', () => {
  const payload = transformFormDataToUpdatePayload(
    {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      responses_empty_output_guard: 'on',
      max_retries: 0,
    },
    1
  )
  const settings = JSON.parse(String(payload.settings))

  assert.equal(settings.responses_empty_output_guard, true)
  assert.equal(settings.max_retries, 0)

  const roundTripped = transformChannelToFormDefaults(
    channelSchema.parse({
      id: 1,
      type: 1,
      key: '',
      status: 1,
      name: 'responses-guard',
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      settings: payload.settings,
    })
  )

  assert.equal(roundTripped.responses_empty_output_guard, 'on')
  assert.equal(roundTripped.max_retries, 0)
})

test('removes inherited empty-output settings while preserving explicit off', () => {
  const inheritedPayload = transformFormDataToUpdatePayload(
    {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      settings: JSON.stringify({
        responses_empty_output_guard: true,
        max_retries: 5,
        preserved_setting: 'kept',
      }),
      responses_empty_output_guard: 'inherit',
      max_retries: undefined,
    },
    1
  )
  const inheritedSettings = JSON.parse(String(inheritedPayload.settings))

  assert.equal(inheritedSettings.responses_empty_output_guard, undefined)
  assert.equal(inheritedSettings.max_retries, undefined)
  assert.equal(inheritedSettings.preserved_setting, 'kept')

  const disabledPayload = transformFormDataToUpdatePayload(
    {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      responses_empty_output_guard: 'off',
    },
    1
  )
  const disabledSettings = JSON.parse(String(disabledPayload.settings))
  assert.equal(disabledSettings.responses_empty_output_guard, false)
})
