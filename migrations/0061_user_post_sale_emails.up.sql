-- 0061: opt-in do admin para receber cópia de TODO pós-venda enviado.
--
-- Irmã da receive_alert_emails (0037), com o default INVERTIDO: aquela nasceu
-- preservando um comportamento que já existia (todos os admins recebiam os
-- disparos diários), esta cria comportamento novo. Pós-venda de todo cliente na
-- caixa de todo admin é volume alto — ligar sozinho surpreenderia. Default
-- FALSE e sem backfill: só recebe quem marcar em /admin/users.
--
-- Só admin/operator é destinatário de qualquer forma (o CHECK
-- users_client_role_consistency da 0027 garante que viewer tem client_id, e a
-- query de destinatários filtra por role). A coluna existir na linha do viewer
-- é inócuo e evita um CHECK a mais.
--
-- Migration estrutural pura (ADD COLUMN com default constante): não lê dado
-- existente, não adiciona constraint sobre ele — fora do risco da regra 4.8 do
-- CLAUDE.md.
ALTER TABLE users ADD COLUMN IF NOT EXISTS
    receive_post_sale_emails BOOLEAN NOT NULL DEFAULT FALSE;
