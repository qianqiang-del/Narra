const STORAGE_KEY = 'narra-auth'

export interface AuthUser {
  id: number
  phone: string
}

export interface AuthSession {
  token: string
  expires_in: number
  user: AuthUser
}

interface StoredSession extends AuthSession {
  expires_at: number
}

export function getToken(): string | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<StoredSession>
    if (typeof parsed.expires_at !== 'number' || parsed.expires_at <= Date.now()) {
      clearSession()
      return null
    }
    return typeof parsed.token === 'string' && parsed.token ? parsed.token : null
  } catch {
    return null
  }
}

export function saveSession(session: AuthSession): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...session, expires_at: Date.now() + session.expires_in * 1000 }))
}

export function clearSession(): void {
  localStorage.removeItem(STORAGE_KEY)
}
