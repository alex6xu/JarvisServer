export type PublicConfigFetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>

export async function loadRegistrationEnabled(fetcher: PublicConfigFetcher = fetch): Promise<boolean> {
  try {
    const response = await fetcher('/v1/auth/config')
    if (!response.ok) return false
    const data = await response.json() as { registration_enabled?: unknown }
    return data.registration_enabled === true
  } catch {
    return false
  }
}
