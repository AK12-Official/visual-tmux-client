// Browser runtime configuration store and schema validator.
// Fetched unauthenticated from GET /api/client-config before application bootstrap.

export interface ReconnectConfig {
  initial_delay: number
  max_delay: number
}

export interface TerminalConfig {
  scrollback: number
  font_size: number
  min_font_size: number
  max_font_size: number
}

export interface NotificationsConfig {
  max_toasts: number
  error_lifetime: number
  warning_lifetime: number
  info_lifetime: number
}

export interface WebConfig {
  session_poll_interval: number
  activity_decay: number
  activity_throttle: number
  resize_debounce: number
  reconnect: ReconnectConfig
  terminal: TerminalConfig
  notifications: NotificationsConfig
}

export interface ClientConfig {
  version: number
  web: WebConfig
}

function isPositiveInteger(n: unknown, min = 1): n is number {
  return typeof n === 'number' && Number.isInteger(n) && n >= min
}

function isNonNegativeInteger(n: unknown): n is number {
  return typeof n === 'number' && Number.isInteger(n) && n >= 0
}

export function validateClientConfig(raw: unknown): ClientConfig {
  if (!raw || typeof raw !== 'object') {
    throw new Error('Invalid client configuration: payload must be a JSON object')
  }

  const payload = raw as Partial<ClientConfig>
  if (payload.version !== 1) {
    throw new Error(`Invalid client configuration: expected version 1, got ${String(payload.version)}`)
  }

  if (!payload.web || typeof payload.web !== 'object') {
    throw new Error('Invalid client configuration: missing or invalid "web" object')
  }

  const web = payload.web as Partial<WebConfig>

  if (!isPositiveInteger(web.session_poll_interval, 100)) {
    throw new Error('Invalid client configuration: web.session_poll_interval must be an integer >= 100 ms')
  }

  if (!isPositiveInteger(web.activity_decay, 100)) {
    throw new Error('Invalid client configuration: web.activity_decay must be an integer >= 100 ms')
  }

  if (!isNonNegativeInteger(web.activity_throttle)) {
    throw new Error('Invalid client configuration: web.activity_throttle must be an integer >= 0 ms')
  }

  if (!isNonNegativeInteger(web.resize_debounce)) {
    throw new Error('Invalid client configuration: web.resize_debounce must be an integer >= 0 ms')
  }

  // Reconnect
  if (!web.reconnect || typeof web.reconnect !== 'object') {
    throw new Error('Invalid client configuration: missing or invalid "web.reconnect" object')
  }
  const reconnect = web.reconnect as Partial<ReconnectConfig>
  if (!isPositiveInteger(reconnect.initial_delay, 50)) {
    throw new Error('Invalid client configuration: web.reconnect.initial_delay must be an integer >= 50 ms')
  }
  if (!isPositiveInteger(reconnect.max_delay, reconnect.initial_delay)) {
    throw new Error('Invalid client configuration: web.reconnect.max_delay must be an integer >= initial_delay')
  }

  // Terminal
  if (!web.terminal || typeof web.terminal !== 'object') {
    throw new Error('Invalid client configuration: missing or invalid "web.terminal" object')
  }
  const terminal = web.terminal as Partial<TerminalConfig>
  if (!isNonNegativeInteger(terminal.scrollback)) {
    throw new Error('Invalid client configuration: web.terminal.scrollback must be an integer >= 0')
  }
  if (!isPositiveInteger(terminal.min_font_size, 6)) {
    throw new Error('Invalid client configuration: web.terminal.min_font_size must be an integer >= 6')
  }
  if (!isPositiveInteger(terminal.max_font_size, terminal.min_font_size) || terminal.max_font_size > 72) {
    throw new Error('Invalid client configuration: web.terminal.max_font_size must be between min_font_size and 72')
  }
  if (
    !isPositiveInteger(terminal.font_size, terminal.min_font_size) ||
    terminal.font_size > terminal.max_font_size
  ) {
    throw new Error('Invalid client configuration: web.terminal.font_size must be between min_font_size and max_font_size')
  }

  // Notifications
  if (!web.notifications || typeof web.notifications !== 'object') {
    throw new Error('Invalid client configuration: missing or invalid "web.notifications" object')
  }
  const notifs = web.notifications as Partial<NotificationsConfig>
  if (!isPositiveInteger(notifs.max_toasts, 1)) {
    throw new Error('Invalid client configuration: web.notifications.max_toasts must be an integer >= 1')
  }
  if (!isPositiveInteger(notifs.error_lifetime, 100)) {
    throw new Error('Invalid client configuration: web.notifications.error_lifetime must be an integer >= 100 ms')
  }
  if (!isPositiveInteger(notifs.warning_lifetime, 100)) {
    throw new Error('Invalid client configuration: web.notifications.warning_lifetime must be an integer >= 100 ms')
  }
  if (!isPositiveInteger(notifs.info_lifetime, 100)) {
    throw new Error('Invalid client configuration: web.notifications.info_lifetime must be an integer >= 100 ms')
  }

  return {
    version: payload.version,
    web: {
      session_poll_interval: web.session_poll_interval,
      activity_decay: web.activity_decay,
      activity_throttle: web.activity_throttle,
      resize_debounce: web.resize_debounce,
      reconnect: {
        initial_delay: reconnect.initial_delay,
        max_delay: reconnect.max_delay,
      },
      terminal: {
        scrollback: terminal.scrollback,
        font_size: terminal.font_size,
        min_font_size: terminal.min_font_size,
        max_font_size: terminal.max_font_size,
      },
      notifications: {
        max_toasts: notifs.max_toasts,
        error_lifetime: notifs.error_lifetime,
        warning_lifetime: notifs.warning_lifetime,
        info_lifetime: notifs.info_lifetime,
      },
    },
  }
}

