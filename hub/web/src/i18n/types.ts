export type SupportedLocale = 'en' | 'zh-CN'

export type LanguagePreference = 'auto' | 'en' | 'zh-CN'

export interface LocaleMessages {
  common: Record<string, string>
  auth: Record<string, string>
  hosts: Record<string, string>
  sessions: Record<string, string>
  terminal: Record<string, string>
  files: Record<string, string>
  help: Record<string, string>
  errors: Record<string, string>
}
