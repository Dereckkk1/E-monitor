-- 0059_post_sale_overrides.up.sql
-- Duas colunas no bloco de campanha do pós-venda. Ver docs/features/post-sale.md.
--
-- 1. checking_edited distingue "o admin ainda não mexeu no Checking" de "o admin
--    apagou todas as linhas de propósito". Antes disso o wizard salvava o bloco
--    no passo 2 com a lista vazia, o backend entendia como remoção deliberada, e
--    o documento saía com TODAS as emissoras em "entregaram conforme o
--    planejado" — o Checking nunca listava ninguém.
--
--    Não deu pra inferir da própria coluna: '[]' é ambíguo entre os dois casos, e
--    o caso "removi todas" é legítimo (campanha em que só interessa o texto).
--
-- 2. kpi_overrides guarda o número que o admin digitou à mão quando o valor do
--    sistema não é o que vai ser cobrado (acordo fechado fora da plataforma).
--    Só as chaves presentes sobrescrevem; o resto continua vindo do /insights.
--    CPM nunca entra aqui: ele é derivado de valor ÷ impactos × 1000, então
--    guardá-lo criaria um número que contradiz os outros dois.
--
-- Migration puramente estrutural (ADD COLUMN com DEFAULT constante em tabela
-- nova, criada na 0057): não depende de dados existentes, então não cai no risco
-- da regra 4.8 do CLAUDE.md.
BEGIN;

ALTER TABLE post_sale_report_campaigns
    ADD COLUMN checking_edited BOOLEAN NOT NULL DEFAULT FALSE,
    -- {"valor_entregue": 1234.56, "impactos": 90000, "bonificacao": 0}
    ADD COLUMN kpi_overrides  JSONB   NOT NULL DEFAULT '{}';

-- Blocos que já existem foram gravados pelo fluxo antigo, onde toda lista vazia
-- era ambígua. Quem tem linha gravada teve edição de fato; quem não tem, não.
UPDATE post_sale_report_campaigns
   SET checking_edited = TRUE
 WHERE jsonb_array_length(COALESCE(checking_rows, '[]'::jsonb)) > 0;

COMMIT;
