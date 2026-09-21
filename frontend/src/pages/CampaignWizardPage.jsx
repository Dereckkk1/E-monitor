import { useState, useEffect, useMemo, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import {
  useCampaign, useCreateCampaign, useUpdateCampaign,
  useCampaignMaterials, useClients, useStations,
  useDistributionRules, useMaterials,
} from '../api/hooks'
import WizardLayout from '../components/WizardLayout'
import BasicDataStep from './CampaignWizardSteps/BasicDataStep'
import StationsStep from './CampaignWizardSteps/StationsStep'
import ConnectionStep from './CampaignWizardSteps/ConnectionStep'
import MaterialsStep from './CampaignWizardSteps/MaterialsStep'
import DistributionStep from './CampaignWizardSteps/DistributionStep'
import PricingStep from './CampaignWizardSteps/PricingStep'

export default function CampaignWizardPage() {
  const { id: routeId } = useParams()
  const navigate = useNavigate()
  const isEdit = !!routeId

  const [draftCampaign, setDraftCampaign] = useState({
    name: '', client_id: '', start_date: '', end_date: '', hub_code: '',
  })
  const [campaignId, setCampaignId] = useState(routeId ?? null)
  const [currentStep, setCurrentStep] = useState(1)
  const [completedSteps, setCompletedSteps] = useState([])

  const { data: existingCampaign } = useCampaign(routeId)
  useEffect(() => {
    if (existingCampaign) {
      setDraftCampaign({
        name: existingCampaign.name,
        client_id: existingCampaign.client_id,
        start_date: existingCampaign.start_date?.slice(0, 10) ?? '',
        end_date: existingCampaign.end_date?.slice(0, 10) ?? '',
        hub_code: existingCampaign.hub_code ?? '',
      })
      setCampaignId(existingCampaign.id)
      // Em modo edit todas as etapas anteriores são consideradas concluídas,
      // assim o usuário pode navegar livremente pra editar valor sem refazer
      // o fluxo todo.
      setCompletedSteps([1, 2, 3, 4, 5])
    }
  }, [existingCampaign])

  const { data: clients = [] } = useClients()
  const { data: stationsData } = useStations({ limit: 10000 })
  const allStations = stationsData?.data ?? stationsData ?? []
  const { data: campaignMaterials = [] } = useCampaignMaterials(campaignId)
  const { data: distributionRules = [] } = useDistributionRules(campaignId)

  // Hydrate materials for the client (provides title + type_id that campaign_materials lacks)
  const { data: clientLibrary = [] } = useMaterials(draftCampaign.client_id || null)
  const materialsById = useMemo(
    () => Object.fromEntries(clientLibrary.map(m => [m.id, m])),
    [clientLibrary]
  )

  const currentClient = clients.find(c => c.id === draftCampaign.client_id) ?? null
  const clientName = currentClient?.name ?? ''
  const targetStationIds = existingCampaign?.target_stations ?? []
  const stationCount = targetStationIds.length
  const materialCount = campaignMaterials.length
  const distributedTotal = stationCount * materialCount
  const distributedCount = countCoveredCombinations(distributionRules)

  const createCampaign = useCreateCampaign()
  const updateCampaign = useUpdateCampaign()

  // Ref usada pelo PricingStep pra expor saveAll() ao parent. handleFinish
  // chama isso ANTES de navegar pra /campaigns — sem o save, os drafts do
  // Step 5 nunca chegam no banco.
  const pricingRef = useRef(null)

  function handleStepClick(step) {
    if (step <= currentStep || completedSteps.includes(step - 1)) {
      setCurrentStep(step)
    }
  }

  function markStepCompleteAndAdvance() {
    if (!completedSteps.includes(currentStep)) {
      setCompletedSteps([...completedSteps, currentStep])
    }
    setCurrentStep(s => Math.min(6, s + 1))
  }

  function handlePrev() {
    setCurrentStep(s => Math.max(1, s - 1))
  }

  async function handleNext() {
    if (currentStep === 1 && !campaignId) {
      try {
        const created = await createCampaign.mutateAsync({
          ...draftCampaign,
          start_date: draftCampaign.start_date + 'T00:00:00Z',
          end_date:   draftCampaign.end_date   + 'T00:00:00Z',
          target_stations: [],
        })
        setCampaignId(created.id)
        navigate(`/campaigns/${created.id}/edit`, { replace: true })
      } catch {
        window.alert('Erro ao criar campanha. Tente novamente.')
        return
      }
    } else if (currentStep === 1 && campaignId) {
      // Modo edit: persiste qualquer mudança em name / start_date / end_date
      // antes de avançar. Sem isso o usuário podia editar o período no Step 1
      // e o backend nunca via — bug reportado em 2026-05-12.
      const startChanged = (existingCampaign?.start_date?.slice(0, 10) ?? '') !== draftCampaign.start_date
      const endChanged   = (existingCampaign?.end_date?.slice(0, 10)   ?? '') !== draftCampaign.end_date
      const nameChanged  = (existingCampaign?.name ?? '')               !== draftCampaign.name
      const codeChanged  = (existingCampaign?.hub_code ?? '')           !== draftCampaign.hub_code
      if (startChanged || endChanged || nameChanged || codeChanged) {
        try {
          await updateCampaign.mutateAsync({
            id: campaignId,
            name: draftCampaign.name,
            start_date: draftCampaign.start_date + 'T00:00:00Z',
            end_date:   draftCampaign.end_date   + 'T00:00:00Z',
            /* ⚠️ `hub_code` vai SÓ quando mudou, e a omissão é o desenho: o
               backend trata campo ausente como "não mexe" e campo vazio como
               "apaga", e apagar o código CONGELA a coleta da proposta no hub
               (§6.5). Mandar sempre significaria que qualquer falha em carregar
               o código para o rascunho apagaria o código de uma campanha boa
               na primeira edição de nome. */
            ...(codeChanged ? { hub_code: draftCampaign.hub_code.trim() } : {}),
          })
        } catch {
          window.alert('Erro ao salvar alterações da campanha. Tente novamente.')
          return
        }
      }
    }
    markStepCompleteAndAdvance()
  }

  async function handleFinish() {
    // Step 5 → persiste todos os pricings antes de fechar o wizard. Se algum
    // falhar, saveAll() já mostra alert; aqui apenas abortamos.
    if (pricingRef.current) {
      const ok = await pricingRef.current.saveAll()
      if (!ok) return
    }
    navigate('/campaigns', { replace: true })
  }

  const summary = {
    name: draftCampaign.name,
    clientName,
    startDate: draftCampaign.start_date,
    endDate: draftCampaign.end_date,
    stationCount, materialCount,
    distributedCount, distributedTotal,
  }

  const title = isEdit ? `Editar: ${draftCampaign.name}` : 'Nova campanha'

  let stepContent = null
  let nextDisabled = false

  if (currentStep === 1) {
    stepContent = (
      <BasicDataStep
        value={draftCampaign}
        onChange={setDraftCampaign}
        clients={clients}
        isEditMode={isEdit}
      />
    )
    // Decisão 6 da spec: o código é obrigatório na CRIAÇÃO. Campanha antiga
    // continua salvando sem ele — senão corrigir uma data numa campanha de
    // junho viraria "vá ao hub criar uma campanha primeiro".
    nextDisabled = !draftCampaign.name || !draftCampaign.client_id ||
                   !draftCampaign.start_date || !draftCampaign.end_date ||
                   (!isEdit && !(draftCampaign.hub_code ?? '').trim())
  } else if (currentStep === 2) {
    stepContent = (
      <StationsStep
        campaignId={campaignId}
        allStations={allStations}
        currentSelection={targetStationIds}
      />
    )
    nextDisabled = stationCount === 0
  } else if (currentStep === 3) {
    stepContent = (
      <ConnectionStep
        campaignStations={targetStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean)}
      />
    )
    // Etapa diagnóstica: nunca bloqueia avançar (spec §3).
    nextDisabled = false
  } else if (currentStep === 4) {
    stepContent = (
      <MaterialsStep
        campaignId={campaignId}
        clientId={draftCampaign.client_id}
        materialsById={materialsById}
        campaignStations={targetStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean)}
        onSkip={handleNext}
      />
    )
    // Migration 0019: distribution is by type, so every linked material MUST
    // have a type_id before advancing — otherwise no rule can cover it.
    const someWithoutType = campaignMaterials.some(cm => {
      const mat = materialsById[cm.material_id]
      return mat && !mat.type_id
    })
    const someWithoutStations = campaignMaterials.some(cm =>
      !cm.target_stations || cm.target_stations.length === 0)
    // Distribuição sem material é permitida: o operador pode planejar regras
    // por TIPO no Step 5 antes do áudio chegar (spec 2026-05-25). Só
    // bloqueamos quando há material linkado mas mal configurado.
    nextDisabled = someWithoutType || someWithoutStations
  } else if (currentStep === 5) {
    stepContent = (
      <DistributionStep
        campaignId={campaignId}
        campaignStart={existingCampaign?.start_date ?? draftCampaign.start_date}
        campaignEnd={existingCampaign?.end_date ?? draftCampaign.end_date}
        campaignMaterials={campaignMaterials}
        campaignStationIds={targetStationIds}
        materialsById={materialsById}
        allStations={allStations}
      />
    )
    nextDisabled = false
  } else if (currentStep === 6) {
    const campaignStations = targetStationIds
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
    stepContent = (
      <PricingStep
        ref={pricingRef}
        campaignId={campaignId}
        campaignStations={campaignStations}
        campaignMaterials={campaignMaterials}
        materialsById={materialsById}
        distributionRules={distributionRules}
        initialFixedCPM={existingCampaign?.fixed_cpm ?? null}
      />
    )
    // O nextDisabled aqui é só visual; saveAll() valida de novo na hora.
    nextDisabled = false
  }

  const nextLabel =
    currentStep === 6
      ? 'Concluir campanha →'
      : (currentStep === 4 && materialCount === 0)
        ? 'Pular materiais →'
        : 'Avançar →'
  const onNext = currentStep === 6 ? handleFinish : handleNext

  return (
    <WizardLayout
      title={title}
      client={currentClient}
      currentStep={currentStep}
      completedSteps={completedSteps}
      onStepClick={handleStepClick}
      onPrev={handlePrev}
      onNext={onNext}
      nextDisabled={nextDisabled}
      nextLabel={nextLabel}
      summary={summary}
    >
      {stepContent}
    </WizardLayout>
  )
}

function countCoveredCombinations(rules) {
  const covered = new Set()
  for (const r of rules) {
    for (const sid of r.station_ids) {
      covered.add(`${sid}|${r.material_id}`)
    }
  }
  return covered.size
}
