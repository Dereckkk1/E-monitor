import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import ClipboardPasteZone from './ClipboardPasteZone'
import { Controlado } from '../../test-utils/area-componentes'

const png = (nome = 'print.png', tamanho) => {
  const f = new File(['x'], nome, { type: 'image/png' })
  if (tamanho != null) Object.defineProperty(f, 'size', { value: tamanho })
  return f
}

function montar(props = {}) {
  const onMudou = vi.fn()
  const r = render(
    <Controlado Comp={ClipboardPasteZone} propValor="files" propMudou="onChange"
      inicial={[]} onMudou={onMudou} extra={props} />,
  )
  return { onMudou, ...r }
}

const entrada = (container) => container.querySelector('input[type="file"]')
const ultimo = (fn) => fn.mock.calls[fn.mock.calls.length - 1][0]

describe('ClipboardPasteZone — seleção de arquivos', () => {
  test('mostra a chamada pra colar/arrastar e a dica personalizada', () => {
    montar({ hint: 'Cole/arraste um print' })
    expect(screen.getByText('Cole um print')).toBeTruthy()
    expect(screen.getByText('Cole/arraste um print')).toBeTruthy()
  })

  test('escolher imagens pelo seletor adiciona miniaturas e devolve a lista', () => {
    const { container, onMudou } = montar()
    fireEvent.change(entrada(container), { target: { files: [png('a.png'), png('b.png')] } })
    expect(ultimo(onMudou).map(f => f.name)).toEqual(['a.png', 'b.png'])
    expect(screen.getByAltText('a.png')).toBeTruthy()
    expect(screen.getByAltText('b.png')).toBeTruthy()
  })

  test('arquivo que não é imagem é recusado com aviso', () => {
    const { container, onMudou } = montar()
    fireEvent.change(entrada(container), { target: { files: [new File(['x'], 'doc.pdf', { type: 'application/pdf' })] } })
    expect(screen.getByText('Só imagens (PNG, JPG, WEBP, GIF).')).toBeTruthy()
    expect(onMudou).not.toHaveBeenCalled()
  })

  test('imagem acima de 10MB é recusada', () => {
    const { container, onMudou } = montar()
    fireEvent.change(entrada(container), { target: { files: [png('grande.png', 11 * 1024 * 1024)] } })
    expect(screen.getByText('Máx. 10MB por imagem.')).toBeTruthy()
    expect(onMudou).not.toHaveBeenCalled()
  })

  test('respeita o máximo: guarda só as primeiras e avisa', () => {
    const { container, onMudou } = montar({ max: 2 })
    fireEvent.change(entrada(container), { target: { files: [png('a.png'), png('b.png'), png('c.png')] } })
    expect(ultimo(onMudou).map(f => f.name)).toEqual(['a.png', 'b.png'])
    expect(screen.getByText('Máximo de 2 imagens.')).toBeTruthy()
  })

  test('remover uma miniatura tira só aquela', () => {
    const { container, onMudou } = montar()
    fireEvent.change(entrada(container), { target: { files: [png('a.png'), png('b.png')] } })
    fireEvent.click(screen.getAllByRole('button', { name: 'Remover' })[0])
    expect(ultimo(onMudou).map(f => f.name)).toEqual(['b.png'])
    expect(screen.queryByAltText('a.png')).toBeNull()
  })

  test('Enter e espaço na área de soltar abrem o seletor de arquivos', () => {
    const { container } = montar()
    const clique = vi.spyOn(entrada(container), 'click').mockImplementation(() => {})
    const area = screen.getByRole('button', { name: /Cole um print/ })
    fireEvent.keyDown(area, { key: 'Enter' })
    fireEvent.keyDown(area, { key: ' ' })
    fireEvent.click(area)
    expect(clique).toHaveBeenCalledTimes(3)
  })
})

describe('ClipboardPasteZone — arrastar e colar', () => {
  test('arrastar por cima destaca a área; soltar adiciona os arquivos', () => {
    const { container, onMudou } = montar()
    const area = screen.getByRole('button', { name: /Cole um print/ })
    fireEvent.dragOver(area)
    expect(area.className).toContain('is-dragging')
    fireEvent.dragLeave(area)
    expect(area.className).not.toContain('is-dragging')
    fireEvent.drop(area, { dataTransfer: { files: [png('solto.png')] } })
    expect(ultimo(onMudou).map(f => f.name)).toEqual(['solto.png'])
    expect(container.querySelector('.sug-paste-thumb')).not.toBeNull()
  })

  test('colar uma imagem da área de transferência dentro do componente a adiciona', () => {
    const { container, onMudou } = montar()
    const arquivo = png('colado.png')
    fireEvent.paste(container.querySelector('.sug-paste'), {
      clipboardData: { items: [{ kind: 'file', type: 'image/png', getAsFile: () => arquivo }] },
    })
    expect(ultimo(onMudou)).toEqual([arquivo])
  })

  test('colar texto (sem imagem) é ignorado', () => {
    const { container, onMudou } = montar()
    fireEvent.paste(container.querySelector('.sug-paste'), {
      clipboardData: { items: [{ kind: 'string', type: 'text/plain', getAsFile: () => null }] },
    })
    expect(onMudou).not.toHaveBeenCalled()
  })

  test('com pasteTarget="window" a colagem vale em qualquer lugar da página', () => {
    const { onMudou } = montar({ pasteTarget: 'window' })
    const arquivo = png('global.png')
    fireEvent.paste(window, {
      clipboardData: { items: [{ kind: 'file', type: 'image/png', getAsFile: () => arquivo }] },
    })
    expect(ultimo(onMudou)).toEqual([arquivo])
  })
})
