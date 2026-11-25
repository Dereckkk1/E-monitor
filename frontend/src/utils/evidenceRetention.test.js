// evidenceRetention.js — texto único da retenção local de evidência.
import { describe, test, expect } from 'vitest'
import {
  EVIDENCE_RETENTION_DAYS, EVIDENCE_EXPIRED_TITLE, EVIDENCE_EXPIRED_MESSAGE, EVIDENCE_EXPIRED_SHORT,
} from './evidenceRetention.js'

describe('evidenceRetention', () => {
  test('a retenção é de 30 dias (igual ao env do backend)', () => {
    expect(EVIDENCE_RETENTION_DAYS).toBe(30)
  })

  test('as mensagens citam o mesmo número de dias da constante', () => {
    expect(EVIDENCE_EXPIRED_MESSAGE).toContain(`${EVIDENCE_RETENTION_DAYS} dias`)
    expect(EVIDENCE_EXPIRED_SHORT).toContain(`${EVIDENCE_RETENTION_DAYS} dias`)
  })

  test('a mensagem tranquiliza: a veiculação segue válida, só o áudio some', () => {
    expect(EVIDENCE_EXPIRED_MESSAGE).toMatch(/veiculação continua registrada e válida/)
    expect(EVIDENCE_EXPIRED_TITLE).toBe('Áudio expirado pela retenção')
  })
})
