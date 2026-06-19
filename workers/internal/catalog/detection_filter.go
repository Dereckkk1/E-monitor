package catalog

// ApprovedDetectionsFilter é o fragmento SQL canônico que seleciona o conjunto
// "aprovado" de detecções — as ÚNICAS que podem ser contadas ou listadas em
// qualquer número de veiculação visível ao usuário no sistema inteiro.
//
// Uma detecção é aprovada quando NÃO foi retratada (§18.2.2 desambiguação de
// versões), NÃO foi ignorada manualmente por um admin ("Desconsiderar") e NÃO
// foi rejeitada pelo audit de evidência §9.9. Toda tela que mostra "emissora X
// veiculou N" — grid + modal de /detections, /insights, /live-map, /management,
// /reports, falhas por emissora/campanha — TEM que resolver pro mesmo conjunto,
// senão a mesma veiculação aparece como número diferente em lugares diferentes
// (foi o que o operador reportou: modal=4, grid=2 na mesma célula).
//
// Uso: a tabela detections PRECISA estar aliasada como `d` na query; daí
// concatene "AND " + ApprovedDetectionsFilter na cláusula WHERE. O predicado não
// carrega bind params, então nunca desloca a numeração de $N.
//
// A VIEW daily_play_summary (migration 0029) já embute exatamente este filtro no
// CTE `actual`, então qualquer query que lê a view herda o conjunto de graça e
// NÃO deve reaplicá-lo.
//
// Exceções deliberadas (documentadas em
// docs/architecture/detection-count-consistency.md):
//   - Detections.Get (detalhe /detections/:id) devolve a linha independente do
//     estado pro operador inspecionar uma veiculação retratada/ignorada/rejeitada;
//     a página renderiza os badges de estado.
//   - system_health.go conta detecções CRUAS nos contadores de liveness do
//     pipeline (o matcher está produzindo saída?), não é tally de veiculação.
//   - O recategorizador dá UPDATE em todas as linhas do escopo, por design.
const ApprovedDetectionsFilter = `d.retracted_at IS NULL AND d.ignored_at IS NULL AND d.evidence_status <> 'audit_rejected'`
