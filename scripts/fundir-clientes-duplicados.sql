-- ╔══════════════════════════════════════════════════════════════════════════╗
-- ║  FUNDIR DOIS CADASTROS DA MESMA EMPRESA                                   ║
-- ╚══════════════════════════════════════════════════════════════════════════╝
--
-- Move TUDO que pertence ao cadastro duplicado para o cadastro que fica, e
-- apaga o duplicado. Nasceu em 2026-09-16, da contagem do §4.3b que destrava o
-- retrato periódico do E-Hub: três empresas estavam cadastradas duas vezes, e
-- o primeiro retrato as transformaria em inquilinos duplicados no portal.
--
-- ┌─ POR QUE ISTO É SQL E NÃO CHAMADA DE API ────────────────────────────────┐
-- │ O `client_id` de uma campanha é IMUTÁVEL no `PUT /v1/internal/campaigns` │
-- │ — e por uma razão real, escrita no handler: "client_id stays locked      │
-- │ because Step 3 is hydrated against the client's material library".      │
-- │ Mover campanha sem mover material quebra a campanha. Aqui os dois se     │
-- │ movem juntos, na mesma transação.                                        │
-- └──────────────────────────────────────────────────────────────────────────┘
--
-- ## QUEM FICA, E POR QUÊ NÃO É ESCOLHA LIVRE
--
-- Fica o cadastro que o hub JÁ APONTA (o `emonitorClientId` do cliente do hub,
-- que o relatório chama de "já ligado a"). Manter o outro exigiria também
-- reescrever o vínculo no Mongo do hub — duas cirurgias em vez de uma, em dois
-- bancos, sem transação comum entre eles.
--
-- Não importa qual dos dois tem mais campanhas: elas vão todas para o mesmo
-- lugar de qualquer forma.
--
-- ⚠️ **E não importa de que lado está o `hub_id`.** As duas metades da ponte
-- são carimbadas por caminhos diferentes e podem discordar — foi o caso do
-- Panvel em 2026-09-16. O passo 0 consolida o carimbo no sobrevivente, e o
-- resultado é que as duas metades passam a concordar.
--
-- ## COMO RODAR
--
-- 1. Preencha os dois ids no bloco `parametros` abaixo.
-- 2. Rode o arquivo INTEIRO. Ele abre transação, mostra o antes, move, mostra
--    o depois — e PARA, sem commitar.
-- 3. Leia os números. Se baterem, rode `COMMIT;`. Se não, `ROLLBACK;`.
--
-- ⚠️ Não há `COMMIT` neste arquivo, e a ausência é deliberada. Um script de
--    fusão que commita sozinho tira de você o único momento em que dá para
--    olhar o resultado antes de ele ser permanente.
--
-- ⚠️ Tire backup antes. O `scripts/deploy.sh` deste repositório faz backup
--    verificado antes de qualquer migration por causa dos incidentes de
--    2026-05-12 e 2026-05-17 — a mesma prudência vale aqui, e aqui não há
--    tripwire nenhum te protegendo.

BEGIN;

-- ╭──────────────────────────────────────────────────────────────────────────╮
-- │  PARÂMETROS — só isto se edita                                           │
-- ╰──────────────────────────────────────────────────────────────────────────╯
CREATE TEMP TABLE parametros ON COMMIT DROP AS
SELECT
  -- O que FICA (o "já ligado a" do relatório — o que o hub aponta):
  '00000000-0000-0000-0000-000000000000'::uuid AS fica,
  -- O que SOME (o "duplicaria" do relatório):
  '00000000-0000-0000-0000-000000000000'::uuid AS sai;

-- ╭──────────────────────────────────────────────────────────────────────────╮
-- │  GUARDAS — falham a transação inteira em vez de fazer estrago            │
-- ╰──────────────────────────────────────────────────────────────────────────╯
DO $$
DECLARE
  p RECORD;
  n_fica int;
  n_sai int;
  cnpj_fica text;
  cnpj_sai text;
  hub_fica text;
  hub_sai text;
