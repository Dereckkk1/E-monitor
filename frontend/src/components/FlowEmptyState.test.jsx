import { describe, test, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import FlowEmptyState from './FlowEmptyState'
import FlowStepper from './FlowStepper'
import RequireAuth from './RequireAuth'
import RequireRole from './RequireRole'
import { renderComProviders, USUARIO_ADMIN, USUARIO_CLIENTE } from '../test-utils/render'

describe('FlowStepper', () => {
  test('marca feito / ativo / pendente conforme o passo atual', () => {
    const { container } = render(<FlowStepper step={2} steps={['Cliente', 'Campanha', 'Período']} />)
    const itens = container.querySelectorAll('.detection-empty-stepper-item')
    expect(itens[0].className).toContain('--done')
    expect(itens[1].className).toContain('--active')
    expect(itens[2].className).toContain('--pending')
    // feito mostra o check (svg) em vez do número; ativo e pendente mostram o número
    expect(itens[0].querySelector('svg')).not.toBeNull()
    expect(itens[1].querySelector('.detection-empty-stepper-num').textContent).toBe('2')
    expect(itens[2].querySelector('.detection-empty-stepper-num').textContent).toBe('3')
    expect(container.querySelectorAll('.detection-empty-stepper-sep').length).toBe(2)
  })
})

describe('FlowEmptyState', () => {
  test('só o título é obrigatório; o resto some quando não vem', () => {
    const { container } = render(<FlowEmptyState title="Nada por aqui" />)
    expect(screen.getByRole('status')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Nada por aqui' })).toBeTruthy()
    expect(container.querySelector('.detection-empty-stepper')).toBeNull()
    expect(container.querySelector('.detection-empty-ghost')).toBeNull()
    expect(container.querySelector('.detection-empty-actions')).toBeNull()
    expect(container.querySelector('p')).toBeNull()
  })

  test('com stepper, ícone, descrição, ação e silhueta ao fundo', () => {
    const { container } = render(
      <FlowEmptyState
        step={1} steps={['Cliente', 'Campanha']} tone="warn" className="extra"
        icon={<i data-testid="icone" />} title="Escolha um cliente"
        description="Sem cliente não há o que mostrar."
        actions={<button>Ir pra clientes</button>}
        ghost={<div data-testid="silhueta" />}
      />,
    )
    expect(container.firstChild.className).toBe('detection-empty extra')
    expect(container.querySelector('.detection-empty-stepper')).not.toBeNull()
    expect(container.querySelector('.detection-empty-icon').className).toContain('--warn')
    expect(screen.getByText('Sem cliente não há o que mostrar.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Ir pra clientes' })).toBeTruthy()
    expect(screen.getByTestId('silhueta').parentElement.getAttribute('aria-hidden')).toBe('true')
  })

  test('tom "mute" usa a classe do ícone apagado', () => {
    const { container } = render(<FlowEmptyState tone="mute" icon={<i />} title="x" />)
    expect(container.querySelector('.detection-empty-icon').className).toContain('--mute')
  })

  test('stepper só aparece com step E steps', () => {
    const { container } = render(<FlowEmptyState step={1} title="x" />)
    expect(container.querySelector('.detection-empty-stepper')).toBeNull()
  })
})

describe('RequireAuth / RequireRole', () => {
  test('RequireAuth: logado vê o conteúdo', () => {
    renderComProviders(<RequireAuth><div>Área logada</div></RequireAuth>, { caminho: '/' })
    expect(screen.getByText('Área logada')).toBeTruthy()
  })

  test('RequireAuth deslogado redireciona', () => {
    renderComProviders(<RequireAuth><div>Área logada</div></RequireAuth>, { caminho: '/', usuario: null })
    expect(screen.queryByText('Área logada')).toBeNull()
    expect(screen.getByTestId('rota-fora')).toBeTruthy()
  })

  test('RequireRole: admin entra na rota de admin', () => {
    renderComProviders(<RequireRole roles={['admin']}><div>Só admin</div></RequireRole>, { caminho: '/', usuario: USUARIO_ADMIN })
    expect(screen.getByText('Só admin')).toBeTruthy()
  })

  test('RequireRole: cliente (viewer) é tratado como "client" e barrado da rota de admin', () => {
    renderComProviders(<RequireRole roles={['admin']}><div>Só admin</div></RequireRole>, { caminho: '/', usuario: USUARIO_CLIENTE })
    expect(screen.queryByText('Só admin')).toBeNull()
    expect(screen.getByTestId('rota-fora')).toBeTruthy()
  })

  test('RequireRole: cliente passa quando "client" está na lista', () => {
    renderComProviders(<RequireRole roles={['admin', 'client']}><div>Admin ou cliente</div></RequireRole>, { caminho: '/', usuario: USUARIO_CLIENTE })
    expect(screen.getByText('Admin ou cliente')).toBeTruthy()
  })

  test('RequireRole: operator conta como admin', () => {
    renderComProviders(<RequireRole roles={['admin']}><div>Só admin</div></RequireRole>,
      { caminho: '/', usuario: { ...USUARIO_ADMIN, role: 'operator' } })
    expect(screen.getByText('Só admin')).toBeTruthy()
  })

  test('RequireRole sem usuário manda pro login', () => {
    renderComProviders(<RequireRole roles={['admin']}><div>Só admin</div></RequireRole>, { caminho: '/', usuario: null })
    expect(screen.queryByText('Só admin')).toBeNull()
    expect(screen.getByTestId('rota-fora')).toBeTruthy()
  })
})
