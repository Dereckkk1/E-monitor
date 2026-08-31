/**
 * planUploadOutcome — decide o que fazer depois de POST /materials.
 *
 * O handler de upload (workers/internal/api/handlers/materials.go) tem dois
 * desfechos com significados MUITO diferentes e que só se distinguem pelo
 * status HTTP:
 *
 *   201 → material novo. O evento `fingerprint.generate` foi publicado e o
 *         serviço Python vai gerar o fingerprint; depois dele vem a checagem
 *         de similaridade. Faz sentido acompanhar as etapas.
 *
 *   200 → dedup por `master_sha256`: o cliente JÁ tem material com esse áudio,
 *         a API devolveu a linha existente e saiu ANTES do publish. Não há
 *         nada pra gerar nem pra comparar — encenar as etapas é mentira.
 *
 * A distinção existe em produção desde sempre; o que faltava era a tela usá-la
 * (diagnóstico de 2026-08-31: operador subiu o mesmo áudio várias vezes achando
 * que o fingerprint estava quebrado).
 *
 * Além do aviso, esta função decide se deve vincular. Quando o material reusado
 * JÁ está na campanha, relinkar não é inofensivo: `CampaignMaterials.Link` é um
 * upsert com `DO UPDATE SET target_stations = EXCLUDED.target_stations`, e o
 * frontend manda todas as emissoras da campanha — o re-upload sobrescreveria em
 * silêncio um escopo de emissoras possivelmente restrito.
 */
export function planUploadOutcome({ status, material, alreadyLinkedIds }) {
  const reused = status === 200
  if (!reused) {
    return { reused: false, shouldLink: true, stage: 'fingerprinting', notice: null }
  }

  const linked = alreadyLinkedIds.has(material.id)
  const ref = material.short_id != null
    ? `«${material.title}» (#${material.short_id})`
    : `«${material.title}»`

  return {
    reused: true,
    shouldLink: !linked,
    stage: 'reused',
    notice: linked
      ? `Este áudio já está cadastrado como ${ref} e já faz parte desta campanha. Nada foi alterado.`
      : `Este áudio já está cadastrado como ${ref}. Vinculei o material existente à campanha.`,
  }
}