BEGIN
  SELECT * INTO p FROM parametros;

  IF p.fica = p.sai THEN
    RAISE EXCEPTION 'fica e sai sao o MESMO id — nada a fundir';
  END IF;

  SELECT count(*) INTO n_fica FROM clients WHERE id = p.fica;
  SELECT count(*) INTO n_sai FROM clients WHERE id = p.sai;
  IF n_fica <> 1 THEN RAISE EXCEPTION 'o cliente que FICA (%) nao existe', p.fica; END IF;
  IF n_sai <> 1 THEN RAISE EXCEPTION 'o cliente que SOME (%) nao existe', p.sai; END IF;

  -- ⚠️ A guarda que importa. Fundir dois CNPJs diferentes junta duas empresas
  -- que não são a mesma, e isso não se desfaz com ROLLBACK depois do commit.
  SELECT regexp_replace(COALESCE(cnpj,''), '\D', '', 'g') INTO cnpj_fica FROM clients WHERE id = p.fica;
  SELECT regexp_replace(COALESCE(cnpj,''), '\D', '', 'g') INTO cnpj_sai FROM clients WHERE id = p.sai;
  IF cnpj_fica = '' OR cnpj_sai = '' THEN
    RAISE EXCEPTION 'um dos dois nao tem CNPJ — sem ele nao da para afirmar que sao a mesma empresa';
  END IF;
  IF cnpj_fica <> cnpj_sai THEN
    RAISE EXCEPTION 'CNPJs DIFERENTES (% vs %) — estes dois nao sao a mesma empresa', cnpj_fica, cnpj_sai;
  END IF;

  /* ⚠️ Só o caso genuinamente ambíguo aborta: os DOIS carimbados, com valores
   * DIFERENTES. Aí há dois clientes no hub disputando, e escolher um em
   * silêncio deixaria o outro apontando para um id apagado.
   *
   * A versão anterior desta guarda barrava também "o `hub_id` está no que SAI"
   * e chamava isso de ids invertidos. Estava errado, e o Panvel provou em
   * 2026-09-16: o hub apontava para um cadastro e o `hub_id` estava no outro.
   * As duas metades da ponte são carimbadas por caminhos diferentes — o
   * `emonitorClientId` pela carga de importação, o `hub_id` pelo
   * `client.upsert` — e com a comparação literal de CNPJ cada uma escolheu um
   * duplicado. Isso não é erro de digitação de quem roda o script: é o próprio
   * defeito que a fusão vem consertar, e o passo 0 abaixo o resolve. */
  SELECT hub_id INTO hub_fica FROM clients WHERE id = p.fica;
  SELECT hub_id INTO hub_sai  FROM clients WHERE id = p.sai;
  IF hub_fica IS NOT NULL AND hub_sai IS NOT NULL AND hub_fica <> hub_sai THEN
    RAISE EXCEPTION
      'os DOIS tem hub_id e sao diferentes (% e %) — ha dois clientes no hub para esta empresa, e juntar aqui deixaria um deles apontando para um id apagado. Resolva no hub primeiro.',
      hub_fica, hub_sai;
  END IF;

  IF hub_fica IS NULL AND hub_sai IS NOT NULL THEN
    RAISE NOTICE 'o hub_id (%) esta no cadastro que SAI — o passo 0 vai move-lo para o que FICA', hub_sai;
  END IF;

  RAISE NOTICE 'guardas ok: mesmo CNPJ (%), os dois existem', cnpj_fica;
END $$;

