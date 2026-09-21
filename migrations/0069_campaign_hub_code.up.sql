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
-- SAFETY, e por que esta migration NÃO cai na regra 4.8 do CLAUDE.md:
--
-- Pelo mesmo motivo ESTRUTURAL que a 0068 registra, não por otimismo. As duas
-- colunas são criadas NESTA migration, então toda linha existente nasce com
-- elas NULL; e o índice é PARCIAL em `hub_code IS NOT NULL`, ou seja, ele é
-- literalmente vazio no instante em que é criado — com 0 ou 10 milhões de
-- linhas em `campaigns`. Não existe conjunto de dados de produção capaz de
-- fazer isto falhar por colisão ou volume.
--
-- ADD COLUMN nullable sem DEFAULT é rewrite-free no PG 11+; o lock
-- AccessExclusive dura instantes. O lock_timeout evita que a DDL entre na fila
-- FIFO e prenda leitura de `campaigns` atrás de si durante o deploy, quando a
-- API antiga ainda serve tráfego.

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
CREATE INDEX IF NOT EXISTS campaigns_hub_pendentes
    ON campaigns (created_at)
    WHERE hub_code IS NOT NULL AND hub_notified_at IS NULL;

COMMIT;
