import { request } from './client'
import type { AuthSession, AuthUser } from '@/lib/authSession'

export function sendCode(phone: string, purpose: 'register' | 'login'): Promise<{ sent: boolean }> {
  return request('/auth/codes', { method: 'POST', body: JSON.stringify({ phone, purpose }) })
}

export function register(phone: string, password: string, code: string): Promise<AuthSession> {
  return request('/auth/register', { method: 'POST', body: JSON.stringify({ phone, password, code }) })
}

export function loginPassword(phone: string, password: string): Promise<AuthSession> {
  return request('/auth/login/password', { method: 'POST', body: JSON.stringify({ phone, password }) })
}

export function loginCode(phone: string, code: string): Promise<AuthSession> {
  return request('/auth/login/code', { method: 'POST', body: JSON.stringify({ phone, code }) })
}

export function currentUser(): Promise<AuthUser> {
  return request('/auth/me')
}
