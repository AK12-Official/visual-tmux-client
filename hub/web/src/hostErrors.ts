// What the hub says about a remote connection that failed because of the
// configured proxy, in words a person can act on.
//
// The hub reports every proxy failure as `code: proxy host:port` -- for example
// `proxy_unreachable: proxy 127.0.0.1:7890`. Four codes, one per thing that can
// go wrong, so that a user who knows nothing about proxies can still tell
// whether to look at the proxy app or at the host they were connecting to.

import { t } from './i18n/index'

export const PROXY_CODES = [
  'proxy_unreachable',
  'proxy_auth_required',
  'proxy_refused',
  'proxy_target_unreachable',
] as const

export type ProxyCode = (typeof PROXY_CODES)[number]

export interface ProxyFailure {
  code: ProxyCode
  /** The configured proxy address, when the hub named it. */
  proxy: string
}

/** parseProxyFailure reads a proxy failure out of a hub message, or returns null. */
export function parseProxyFailure(message: string): ProxyFailure | null {
  if (typeof message !== 'string') return null
  for (const code of PROXY_CODES) {
    if (!message.includes(code)) continue
    const match = message.match(/:\s*proxy\s+(\S+)/)
    return { code, proxy: match ? match[1] : '' }
  }
  return null
}

/** isProxyFailure reports whether a hub message is about the configured proxy. */
export function isProxyFailure(message: string): boolean {
  return parseProxyFailure(message) !== null
}

/**
 * proxyFailureText explains a proxy failure in the active language.
 *
 * `fallbackProxy` names the proxy when the message carries only the code, as a
 * host status does. Returns null when the message is not a proxy failure, so a
 * caller can go on to its own handling.
 */
export function proxyFailureText(message: string, fallbackProxy = ''): string | null {
  const failure = parseProxyFailure(message)
  if (!failure) return null
  return t(`errors.${failure.code}`, { proxy: failure.proxy || fallbackProxy })
}