function deepFreeze<T extends object>(obj: T): Readonly<T> {
  Object.freeze(obj)
  for (const key of Object.keys(obj)) {
    const val = (obj as Record<string, unknown>)[key]
    if (val && typeof val === 'object' && !Object.isFrozen(val)) {
      deepFreeze(val as object)
    }
  }
  return obj
}

let activeConfig: ClientConfig | null = null

export function setConfig(cfg: ClientConfig): void {
  activeConfig = deepFreeze({
    version: cfg.version,
    web: {
      session_poll_interval: cfg.web.session_poll_interval,
      activity_decay: cfg.web.activity_decay,
      activity_throttle: cfg.web.activity_throttle,
      resize_debounce: cfg.web.resize_debounce,
      reconnect: { ...cfg.web.reconnect },
      terminal: { ...cfg.web.terminal },
      notifications: { ...cfg.web.notifications },
    },
  })
}

export function getConfig(): ClientConfig {
  if (!activeConfig) {
    throw new Error('Client configuration has not been initialized.')
  }
  return activeConfig
}

export function resetConfigForTest(): void {
  activeConfig = null
}

export function resolveFontSize(
  storedValue: string | null,
  termCfg: TerminalConfig = getConfig().web.terminal,
): number {
  if (storedValue === null || storedValue === '') {
    return termCfg.font_size
  }
  const parsed = Number.parseInt(storedValue, 10)
  if (Number.isNaN(parsed)) {
    return termCfg.font_size
  }
  return Math.min(termCfg.max_font_size, Math.max(termCfg.min_font_size, parsed))
}

export async function fetchClientConfig(
  fetchFn: typeof fetch = (url, init) => window.fetch(url, init),
): Promise<ClientConfig> {
  const res = await fetchFn('/api/client-config', { cache: 'no-store' })
  if (!res.ok) {
    throw new Error(`Failed to load client configuration (HTTP ${res.status})`)
  }
  const raw = (await res.json()) as unknown
  const valid = validateClientConfig(raw)
  setConfig(valid)
  return valid
}
