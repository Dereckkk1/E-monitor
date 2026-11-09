-- Reversão da 0074. Devolve a função de revogação à versão da 0072 (sem tocar nos pedidos) ANTES de
-- derrubar a tabela que a versão nova referencia.
CREATE OR REPLACE FUNCTION mcp_revogar_credenciais_do_usuario() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public
AS $$
BEGIN
    UPDATE mcp_tokens SET revoked_at = NOW() WHERE user_id = NEW.id AND revoked_at IS NULL;
    DELETE FROM mcp_oauth_codes WHERE user_id = NEW.id;
    RETURN NULL;
END;
$$;

DROP TABLE IF EXISTS mcp_acoes_pendentes;
