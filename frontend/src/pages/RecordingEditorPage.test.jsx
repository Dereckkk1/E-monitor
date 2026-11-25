import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import RecordingEditorPage from './RecordingEditorPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

// A forma de onda desenha em <canvas> (que o jsdom não tem): trocamos por um
// duplo que expõe o que a página passa e os callbacks que ela espera receber.
vi.mock('../components/RecordingWaveform', () => ({
  WaveOverview: ({ view, onViewChange }) => (
    <div data-testid="onda-geral" data-view={`${view.start}-${view.end}`}>
      <button type="button" onClick={() => onViewChange({ start: 10, end: 50 })}>geral: ver 10-50</button>
    </div>
  ),
  WaveDetail: ({ view, playhead, markIn, selectedId, clips, gapMarks, clockLabel, onSeek, onCreate, onResize, onSelect, onZoom, onPan }) => (
    <div
      data-testid="onda-detalhe"
      data-view={`${view.start}-${view.end}`}
      data-playhead={playhead}
      data-markin={markIn ?? ''}
      data-sel={selectedId ?? ''}
      data-clips={clips.length}
      data-gaps={gapMarks.map(g => `${g.t}:${g.seconds}`).join(',')}
      data-rotulo={clockLabel(0, 1)}
    >
      <button type="button" onClick={() => onSeek(30)}>onda: ir pra 30</button>
      <button type="button" onClick={() => onSeek(60)}>onda: ir pra 60</button>
      <button type="button" onClick={() => onCreate(10, 40)}>onda: criar 10-40</button>
      <button type="button" onClick={() => onCreate(40, 70)}>onda: criar 40-70</button>
      <button type="button" onClick={() => onCreate(10, 10.2)}>onda: criar curto</button>
      <button type="button" onClick={() => selectedId && onResize(selectedId, 'to', 55)}>onda: esticar fim pra 55</button>
      <button type="button" onClick={() => clips[0] && onSelect(clips[0].id)}>onda: selecionar primeiro</button>
      <button type="button" onClick={() => onZoom(0.5, 30)}>onda: zoom</button>
      <button type="button" onClick={() => onPan(20)}>onda: andar</button>
    </div>
  ),
}))

const ID = 'rec-1'
const GRAVACAO = {
  id: ID,
  station_name: 'Rádio Teste FM',
  station_band: 'FM',
  station_frequency_mhz: 99.5,
  station_city: 'Cidade Teste',
  station_state: 'SP',
  status: 'concluida',
  has_audio: true,
  starts_at: '2026-07-15T17:00:00Z', // 14:00 em Brasília
  ends_at: '2026-07-15T17:02:00Z',
  // 10s sem áudio a partir das 14:00:50
  gaps: [{ from: '2026-07-15T17:00:50Z', to: '2026-07-15T17:01:00Z' }],
}