-- ╭──────────────────────────────────────────────────────────────────────────╮
-- │  ANTES                                                                   │
-- ╰──────────────────────────────────────────────────────────────────────────╯
SELECT 'ANTES' AS momento, c.id, c.name, c.cnpj, c.hub_id,
       (SELECT count(*) FROM campaigns           x WHERE x.client_id = c.id) AS campanhas,
       (SELECT count(*) FROM materials           x WHERE x.client_id = c.id) AS materiais,
       (SELECT count(*) FROM users               x WHERE x.client_id = c.id) AS usuarios,
       (SELECT count(*) FROM user_clients        x WHERE x.client_id = c.id) AS carteiras,
       (SELECT count(*) FROM client_station_pmm  x WHERE x.client_id = c.id) AS pmm,
       (SELECT count(*) FROM api_keys            x WHERE x.client_id = c.id) AS chaves,
       (SELECT count(*) FROM post_sale_reports   x WHERE x.client_id = c.id) AS pos_venda
  FROM clients c, parametros p
 WHERE c.id IN (p.fica, p.sai);

-- ╭──────────────────────────────────────────────────────────────────────────╮
-- │  A MUDANÇA — nove tabelas, que é o mapa COMPLETO                         │
-- │                                                                          │
-- │  As nove foram levantadas do `information_schema` contra o schema real,  │
-- │  não de memória: são todas as chaves estrangeiras que apontam para        │
-- │  `clients(id)`. A mesma consulta confirmou que NÃO existe coluna com      │
-- │  cara de `client_id` sem FK — ou seja, não há tabela escondida.           │
-- ╰──────────────────────────────────────────────────────────────────────────╯

-- 0) O `hub_id` — a ponte com o E-Hub — antes de qualquer outra coisa.
--
--    ⚠️ ANULAR no que sai ANTES de gravar no que fica, e nunca o contrário: a
--    coluna é UNIQUE (`clients_hub_id_key`) e os dois cadastros coexistem até o
--    DELETE lá no fim. Gravar primeiro violaria a unicidade e abortaria tudo.
--
--    COALESCE: se o que fica já tem o carimbo, ele permanece — a guarda acima
--    já garantiu que, se os dois têm, são o mesmo valor.
--
--    Sem este passo, fundir o Panvel apagaria o único cadastro que o E-monitor
--    sabia ligar ao hub, e o SSO de quem trabalha nele passaria a responder
--    `client_not_provisioned` — o erro do §8.1, que é justamente o que a ponte
--    existe para evitar.
--    ⚠️ CAPTURAR ANTES DE ANULAR. A primeira versao deste passo anulava o
--    `hub_id` do que sai e so depois tentava le-lo para gravar no que fica —
--    lendo, claro, o NULL que ela mesma acabara de escrever. A ponte sumia em
--    silencio, e o SSO de quem trabalha naquele cliente passaria a responder
--    `client_not_provisioned`. So o teste contra Postgres pegou.
CREATE TEMP TABLE ponte ON COMMIT DROP AS
SELECT COALESCE(
         (SELECT c.hub_id FROM clients c, parametros p WHERE c.id = p.fica),
         (SELECT c.hub_id FROM clients c, parametros p WHERE c.id = p.sai)
       ) AS hub_id;

UPDATE clients SET hub_id = NULL
 WHERE id = (SELECT sai FROM parametros) AND hub_id IS NOT NULL;

UPDATE clients SET hub_id = (SELECT hub_id FROM ponte)
 WHERE id = (SELECT fica FROM parametros);

-- 1) Usuários. O gatilho `trg_sync_user_primary_client` dispara neste UPDATE e
--    INSERE (usuario, fica) em `user_clients` com ON CONFLICT DO NOTHING. Ele
--    só acrescenta; quem remove o vínculo velho é o passo 3.
UPDATE users SET client_id = (SELECT fica FROM parametros)
 WHERE client_id = (SELECT sai FROM parametros);

-- 2) Carteiras secundárias (agência com mais de um cliente). Insere-e-ignora
--    porque `user_clients` é único em (user_id, client_id): quem já estava nos
--    dois colidiria num UPDATE cru.
INSERT INTO user_clients (user_id, client_id)
SELECT uc.user_id, p.fica FROM user_clients uc, parametros p
 WHERE uc.client_id = p.sai
