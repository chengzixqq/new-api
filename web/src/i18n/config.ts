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
import i18n, { type BackendModule } from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import {
  convertDetectedLanguage,
  type InterfaceLanguageCode,
} from './languages'

type LocaleResource = Record<string, string>

const localeLoaders: Record<
  InterfaceLanguageCode,
  () => Promise<LocaleResource>
> = {
  en: async () => (await import('./locales/en.json')).default.translation,
  zhCN: async () => (await import('./locales/zh.json')).default.translation,
  fr: async () => (await import('./locales/fr.json')).default.translation,
  ru: async () => (await import('./locales/ru.json')).default.translation,
  ja: async () => (await import('./locales/ja.json')).default.translation,
  vi: async () => (await import('./locales/vi.json')).default.translation,
  zhTW: async () => (await import('./locales/zh-TW.json')).default.translation,
}

const localeBackend: BackendModule = {
  type: 'backend',
  init: () => undefined,
  async read(language, namespace, callback) {
    if (namespace !== 'translation') {
      callback(null, {})
      return
    }

    const loader = localeLoaders[language as InterfaceLanguageCode]
    if (!loader) {
      callback(new Error(`Unsupported interface language: ${language}`), false)
      return
    }

    let resource: LocaleResource
    try {
      resource = await loader()
    } catch (error: unknown) {
      callback(
        error instanceof Error
          ? error
          : new Error(`Failed to load interface language: ${language}`),
        false
      )
      return
    }

    callback(null, resource)
  },
}

export const i18nReady = i18n
  .use(localeBackend)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    fallbackLng: 'en',
    supportedLngs: Object.keys(localeLoaders),
    load: 'currentOnly',
    nsSeparator: false, // Allow literal colons in keys (e.g., URLs, labels)
    debug: import.meta.env.DEV,
    interpolation: {
      escapeValue: false, // not needed for react as it escapes by default
    },
    detection: {
      order: ['localStorage', 'navigator'],
      caches: ['localStorage'],
      // Browsers report `zh-CN`/`zh-TW`/`zh`; map them onto our `zhCN`/`zhTW`
      // codes (non-Chinese codes pass through for normal supportedLngs matching).
      convertDetectedLanguage,
    },
  })
  .then(() => undefined)

export default i18n