describe('RecordingEditorPage', () => {
  let chamadas
  let downloads

  beforeEach(() => {
    localStorage.clear()
    downloads = []
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function registrar() {
      downloads.push(this.download)
    })
  })
  afterEach(() => { vi.restoreAllMocks() })

  function montar(rotas = {}) {
    chamadas = mockApi({
      [`GET /recordings/${ID}`]: GRAVACAO,
      [`GET /recordings/${ID}/peaks`]: new Uint8Array(2400), // 120s a 20 picos/s
      [`GET /recordings/${ID}/audio`]: new Blob(['audio']),
      ...rotas,
    })
    return renderComProviders(<RecordingEditorPage />, { rota: `/admin/gravacoes/${ID}/editar`, caminho: '/admin/gravacoes/:id/editar' })
  }

  const detalhe = () => screen.getByTestId('onda-detalhe')
  const clicar = (nome) => fireEvent.click(screen.getByRole('button', { name: nome }))
  const itensDeTrecho = () => [...document.querySelectorAll('.re-clip')]
  async function esperarAudio() {
    await waitFor(() => expect(screen.getByRole('button', { name: 'Tocar' }).disabled).toBe(false))
  }

  test('mostra a emissora, a janela, o status e as ondas depois de carregar os picos', async () => {
    montar()
    expect(await screen.findByText('Editar trechos')).toBeTruthy()
    expect(screen.getByText('Rádio Teste FM')).toBeTruthy()
    expect(screen.getByText('Concluída')).toBeTruthy()
    expect(document.querySelector('.re-identity-sub').textContent).toContain('Cidade Teste/SP')
    expect(document.querySelector('.re-identity-sub').textContent).toContain('15/07/2026, 14:00 às 14:02')
    // 2400 picos a 20/s = 2 minutos; sem mexer na visão, vê a gravação inteira
    expect(await screen.findByTestId('onda-detalhe')).toBeTruthy()
    expect(detalhe().getAttribute('data-view')).toBe('0-120')
    expect(document.querySelector('.re-now-offset').textContent).toBe('0:00.0 / 2:00.0')
    // o arquivo tem 120s e a janela (2min - 10s de buraco) só 110s: o excedente é pré-roll,
    // então o segundo 0 do arquivo é 10s ANTES das 14:00
    expect(document.querySelector('.re-now-clock').textContent).toContain('13:59:50')
    // o buraco de 10s (14:00:50) cai em 60s do arquivo
    expect(detalhe().getAttribute('data-gaps')).toBe('60:10')
    expect(screen.getByText(/Nenhum trecho ainda/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Baixar todos (.zip)' }).disabled).toBe(true)
  })

  test('o áudio baixa pra tocar: o botão de tocar só habilita quando o blob chega', async () => {
    montar()
    const tocar = await screen.findByRole('button', { name: 'Tocar' })
    await esperarAudio()
    expect(chamadas.some(c => c.chave === `GET /recordings/${ID}/audio`)).toBe(true)
    expect(tocar.disabled).toBe(false)
    expect(document.querySelector('audio').getAttribute('src')).toMatch(/^blob:/)
  })

  test('falha ao baixar o áudio avisa, mas a onda continua disponível pra marcar', async () => {
    montar({ [`GET /recordings/${ID}/audio`]: erroApi(500, {}) })
    expect(await screen.findByText('Áudio: Não foi possível concluir. Tente de novo.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Tocar' }).disabled).toBe(true)
    expect(await screen.findByTestId('onda-detalhe')).toBeTruthy()
  })

  test('falha ao montar a forma de onda mostra o motivo', async () => {
    montar({ [`GET /recordings/${ID}/peaks`]: erroApi(400, 'Gravação sem áudio decodificável') })
    expect(await screen.findByText('Não foi possível montar a forma de onda: Gravação sem áudio decodificável')).toBeTruthy()
    expect(screen.queryByTestId('onda-detalhe')).toBeNull()
  })

  test('gravação inexistente (404) mostra "não encontrada"', async () => {
    montar({ [`GET /recordings/${ID}`]: erroApi(404, 'not found') })
    expect(await screen.findByText('Gravação não encontrada')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Voltar para Gravações' }).getAttribute('href')).toBe('/admin/gravacoes')
  })

  test('gravação expirada (sem áudio) explica que o áudio foi apagado', async () => {
    montar({ [`GET /recordings/${ID}`]: { ...GRAVACAO, has_audio: false, status: 'expirada' } })
    expect(await screen.findByText('Esta gravação não tem áudio')).toBeTruthy()
    expect(screen.getByText('O áudio passou dos 15 dias e foi apagado.')).toBeTruthy()
  })

  test('criar trecho pela onda: nome padrão pela hora do ar, duração e aviso de buraco', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    clicar('onda: criar 40-70')
    const itens = itensDeTrecho()
    expect(itens.length).toBe(2)
    // mesmo minuto → nome com segundos
    expect(within(itens[0]).getByLabelText('Nome do trecho 1').value).toBe('Rádio Teste FM 14h00m00s-14h00m30s')
    expect(itens[0].textContent).toContain('30s')
    expect(itens[0].textContent).not.toContain('buraco')
    // 40–70 atravessa o buraco de 10s que cai em 60; as horas pulam o buraco (14:00:30 → 14:01:10)
    expect(within(itens[1]).getByLabelText('Nome do trecho 2').value).toBe('Rádio Teste FM 14h00-14h01')
    expect(itens[1].querySelector('.re-clip-warn').textContent).toContain('Contém um buraco de 10s')
    expect(screen.getByText('2 · 1m00s')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Baixar todos (.zip)' }).disabled).toBe(false)
    // persistido no navegador
    expect(JSON.parse(localStorage.getItem(`rec-editor:${ID}`)).length).toBe(2)
  })

  test('trecho com menos de meio segundo é ignorado', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar curto')
    expect(itensDeTrecho().length).toBe(0)
  })

  test('trechos guardados no navegador voltam ao reabrir a gravação', async () => {
    localStorage.setItem(`rec-editor:${ID}`, JSON.stringify([
      { id: 'salvo-1', from: 5, to: 25, name: 'Entrevista Teste', named: true },
      { id: 'lixo', from: 'x', to: 3 },
    ]))
    montar()
    await screen.findByTestId('onda-detalhe')
    const itens = itensDeTrecho()
    expect(itens.length).toBe(1)
    expect(within(itens[0]).getByLabelText('Nome do trecho 1').value).toBe('Entrevista Teste')
  })

  test('renomear mantém o nome; esvaziar volta ao nome automático; ajuste fino mexe 0,1s na ponta', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    const nome = screen.getByLabelText('Nome do trecho 1')
    fireEvent.change(nome, { target: { value: 'Entrevista Teste' } })
    expect(nome.value).toBe('Entrevista Teste')
    fireEvent.change(nome, { target: { value: '' } })
    fireEvent.blur(nome)
    expect(nome.value).toBe('Rádio Teste FM 14h00m00s-14h00m30s')

    // ‹ no início: 10s → 9,9s; o relógio do trecho passa a 13:59:59 (pré-roll de 10s)
    fireEvent.click(screen.getByLabelText('Início 0,1s antes'))
    expect(itensDeTrecho()[0].querySelector('.re-edge-time').textContent).toBe('13:59:59')
    fireEvent.click(screen.getByLabelText('Fim 0,1s depois'))
    expect(JSON.parse(localStorage.getItem(`rec-editor:${ID}`))[0]).toMatchObject({ from: 9.9, to: 40.1 })
  })

  test('esticar o fim pela alça atualiza o trecho e o nome automático acompanha', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    clicar('onda: esticar fim pra 55')
    expect(screen.getByLabelText('Nome do trecho 1').value).toBe('Rádio Teste FM 14h00m00s-14h00m45s')
    expect(itensDeTrecho()[0].querySelector('.re-clip-len').textContent).toBe('45s')
  })

  test('excluir pelo botão e pela tecla Delete (no trecho selecionado)', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    clicar('onda: criar 40-70')
    fireEvent.click(within(itensDeTrecho()[0]).getByRole('button', { name: 'Excluir trecho' }))
    expect(itensDeTrecho().length).toBe(1)
    // o trecho que sobrou foi o último criado (selecionado): Delete o remove
    expect(detalhe().getAttribute('data-sel')).not.toBe('')
    fireEvent.keyDown(window, { key: 'Delete' })
    expect(itensDeTrecho().length).toBe(0)
    expect(localStorage.getItem(`rec-editor:${ID}`)).toBeNull()
  })

  test('atalhos I e O marcam início e fim a partir da posição da agulha; Esc cancela a marca', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    clicar('onda: ir pra 30')
    fireEvent.keyDown(window, { key: 'i' })
    expect(detalhe().getAttribute('data-markin')).toBe('30')
    clicar('onda: ir pra 60')
    fireEvent.keyDown(window, { key: 'o' })
    expect(itensDeTrecho().length).toBe(1)
    expect(itensDeTrecho()[0].querySelector('.re-clip-len').textContent).toBe('30s')
    expect(detalhe().getAttribute('data-markin')).toBe('')

    // sem I antes, O fecha um trecho dos últimos 30s
    fireEvent.keyDown(window, { key: 'Escape' }) // desmarca o selecionado
    fireEvent.keyDown(window, { key: 'o' })
    expect(itensDeTrecho().length).toBe(2)
    // Esc limpa uma marca de início pendente
    fireEvent.keyDown(window, { key: 'Escape' })
    fireEvent.keyDown(window, { key: 'i' })
    expect(detalhe().getAttribute('data-markin')).toBe('60')
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(detalhe().getAttribute('data-markin')).toBe('')
  })

  test('com um trecho selecionado, I e O ajustam as pontas dele em vez de criar outro', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40') // fica selecionado
    clicar('onda: ir pra 30')
    fireEvent.keyDown(window, { key: 'i' })
    expect(itensDeTrecho().length).toBe(1)
    expect(itensDeTrecho()[0].querySelector('.re-clip-len').textContent).toBe('10s') // 30 → 40
    clicar('onda: ir pra 60')
    fireEvent.keyDown(window, { key: 'o' })
    expect(itensDeTrecho()[0].querySelector('.re-clip-len').textContent).toBe('30s') // 30 → 60
  })

  test('setas movem a agulha 1s (Shift 5s) e a tecla 0 volta a ver tudo; atalhos não valem digitando', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    fireEvent.keyDown(window, { key: 'ArrowRight' })
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(1)
    fireEvent.keyDown(window, { key: 'ArrowRight', shiftKey: true })
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(6)
    fireEvent.keyDown(window, { key: 'ArrowLeft' })
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(5)

    // dentro do campo "Ir para", as teclas não mexem na agulha
    fireEvent.keyDown(screen.getByLabelText('Ir para'), { key: 'ArrowRight' })
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(5)

    fireEvent.keyDown(window, { key: '+' })
    expect(detalhe().getAttribute('data-view')).not.toBe('0-120')
    fireEvent.keyDown(window, { key: '0' })
    expect(detalhe().getAttribute('data-view')).toBe('0-120')
  })

  test('zoom: botões +/−/Tudo e o zoom/arrasto vindos da onda alteram a janela visível', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    fireEvent.click(screen.getByTitle('Mais zoom (+)'))
    expect(detalhe().getAttribute('data-view')).toBe('30-90') // metade da janela, centrada
    fireEvent.click(screen.getByTitle('Menos zoom (-)'))
    expect(detalhe().getAttribute('data-view')).toBe('0-120')
    fireEvent.click(screen.getByTitle('Mais zoom (+)'))
    clicar('onda: andar')
    expect(detalhe().getAttribute('data-view')).toBe('50-110')
    clicar('geral: ver 10-50')
    expect(detalhe().getAttribute('data-view')).toBe('10-50')
    fireEvent.click(screen.getByRole('button', { name: 'Tudo' }))
    expect(detalhe().getAttribute('data-view')).toBe('0-120')
    clicar('onda: zoom')
    expect(detalhe().getAttribute('data-view')).not.toBe('0-120')
  })

  test('"Ir para" converte a hora do ar em posição no arquivo (descontando o buraco)', async () => {
    montar()
    await screen.findByTestId('onda-detalhe')
    const campo = screen.getByLabelText('Ir para')
    // texto que não é hora não faz nada
    fireEvent.change(campo, { target: { value: 'banana' } })
    fireEvent.submit(campo.closest('form'))
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(0)
    // 14:01 = fim do buraco: 70s depois do início do arquivo (13:59:50) - 10s sem áudio = 60s no arquivo
    fireEvent.change(campo, { target: { value: '14:01' } })
    fireEvent.submit(campo.closest('form'))
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(60)
    expect(document.querySelector('.re-now-offset').textContent).toBe('1:00.0 / 2:00.0')
    // um horário depois do fim da gravação para no limite do arquivo
    fireEvent.change(campo, { target: { value: '15h30' } })
    fireEvent.submit(campo.closest('form'))
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(120)
  })

  test('espaço alterna tocar/pausar quando o áudio já chegou', async () => {
    montar()
    await esperarAudio()
    fireEvent.keyDown(window, { key: ' ' })
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled())
    expect(await screen.findByRole('button', { name: 'Pausar' })).toBeTruthy()
    fireEvent.keyDown(window, { key: ' ' })
    expect(HTMLMediaElement.prototype.pause).toHaveBeenCalled()
    expect(await screen.findByRole('button', { name: 'Tocar' })).toBeTruthy()
  })

  test('ouvir um trecho posiciona o player no início dele e toca', async () => {
    montar()
    await esperarAudio()
    clicar('onda: criar 10-40')
    fireEvent.click(screen.getByRole('button', { name: 'Ouvir o trecho' }))
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled())
    expect(Number(detalhe().getAttribute('data-playhead'))).toBe(10)
  })

  test('baixar um trecho em MP3 manda from/to/fade/name e dispara o download do arquivo', async () => {
    montar({ [`GET /recordings/${ID}/clip`]: new Blob(['mp3']) })
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    fireEvent.click(screen.getByLabelText('Fade curto nas pontas'))   // desliga o fade
    fireEvent.click(screen.getByRole('button', { name: 'Baixar MP3' }))
    await waitFor(() => expect(downloads.length).toBe(1))
    expect(downloads[0]).toBe('Rádio Teste FM 14h00m00s-14h00m30s.mp3')
    const clip = chamadas.find(c => c.chave === `GET /recordings/${ID}/clip`)
    expect(clip.params).toEqual({ from: '10.000', to: '40.000', fade: 0, name: 'Rádio Teste FM 14h00m00s-14h00m30s' })
  })

  test('erro ao gerar o MP3 abre um aviso com a mensagem do servidor', async () => {
    montar({ [`GET /recordings/${ID}/clip`]: erroApi(400, 'Trecho fora da gravação') })
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    fireEvent.click(screen.getByRole('button', { name: 'Baixar MP3' }))
    expect(await screen.findByText('Trecho fora da gravação')).toBeTruthy()
    expect(downloads.length).toBe(0)
  })

  test('baixar todos gera um .zip com os trechos e o nome do dia da gravação', async () => {
    montar({ [`POST /recordings/${ID}/clips`]: new Blob(['zip']) })
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    clicar('onda: criar 40-70')
    fireEvent.click(screen.getByRole('button', { name: 'Baixar todos (.zip)' }))
    await waitFor(() => expect(downloads).toEqual(['Trechos Rádio Teste FM 2026-07-15.zip']))
    const post = chamadas.find(c => c.chave === `POST /recordings/${ID}/clips`)
    expect(post.data.fade).toBe(true)
    expect(post.data.name).toBe('Trechos Rádio Teste FM 2026-07-15')
    expect(post.data.clips).toEqual([
      { name: 'Rádio Teste FM 14h00m00s-14h00m30s', from: 10, to: 40 },
      { name: 'Rádio Teste FM 14h00-14h01', from: 40, to: 70 },
    ])
  })

  test('erro ao gerar o .zip também avisa', async () => {
    montar({ [`POST /recordings/${ID}/clips`]: erroApi(502, '<html>bad gateway</html>') })
    await screen.findByTestId('onda-detalhe')
    clicar('onda: criar 10-40')
    fireEvent.click(screen.getByRole('button', { name: 'Baixar todos (.zip)' }))
    expect(await screen.findByText('Não foi possível concluir. Tente de novo.')).toBeTruthy()
  })
})
