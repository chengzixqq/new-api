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
import { SettingsPage } from '../components/settings-page'
import type { ContentSettings } from '../types'
import {
  CONTENT_DEFAULT_SECTION,
  getContentSectionContent,
  getContentSectionMeta,
} from './section-registry.tsx'

const defaultContentSettings: ContentSettings = {
  'console_setting.api_info': '[]',
  'console_setting.announcements': '[]',
  'console_setting.faq': '[]',
  'console_setting.api_info_enabled': true,
  'console_setting.announcements_enabled': true,
  'console_setting.faq_enabled': true,
  'model_health_setting.enabled': false,
  'model_health_setting.pool_details_enabled': false,
  'model_health_setting.multi_sample_enabled': false,
  'model_health_setting.default_interval_seconds': 300,
  'model_health_setting.default_timeout_seconds': 45,
  'model_health_setting.default_sampling_mode': 'confirm_on_failure',
  'model_health_setting.default_samples_per_run': 3,
  'model_health_setting.default_minimum_successes': 2,
  'model_health_setting.default_sample_spacing_seconds': 3,
  'model_health_setting.concurrency': 4,
  'model_health_setting.retention_days': 30,
  'model_health_setting.healthy_threshold': 99,
  'model_health_setting.fluctuating_threshold': 95,
  'model_health_setting.passive_min_samples': 30,
  'model_health_setting.active_min_samples': 3,
  'model_health_setting.public_groups': '[]',
  'model_health_setting.public_models': '[]',
  'perf_metrics_setting.enabled': true,
  'perf_metrics_setting.flush_interval': 5,
  'perf_metrics_setting.bucket_time': '5min',
  'perf_metrics_setting.retention_days': 30,
  DataExportEnabled: false,
  DataExportDefaultTime: 'hour',
  DataExportInterval: 5,
  Chats: '[]',
  DrawingEnabled: false,
  MjNotifyEnabled: false,
  MjAccountFilterEnabled: false,
  MjForwardUrlEnabled: false,
  MjModeClearEnabled: false,
  MjActionCheckSuccessEnabled: false,
}

export function ContentSettings() {
  return (
    <SettingsPage
      routePath='/_authenticated/system-settings/content/$section'
      defaultSettings={defaultContentSettings}
      defaultSection={CONTENT_DEFAULT_SECTION}
      getSectionContent={getContentSectionContent}
      getSectionMeta={getContentSectionMeta}
      loadingMessage='Loading content settings...'
    />
  )
}
