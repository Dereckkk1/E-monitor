-- Migration 0069 — o código do E-Hub na campanha.
--
-- Contexto: a partir de 2026-09-18 a campanha NASCE no hub, com um código
-- (`EH-7K4M2X`), e quem cadastra aqui COLA esse código num campo obrigatório.
-- O hub então guarda a campanha do E-monitor como uma proposta DENTRO da
-- campanha daquele código, em vez de criar uma campanha nova e esperar que
-- alguém as junte à mão. Spec do hub, 2026-09-18, §4.1 e §10.
--
-- ⚠️ SEM índice único em `hub_code`, e isso é decisão de produto (a de número 5
-- da spec), não esquecimento: dois PIs da mesma ação de marketing compartilham
-- um código DE PROPÓSITO, e viram duas propostas no mesmo espaço de pastas lá.
-- Um unique aqui quebraria justamente o caso que a mudança existe para atender.
--
-- TEXT e não uma FK: o hub é outro banco, em outra máquina. Não há integridade
-- referencial a ter, e fingir que há com uma tabela espelho criaria uma segunda
-- fonte da verdade para sincronizar. Quem verifica se o código existe é a
-- chamada do §4.3, em tempo de cadastro.
--
-- SAFETY, e a regra 4.8 do CLAUDE.md:
--
-- COLISÃO: não há como. Sem `UNIQUE` e sem backfill, nenhum dado de produção
-- faz esta migration falhar por conflito. As duas colunas nascem aqui, então
-- toda linha existente nasce com elas NULL.
--
-- VOLUME: o índice RESULTANTE é vazio (o predicado `hub_code IS NOT NULL` não
-- casa nenhuma linha no instante da criação), mas construí-lo NÃO é de graça:
-- um `CREATE INDEX ... WHERE` varre o heap inteiro para avaliar o predicado
-- linha a linha. O custo é O(tamanho de `campaigns`), e ele roda sob o
-- `statement_timeout` de 60s desta mesma transação.
--
-- ⚠️ A primeira versão deste comentário dizia "vazio com 0 ou 10 milhões de
-- linhas, não existe dado capaz de fazer isto falhar". Estava errado: confundia
-- o TAMANHO do índice com o CUSTO DE CONSTRUÍ-LO. A conclusão continua a mesma
-- — `campaigns` é tabela de campanhas, não de detecções, e está na ordem de
-- centenas de linhas —, mas o argumento não era esse, e o `shadow_migration_test`
-- do `scripts/deploy.sh` (que é a proteção que a 4.8 cita) aplica isto sobre uma
-- cópia real antes de qualquer deploy. É ele quem mede, não este comentário.
--
-- ADD COLUMN nullable sem DEFAULT é rewrite-free no PG 11+.
--
-- ⚠️ O `lock_timeout` limita a AQUISIÇÃO do lock, não a duração dele. Depois de
-- o primeiro `ALTER` pegar o AccessExclusive, tudo espera até o fim da
-- transação — inclusive o build do índice, que aqui não pode ser `CONCURRENTLY`
-- porque `CONCURRENTLY` não roda dentro de transação. O que o `lock_timeout`
-- evita é a DDL ficar pendurada na fila esperando um lock que não vem; quem
-- limita o tempo total é o `statement_timeout`.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE campaigns
    ADD COLUMN IF NOT EXISTS hub_code TEXT;

-- Quando o hub confirmou o recebimento desta campanha.
--
-- NULL = o hub ainda não sabe dela. É o que o job de reemissão lê: o emissor é
-- dispara-e-esquece, sem retentativa, e a carga em lote que era a rede de
-- segurança foi aposentada junto com este mesmo redesenho. Sem esta coluna, um
-- evento perdido viraria campanha que nunca chega ao hub, sem nada em tela
-- nenhuma denunciando.
ALTER TABLE campaigns
    ADD COLUMN IF NOT EXISTS hub_notified_at TIMESTAMPTZ;

-- O índice do job: ele pergunta "quem tem código e não tem confirmação?" a cada
-- 15 minutos. Sem índice, isso é varredura da tabela inteira quatro vezes por
-- hora. PARCIAL porque só essas linhas interessam — em regime, são zero, e o
-- índice ocupa praticamente nada.
CREATE INDEX IF NOT EXISTS idx_campaigns_hub_pendentes
    ON campaigns (created_at)
    WHERE hub_code IS NOT NULL AND hub_notified_at IS NULL;

COMMIT;
