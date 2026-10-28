package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestObterWebhookCliente_SemSegredo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	_, err := pool.Exec(ctx, `
		UPDATE clients SET webhook_url = 'https://cliente.example/hook', webhook_secret = 'zq9xSEGREDO123',
		       webhook_enabled = true, webhook_events = ARRAY['detection.confirmed']
		WHERE id = $1`, cli)
	require.NoError(t, err)

	h := handlers.NewWebhooksHandler(pool, catalog.NewClients(pool), nil)
	var tela struct {
		URL       string   `json:"webhook_url"`
		Enabled   bool     `json:"webhook_enabled"`
		Events    []string `json:"webhook_events"`
		HasSecret bool     `json:"has_secret"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetConfig, "/x", map[string]string{"id": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_webhook_cliente", map[string]any{"cliente_id": cli.String()})
	require.Equal(t, true, out["configurado"])
	require.Equal(t, tela.URL, out["url"])
	require.Equal(t, tela.Enabled, out["ativo"])
	require.Equal(t, tela.HasSecret, out["tem_segredo"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "obter_webhook_cliente", map[string]any{"cliente_id": cli.String()})
	require.NotContains(t, texto, "zq9x", "nem o prefixo do segredo sai")
}

func TestListarEntregasWebhook_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	for i, st := range []string{"delivered", "failed", "delivered"} {
		_, err := pool.Exec(ctx, `
			INSERT INTO webhook_deliveries (client_id, event_type, payload, status, attempt_count, created_at)
			VALUES ($1, 'detection.confirmed', '{"segredo":"payload-nao-sai"}'::jsonb, $2, 1, now() - make_interval(mins => $3))`,
			cli, st, i)
		require.NoError(t, err)
	}
	h := handlers.NewWebhooksHandler(pool, catalog.NewClients(pool), nil)
	var tela struct {
		Data []catalog.WebhookDelivery `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListDeliveries, "/x", map[string]string{"id": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_entregas_webhook", map[string]any{"cliente_id": cli.String()})
	require.EqualValues(t, len(tela.Data), pag(t, out)["total"])
	require.Equal(t, tela.Data[0].ID.String(), itens(t, out)[0]["id"])

	falhas := chamarTool(t, cfgTeste(pool), "listar_entregas_webhook", map[string]any{"cliente_id": cli.String(), "status": "failed"})
	require.EqualValues(t, 1, pag(t, falhas)["total"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "listar_entregas_webhook", map[string]any{"cliente_id": cli.String()})
	require.NotContains(t, texto, "payload-nao-sai")

	// last_error guarda o err.Error() do http.Client, que imprime a URL inteira
	// do cliente — inclusive um ?token= na query. A tool corta a query.
	_, err := pool.Exec(ctx, `UPDATE webhook_deliveries SET last_error = $1 WHERE client_id = $2`,
		`Post "https://cliente.example/hook?token=TOKEN-DO-CLIENTE&x=1": dial tcp: timeout`, cli)
	require.NoError(t, err)
	texto, _ = chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "listar_entregas_webhook", map[string]any{"cliente_id": cli.String()})
	require.NotContains(t, texto, "TOKEN-DO-CLIENTE")
	require.Contains(t, texto, "https://cliente.example/hook?…")
}

func TestListarChavesAPI_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	_, err := pool.Exec(ctx, `INSERT INTO api_keys (client_id, key_hash) VALUES ($1, repeat('c', 64))`, cli)
	require.NoError(t, err)

	h := handlers.NewAPIKeysHandler(pool)
	var tela []struct {
		ID        string `json:"id"`
		KeyMasked string `json:"key_masked"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/x", map[string]string{"clientID": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_chaves_api", map[string]any{"cliente_id": cli.String()})
	require.Len(t, itens(t, out), len(tela))
	require.Equal(t, tela[0].ID, itens(t, out)[0]["id"])
	require.Equal(t, tela[0].KeyMasked, itens(t, out)[0]["identificador"])
}

func TestMinhasNotificacoes_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	ini := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	fim := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Com déficit", ini, fim, est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Regra(t, ctx, pool, camp, tipo, est, ini, fim, 127, "06:00", "22:00", 1) // nada tocou: déficit todo dia

	h := &handlers.NotificationsHandler{Repo: catalog.NewNotifications(pool)}
	var tela catalog.NotificationsResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/admin/notifications", nil, principalTeste.UserID), &tela))
	require.NotEmpty(t, tela.Items)

	out := chamarTool(t, cfgTeste(pool), "minhas_notificacoes", map[string]any{})
	require.EqualValues(t, tela.UnreadCount, out["nao_lidas"])
	require.Len(t, itens(t, out), len(tela.Items))
	require.Equal(t, tela.Items[0].Key, itens(t, out)[0]["chave"])
}
