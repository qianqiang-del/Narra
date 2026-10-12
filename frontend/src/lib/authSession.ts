const STORAGE_KEY = 'narra-auth'

export interface AuthUser {
  id: number
  phone: string
}

export interface AuthSession {
  token: string
  expires_in: number
  refresh_token: string
  refresh_expires_in: number
  user: AuthUser
}

interface StoredSession extends AuthSession {
  expires_at: number
  refresh_expires_at: number
}

function readSession(): StoredSession | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<StoredSession>
    if (typeof parsed.refresh_expires_at !== 'number' || parsed.refresh_expires_at <= Date.now()
      || typeof parsed.refresh_token !== 'string' || !parsed.refresh_token) {
      clearSession()
      return null
    }
    return parsed as StoredSession
  } catch {
    return null
  }
}

export function getToken(): string | null {
  const session = readSession()
  return session && session.expires_at > Date.now() + 5000 ? session.token : null
}

export function getRefreshToken(): string | null {
  return readSession()?.refresh_token ?? null
}

export function saveSession(session: AuthSession): void {
  const now = Date.now()
  localStorage.setItem(STORAGE_KEY, JSON.stringify({
    ...session,
    expires_at: now + session.expires_in * 1000,
    refresh_expires_at: now + session.refresh_expires_in * 1000,
  }))
}

export function clearSession(): void {
  localStorage.removeItem(STORAGE_KEY)
}
