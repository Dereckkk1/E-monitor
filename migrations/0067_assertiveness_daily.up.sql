-- assertiveness_daily: fato diário da assertividade da plataforma — de tudo que
-- veiculou, quanto o matcher pegou sozinho vs quanto um operador teve que
-- digitar depois.
--
-- Por que uma tabela e não uma view: o cálculo é caro (cruza detections,
-- detection_campaigns, stream_health_events e materials, com um EXISTS por
-- veiculação manual) e o /management é um painel com filtros. Um job diário
-- recomputa a janela e o card lê um SUM.
--
-- Granularidade (dia, campanha, emissora, material): o material está na chave
-- de propósito. Foi exatamente esse eixo que revelou, na primeira medição
-- (jul/2026), que 26% de todo o miss da plataforma eram materiais "PULSO
-- SONORO" — um bug conhecido com fix já mergeado atrás de flag. Agregado só por
-- emissora, esse padrão fica invisível.
--
-- Volume: ~5-10k linhas/mês. Dado DERIVADO — pode ser recomputado do zero a
-- qualquer momento, por isso os FKs são ON DELETE CASCADE.
--
-- A definição canônica dos baldes vive em scripts/sql/assertividade.sql, que
-- passa a ser a ferramenta de VERIFICAÇÃO: roda a mesma conta ad-hoc e compara
-- com esta tabela quando alguém desconfiar do número.

CREATE TABLE IF NOT EXISTS assertiveness_daily (
    for_date         DATE NOT NULL,
    campaign_id      UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    station_id       UUID NOT NULL REFERENCES stations(id)  ON DELETE CASCADE,
    -- Sem FK: aponta pra materials OU commercials (o FK de detections foi
    -- derrubado na 0024 justamente por causa desse polimorfismo).
    commercial_id    UUID NOT NULL,

    -- Veiculações detectadas sozinhas (o acerto).
    auto             INT NOT NULL DEFAULT 0,
    -- Manuais que são erro nosso: stream no ar, material já no índice, worker
    -- monitorando, e não é redigitação.
    miss             INT NOT NULL DEFAULT 0,

    -- Manuais desculpadas, por motivo. Somadas ao miss dão o total de manuais.
    x_duplicate      INT NOT NULL DEFAULT 0,  -- já detectada; operador redigitou
    x_no_fingerprint INT NOT NULL DEFAULT 0,  -- material fingerprintado depois da tocada
    x_stream_down    INT NOT NULL DEFAULT 0,  -- stream fora do ar na hora
    x_unmonitored    INT NOT NULL DEFAULT 0,  -- nenhum sinal da emissora no dia (AMBÍGUO)

    computed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (for_date, campaign_id, station_id, commercial_id)
);

-- Leitura do card: SUM sobre um mês inteiro, opcionalmente filtrado por
-- campanha. A PK já cobre (for_date, campaign_id...), mas o card global filtra
-- só por data, daí o índice dedicado.
CREATE INDEX IF NOT EXISTS idx_assertiveness_date ON assertiveness_daily (for_date);
