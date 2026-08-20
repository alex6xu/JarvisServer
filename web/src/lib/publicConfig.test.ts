import { describe, expect, it } from 'vitest'
import { loadRegistrationEnabled, type PublicConfigFetcher } from './publicConfig'

function response(ok: boolean, body: unknown): Response {
  return { ok, json: async () => body } as Response
}

describe('loadRegistrationEnabled', () => {
  it('enables registration only for an explicit true response', async () => {
    const enabled: PublicConfigFetcher = async () => response(true, { registration_enabled: true })
    const disabled: PublicConfigFetcher = async () => response(true, { registration_enabled: false })

    await expect(loadRegistrationEnabled(enabled)).resolves.toBe(true)
    await expect(loadRegistrationEnabled(disabled)).resolves.toBe(false)
  })

  it('fails closed on malformed responses and request failures', async () => {
    const malformed: PublicConfigFetcher = async () => response(true, {})
    const rejected: PublicConfigFetcher = async () => { throw new Error('offline') }

    await expect(loadRegistrationEnabled(malformed)).resolves.toBe(false)
    await expect(loadRegistrationEnabled(rejected)).resolves.toBe(false)
  })
})
