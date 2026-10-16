import { request } from './client'
import { clearSession, getRefreshToken } from '@/lib/authSession'
import type { AuthSession, AuthUser } from '@/lib/authSession'

export interface CaptchaChallenge { id: string; image: string }

export function fetchCaptcha(): Promise<CaptchaChallenge> {
  return request('/auth/captcha')
}

export function sendCode(phone: string, purpose: 'register' | 'login', captchaId: string, captchaCode: string): Promise<{ sent: boolean }> {
  return request('/auth/codes', { method: 'POST', body: JSON.stringify({ phone, purpose, captcha_id: captchaId, captcha_code: captchaCode }) })
}

export function register(phone: string, password: string, code: string): Promise<AuthSession> {
  return request('/auth/register', { method: 'POST', body: JSON.stringify({ phone, password, code }) })
}

export function loginPassword(phone: string, password: string, captchaId = '', captchaCode = ''): Promise<AuthSession> {
  return request('/auth/login/password', { method: 'POST', body: JSON.stringify({ phone, password, captcha_id: captchaId, captcha_code: captchaCode }) })
}

export function loginCode(phone: string, code: string): Promise<AuthSession> {
  return request('/auth/login/code', { method: 'POST', body: JSON.stringify({ phone, code }) })
}

export function currentUser(): Promise<AuthUser> {
  return request('/auth/me')
}

export async function logout(): Promise<void> {
  const refreshToken = getRefreshToken()
  clearSession()
  if (!refreshToken) return
  await request('/auth/logout', { method: 'POST', body: JSON.stringify({ refresh_token: refreshToken }) })
}
