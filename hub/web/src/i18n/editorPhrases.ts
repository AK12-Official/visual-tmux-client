import type { SupportedLocale } from './types'

export const cmPhrases: Record<SupportedLocale, Record<string, string>> = {
  en: {},
  'zh-CN': {
    Find: '查找',
    Replace: '替换',
    next: '下一个',
    previous: '上一个',
    all: '全部',
    'match case': '区分大小写',
    by_word: '全字匹配',
    'by word': '全字匹配',
    regexp: '正则表达式',
    replace: '替换',
    'replace all': '全部替换',
    close: '关闭',
    'Go to line': '跳转到行',
    go: '跳转',
    'current match': '当前匹配',
    'on line': '在行',
  },
}
