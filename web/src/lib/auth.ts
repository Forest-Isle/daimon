const params = new URLSearchParams(location.search)
const storedToken = params.get('token') ?? ''

export function getToken(): string {
  return storedToken
}

export function authHeaders(): HeadersInit {
  const t = getToken()
  return t ? { Authorization: `Bearer ${t}` } : {}
}

export function wsTokenQuery(): string {
  const t = getToken()
  return t ? `?token=${encodeURIComponent(t)}` : ''
}