ON CONFLICT DO NOTHING;

-- 3) E só agora os vínculos antigos saem — inclusive o principal que o passo 1
--    deixou para trás.
DELETE FROM user_clients WHERE client_id = (SELECT sai FROM parametros);

-- 4) Materiais ANTES das campanhas: é a biblioteca de que elas dependem, e a
--    razão pela qual a API proíbe mover campanha sozinha.
UPDATE materials SET client_id = (SELECT fica FROM parametros)
 WHERE client_id = (SELECT sai FROM parametros);

UPDATE campaigns SET client_id = (SELECT fica FROM parametros)
 WHERE client_id = (SELECT sai FROM parametros);

-- 5) PMM por emissora. Único em (client_id, station_id): se os dois cadastros
--    têm número para a MESMA emissora, o do que fica prevalece — ele é o que o
--    hub conhece, e inventar uma média seria pior que escolher.
INSERT INTO client_station_pmm (client_id, station_id, pmm_target)
SELECT p.fica, s.station_id, s.pmm_target FROM client_station_pmm s, parametros p
 WHERE s.client_id = p.sai
ON CONFLICT (client_id, station_id) DO NOTHING;

DELETE FROM client_station_pmm WHERE client_id = (SELECT sai FROM parametros);

-- 6) O resto move sem colisão — nenhuma unicidade envolve client_id.
UPDATE api_keys           SET client_id = (SELECT fica FROM parametros) WHERE client_id = (SELECT sai FROM parametros);
UPDATE post_sale_reports  SET client_id = (SELECT fica FROM parametros) WHERE client_id = (SELECT sai FROM parametros);
UPDATE webhook_deliveries SET client_id = (SELECT fica FROM parametros) WHERE client_id = (SELECT sai FROM parametros);
UPDATE webhook_failures   SET client_id = (SELECT fica FROM parametros) WHERE client_id = (SELECT sai FROM parametros);

-- 7) Agora o duplicado está vazio e o DELETE passa. Se ele FALHAR com violação
--    de chave estrangeira, é sinal de que apareceu uma tabela nova desde
--    2026-09-16 — a transação inteira aborta, nada se perde, e o conserto é
--    acrescentar a tabela aqui.
DELETE FROM clients WHERE id = (SELECT sai FROM parametros);

-- ╭──────────────────────────────────────────────────────────────────────────╮
-- │  DEPOIS — os números do que ficou têm de ser a SOMA dos dois de antes    │
-- ╰──────────────────────────────────────────────────────────────────────────╯
SELECT 'DEPOIS' AS momento, c.id, c.name, c.cnpj, c.hub_id,
       (SELECT count(*) FROM campaigns           x WHERE x.client_id = c.id) AS campanhas,
       (SELECT count(*) FROM materials           x WHERE x.client_id = c.id) AS materiais,
       (SELECT count(*) FROM users               x WHERE x.client_id = c.id) AS usuarios,
       (SELECT count(*) FROM user_clients        x WHERE x.client_id = c.id) AS carteiras,
       (SELECT count(*) FROM client_station_pmm  x WHERE x.client_id = c.id) AS pmm,
       (SELECT count(*) FROM api_keys            x WHERE x.client_id = c.id) AS chaves,
       (SELECT count(*) FROM post_sale_reports   x WHERE x.client_id = c.id) AS pos_venda
  FROM clients c, parametros p
 WHERE c.id = p.fica;

SELECT 'o duplicado ainda existe?' AS pergunta, count(*) AS resposta_tem_de_ser_zero
  FROM clients c, parametros p WHERE c.id = p.sai;

-- ╭──────────────────────────────────────────────────────────────────────────╮
-- │  NADA FOI GRAVADO AINDA.                                                 │
-- │                                                                          │
-- │  Conferiu? →  COMMIT;                                                    │
-- │  Estranhou? → ROLLBACK;                                                  │
-- ╰──────────────────────────────────────────────────────────────────────────╯
