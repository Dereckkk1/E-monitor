import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { timeAgo, fmtDateTime, initialOf, authorLabel, isImage } from './utils'
import { devPriorityRank, STATUS_ORDER, STATUS, BOARD_COLUMNS, HUB_PORTAL_URL } from './constants'
import { StatusPill, TypePill, ReqPriorityPill, DevPriorityPill, EffortPill, OriginPill } from './SuggestionPills'
import EmptyState from './EmptyState'
import FilaNoHub from './FilaNoHub'
import { CardsSkeleton, TableSkeleton } from './Skeletons'
import { TypeIcon, IconPlus, IconList, IconBoard, IconSearch, IconInbox } from './icons'

// "agora" = 10/03/2026 12:00 UTC; só o Date é falso
beforeEach(() => { vi.useFakeTimers({ toFake: ['Date'], now: new Date('2026-03-10T12:00:00Z') }) })
afterEach(() => { vi.useRealTimers() })

const atras = (ms) => new Date(Date.now() - ms).toISOString()
const MIN = 60_000, H = 60 * MIN, D = 24 * H

describe('timeAgo', () => {
  test('vazio ou inválido devolve texto vazio', () => {
    expect(timeAgo(null)).toBe('')
    expect(timeAgo('')).toBe('')
    expect(timeAgo('não é data')).toBe('')
  })

  test.each([
    [10_000, 'agora'],
    [44_000, 'agora'],
    [5 * MIN, '5 min atrás'],
    [59 * MIN, '59 min atrás'],
    [3 * H, '3 h atrás'],
    [2 * D, '2 d atrás'],
    [29 * D, '29 d atrás'],
    [65 * D, '2 m atrás'],
    [400 * D, '1 a atrás'],
  ])('há %i ms → "%s"', (ms, esperado) => {
    expect(timeAgo(atras(ms))).toBe(esperado)
  })
})

describe('fmtDateTime / initialOf / authorLabel / isImage', () => {
  test('data e hora em pt-BR no fuso de São Paulo', () => {
    expect(fmtDateTime('2026-03-10T15:30:00Z')).toMatch(/10\/03\/2026.*12:30/)
    expect(fmtDateTime(null)).toBe('')
    expect(fmtDateTime('xx')).toBe('')
  })

  test('inicial em maiúscula, com "?" quando não há nome', () => {
    expect(initialOf('  maria teste')).toBe('M')
    expect(initialOf('')).toBe('?')
    expect(initialOf(undefined)).toBe('?')
  })

  test('autor: nome, senão e-mail, senão "Autor"', () => {
    expect(authorLabel({ created_by_name: 'Autor Teste', created_by_email: 'a@example.com' })).toBe('Autor Teste')
    expect(authorLabel({ created_by_email: 'a@example.com' })).toBe('a@example.com')
    expect(authorLabel({})).toBe('Autor')
    expect(authorLabel(null)).toBe('Autor')
  })

  test('isImage: só arquivo com content-type image/*; link e PDF não', () => {
    expect(isImage({ kind: 'arquivo', content_type: 'image/png' })).toBe(true)
    expect(isImage({ content_type: 'image/jpeg' })).toBe(true)          // kind ausente = arquivo
    expect(isImage({ kind: 'link', content_type: 'image/png' })).toBe(false)
    expect(isImage({ kind: 'arquivo', content_type: 'application/pdf' })).toBe(false)
    expect(isImage(undefined)).toBe(false)
  })
})

describe('constants', () => {
  test('prioridade do dev: urgente é a mais alta; desconhecida/vazia vai pro fim', () => {
    expect(devPriorityRank('urgente')).toBeLessThan(devPriorityRank('alta'))
    expect(devPriorityRank('alta')).toBeLessThan(devPriorityRank('baixa'))
    expect(devPriorityRank(null)).toBe(99)
    expect(devPriorityRank('qualquer')).toBe(99)
  })

  test('todo status da ordem tem rótulo e o board mostra só os ativos', () => {
    for (const st of STATUS_ORDER) expect(STATUS[st].label).toBeTruthy()
    expect(BOARD_COLUMNS).not.toContain('recusada')
    expect(BOARD_COLUMNS).not.toContain('retirada')
  })

  test('URL do portal do hub não termina em barra', () => {
    expect(HUB_PORTAL_URL.endsWith('/')).toBe(false)
  })
})

describe('pílulas', () => {
  test('status mostra o rótulo e a dica; valor desconhecido não renderiza', () => {
    const { container, rerender } = render(<StatusPill value="aceita" />)
    expect(screen.getByText('Aceita')).toBeTruthy()
    expect(container.querySelector('.sug-pill').getAttribute('title')).toBe('No backlog, vai ser feita')
    rerender(<StatusPill value="inexistente" />)
    expect(container.firstChild).toBeNull()
  })

  test('tamanho "sm" adiciona a classe compacta', () => {
    const { container } = render(<TypePill value="bug" size="sm" />)
    expect(screen.getByText('Bug')).toBeTruthy()
    expect(container.querySelector('.sug-pill--sm')).not.toBeNull()
  })

  test('prioridade pedida, prioridade do dev, esforço e origem', () => {
    const { container } = render(
      <>
        <ReqPriorityPill value="alta" />
        <DevPriorityPill value="urgente" />
        <EffortPill value="G" />
        <OriginPill value="hub" />
        <OriginPill value="outra-coisa" />
      </>,
    )
    expect(screen.getByText('Alta')).toBeTruthy()
    expect(screen.getByText('Urgente')).toBeTruthy()
    expect(screen.getByText('Grande')).toBeTruthy()
    expect(screen.getByText('via Hub').getAttribute('title')).toBe('Veio de outra plataforma')
    expect(container.querySelectorAll('.sug-pill').length).toBe(4)  // origem desconhecida não renderiza
  })
})

describe('EmptyState, FilaNoHub, skeletons e ícones', () => {
  test('EmptyState: título, texto e CTA que dispara o callback', () => {
    const onCta = vi.fn()
    render(<EmptyState title="Nada aqui" text="Sem itens." ctaLabel="Criar" onCta={onCta} />)
    expect(screen.getByRole('heading', { name: 'Nada aqui' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Criar/ }))
    expect(onCta).toHaveBeenCalledTimes(1)
  })

  test('EmptyState sem ctaLabel não mostra botão; variante board desenha colunas', () => {
    const { container } = render(<EmptyState title="x" text="y" variant="board" />)
    expect(screen.queryByRole('button')).toBeNull()
    expect(container.querySelectorAll('.sug-ghost-col').length).toBe(5)
  })

  test('FilaNoHub aponta pra fila do hub em nova aba', () => {
    render(<FilaNoHub />)
    const link = screen.getByRole('link', { name: 'Abrir a fila no hub' })
    expect(link.getAttribute('href')).toBe(`${HUB_PORTAL_URL}/admin/sugestoes`)
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
  })

  test('skeletons respeitam a quantidade pedida', () => {
    const { container, rerender } = render(<CardsSkeleton n={3} />)
    expect(container.querySelectorAll('.sug-card--sk').length).toBe(3)
    rerender(<TableSkeleton rows={4} />)
    expect(container.querySelectorAll('.sug-tr--sk').length).toBe(4)
  })

  test('ícones renderizam svg decorativo', () => {
    const { container } = render(
      <>
        <TypeIcon type="bug" /><TypeIcon type="melhoria" /><TypeIcon type="feature" /><TypeIcon type="duvida" />
        <IconPlus /><IconList /><IconBoard /><IconSearch /><IconInbox />
      </>,
    )
    expect(container.querySelectorAll('svg').length).toBe(9)
  })
})
