-- 0060_post_sale_attachments_url.up.sql
-- Link externo com os anexos do fechamento (Drive, OneDrive, o que o time usar).
-- Ver docs/features/post-sale.md.
--
-- É do RELATÓRIO, não do bloco de campanha: o material anexado (contrato, PI,
-- áudio da peça) vale pro fechamento inteiro, e por campanha o admin repetiria
-- o mesmo link em todo bloco.
--
-- Guardamos só a URL. Nada de upload: o arquivo mora onde o time já trabalha,
-- e o E-monitor não vira storage de documento de terceiro.
--
-- DEFAULT '' em vez de NULL porque a ausência aqui não tem significado próprio
-- — "sem anexo" e "não preenchido" são a mesma coisa, e string vazia evita o
-- ponteiro nulo em todo consumidor.
--
-- Migration puramente estrutural (ADD COLUMN com default constante): não
-- depende de dados existentes, então não cai no risco da regra 4.8 do CLAUDE.md.
ALTER TABLE post_sale_reports
    ADD COLUMN attachments_url TEXT NOT NULL DEFAULT '';
