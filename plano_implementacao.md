# Sistema de Monitoramento de Veiculação de Comerciais em Rádios AM/FM via Streaming

## Plano Completo de Implementação

**Versão:** 1.0  
**Status:** Especificação técnica para execução completa  
**Propósito:** Servir como blueprint exaustivo para substituição integral do fornecedor atual de monitoramento, contendo todas as decisões arquiteturais, justificativas técnicas, especificações de algoritmos, esquemas de dados, contratos de API, estratégia de infraestrutura, plano de migração e metodologia de validação suficientes para que uma equipe (ou agente automatizado) execute o projeto do zero sem ambiguidades.

---

## 1. Visão Geral do Sistema

### 1.1 Contexto de Negócio

A empresa contrata atualmente um fornecedor externo que executa monitoramento de veiculação de comerciais publicitários em emissoras de rádio AM e FM brasileiras. O fornecedor mantém infraestrutura que se conecta aos links de streaming das emissoras vinte e quatro horas por dia, sete dias por semana, identifica automaticamente quando comerciais cadastrados são veiculados, registra o timestamp da veiculação e grava trecho de áudio contendo um minuto antes e um minuto depois da veiculação para fins de evidência junto ao cliente final.

O serviço atende às necessidades técnicas mas apresenta dois problemas críticos: custo elevado e qualidade insatisfatória de atendimento. A empresa decide internalizar a operação, eliminando a dependência externa e, no processo, ganhando a possibilidade de oferecer recursos diferenciados que o fornecedor atual não oferece.

### 1.2 Objetivos Funcionais

O sistema deve:

1. Monitorar simultaneamente até duzentas emissoras com campanhas publicitárias ativas, com base cadastral expansível para mais de mil e setecentas emissoras.
2. Detectar veiculação de comerciais em tempo quase real, com latência de confirmação inferior a dez segundos após o término do comercial e disponibilização da evidência completa em até noventa segundos.
3. Gravar evidência de áudio de qualidade audível contendo sessenta segundos antes e sessenta segundos depois de cada comercial detectado.
4. Suportar múltiplas versões de um mesmo comercial (cortes de quinze, trinta, quarenta e cinco e sessenta segundos), distinguindo corretamente qual corte foi veiculado.
5. Operar com taxa de falso positivo abaixo de um por cento e taxa de falso negativo abaixo de cinco por cento, validadas contra dados do fornecedor incumbente durante período de coexistência.
6. Permitir cadastro de novos comerciais com hot reload do índice em produção, sem necessidade de restart de qualquer serviço.
7. Expor API REST para integração com sistemas internos e webhook para notificação em tempo real de detecções.
8. Manter histórico de detecções e evidências por período mínimo de doze meses, com armazenamento frio a partir de trinta dias.

### 1.3 Não Objetivos

Para definir escopo:

1. Captura over the air via antena física não está no escopo. O sistema consome exclusivamente streams disponibilizados pelas emissoras via HTTP, Icecast, Shoutcast, HLS ou similar.
2. Reconhecimento de música, programa editorial, podcasts ou conteúdo livre não cadastrado não é objetivo. Apenas comerciais cadastrados são detectados.
3. Análise semântica do conteúdo veiculado (transcrição, sentimento, contagem de menções) não é objetivo nesta fase, podendo ser adicionada posteriormente sobre a infraestrutura criada.
4. Integração direta com sistemas de automação de tráfego das emissoras é explicitamente fora do escopo. O sistema é puramente passivo, observando o stream público.

### 1.4 Métricas de Sucesso

O projeto é considerado bem sucedido quando, em ambiente de produção:

1. Concordância de detecção com o fornecedor atual igual ou superior a noventa e oito por cento (medida sobre o conjunto comum de emissoras e comerciais durante período de coexistência).
2. Tempo médio de confirmação após término do comercial inferior a dez segundos no percentil noventa e cinco.
3. Disponibilidade do sistema (uptime de captura por emissora) igual ou superior a noventa e nove vírgula cinco por cento mensal.
4. Custo total de operação inferior ao custo atual do fornecedor em pelo menos quarenta por cento, considerando infraestrutura, armazenamento e equipe alocada.
5. Capacidade de adicionar nova emissora ao monitoramento em menos de quinze minutos de operação manual.

---

## 2. Glossário

**Fingerprint acústico:** representação compacta e robusta de um trecho de áudio, projetada para sobreviver a alterações como compressão, codificação e ruído, usada para identificar o áudio em outras gravações.

**Constellation map:** representação do espectrograma como um conjunto esparso de pontos correspondentes aos picos de magnitude local, conceito introduzido pelo Shazam em 2003.

**Peak pair (par de picos):** dois pontos do constellation map combinados em uma chave de hash, formando a unidade mínima de busca no algoritmo de fingerprinting.

**Histograma de delta:** técnica de matching que conta quantos hashes encontrados no stream possuem mesmo deslocamento temporal em relação à referência; o pico do histograma indica alinhamento e portanto match.

**Broadcast simulation:** processo de submeter o áudio master a uma cadeia de processamento que aproxima o que ele sofreria em uma emissora real (compressão multibanda, clipping, codec de baixo bitrate), gerando uma referência mais robusta para fingerprinting.

**Stream ingestor (worker):** processo dedicado a uma emissora que mantém conexão persistente com o link de streaming, decodifica o áudio e alimenta os pipelines de evidência e análise.

**Match state machine:** lógica que combina matches parciais ao longo do tempo para produzir confirmação ou rejeição de uma detecção, evitando falsos positivos por matches isolados.

**Cooldown:** período após uma detecção confirmada durante o qual o sistema ignora novos matches do mesmo comercial na mesma emissora, evitando duplicidade.

**LUFS:** Loudness Units relative to Full Scale, unidade padrão de medição de loudness perceptual (ITU-R BS.1770).

**SBR:** Spectral Band Replication, técnica usada em HE-AAC para reconstruir frequências altas a partir de informação codificada em frequências baixas, característica que distorce o espectro original.

**STFT:** Short-Time Fourier Transform, transformação que produz uma representação tempo-frequência do sinal de áudio.

**Embedding neural:** vetor de alta dimensionalidade produzido por uma rede neural treinada em áudio, usado como representação semântica robusta para comparação por similaridade.

---

## 3. Catálogo Completo de Riscos Técnicos

Esta seção enumera, com profundidade técnica, todos os fenômenos que podem comprometer a detecção. Cada item é numerado para referência cruzada com a seção de mitigações.

### 3.1 Riscos Originados na Cadeia de Áudio Broadcast

**R1. Compressão multibanda agressiva.** Praticamente toda emissora FM minimamente profissional roda o áudio através de processadores como Orban Optimod 8600/8700, Omnia 9/11 ou Wheatstone AirAura. Esses equipamentos aplicam compressão em quatro a seis bandas com razões de quatro para um até dez para um, ataque rápido (sub-milissegundo) e release rápido (vinte a duzentos milissegundos). O efeito é colapso quase total da faixa dinâmica e alteração significativa do envelope de magnitude por banda. Picos espectrais que existiam no master limpo deixam de ser dominantes; novos picos emergem em frequências que originalmente eram secundárias.

**R2. Clipping suave e distorção harmônica.** Após compressão, é comum aplicação de clipping suave (soft clipper) entre meio e três decibéis acima do limite, gerando harmônicos pares e ímpares que enriquecem artificialmente o espectro. Esses harmônicos não estão presentes no master e podem confundir algoritmos que dependem de identificação espectral exata.

**R3. Equalização de presença e brilho.** Processadores broadcast tipicamente acentuam a região de dois a oito quilohertz para "abrir" o som e gerar sensação de proximidade, e podem cortar graves abaixo de quarenta hertz. Isso desloca o centro de massa espectral.

**R4. Stereo enhancement.** Alargamento artificial da imagem estéreo é prática comum, alterando a relação L/R. Como nosso pipeline opera em mono, isso é parcialmente mitigado, mas a mistura mono final ainda sofre influência.

**R5. Pre-emphasis FM.** A norma brasileira (compatível com ITU-R BS.450) determina aplicação de pre-emphasis com constante de tempo de cinquenta microssegundos antes da modulação FM, com de-emphasis correspondente no receptor. No caminho via streaming, essa transformação geralmente já foi compensada antes do encoder de streaming, mas algumas emissoras streamam o sinal pós pre-emphasis, gerando boost em frequências altas.

**R6. Codificação de streaming em baixo bitrate.** O áudio é tipicamente codificado em MP3 a sessenta e quatro a cento e noventa e dois quilobits por segundo, AAC-LC a sessenta e quatro a cento e vinte e oito, ou HE-AAC v1 (AAC com SBR) a trinta e dois a sessenta e quatro. Codecs perceptuais descartam informação considerada inaudível, alterando o espectro de forma irreversível.

**R7. Spectral Band Replication.** Em HE-AAC, frequências acima de tipicamente seis a oito quilohertz não são codificadas diretamente; são reconstruídas no decoder a partir de parâmetros que descrevem a relação com as bandas baixas. O espectro reconstruído tem padrão sintético reconhecível e diverge do original. Algoritmos que dependem de picos em altas frequências falham nessa região.

**R8. Compressão temporal por encaixe de slot.** Algumas estações utilizam time stretching para encaixar comerciais em slots fixos, acelerando entre um e três por cento. Isso desloca todas as frequências proporcionalmente e altera a duração, quebrando alinhamento temporal exato.

**R9. Limitação de banda em AM.** Estações AM tipicamente filtram em cinco quilohertz (norma brasileira). Qualquer informação espectral acima disso é inexistente, então o algoritmo não pode depender de bandas altas para emissoras AM.

**R10. Variação entre emissoras.** Cada emissora tem cadeia de processamento ligeiramente diferente, com diferentes presets de processador, diferentes encoders de streaming, diferentes bitrates. Não existe configuração única que funcione bem para todas.

### 3.2 Riscos Originados no Transporte do Stream

**R11. Quedas de conexão.** Streams podem cair por minutos ou horas, especialmente em emissoras de interior com infraestrutura precária. A reconexão pode falhar repetidamente.

**R12. Buffer underrun e dropouts.** Mesmo com conexão estável, o servidor de streaming pode entregar áudio com pequenos cortes (gaps de centenas de milissegundos) por congestionamento de rede. Esses gaps quebram a continuidade temporal do áudio.

**R13. Detecção de crawler pela emissora.** Servidores de streaming profissionais (Shoutcast, Icecast, Wowza) podem detectar conexões persistentes de longa duração de um mesmo IP e degradar o serviço, baixar o bitrate, ou bloquear o IP. Algumas emissoras rotacionam URLs de stream por questão de balanceamento.

**R14. Mudança de URL de stream.** Emissoras podem alterar a URL de streaming sem aviso, geralmente em mudanças de fornecedor de CDN. O sistema precisa detectar e alertar.

**R15. Mudança de formato ou bitrate.** A mesma URL pode passar a entregar formato diferente (de MP3 para AAC), bitrate diferente, ou número de canais diferente. O ingestor precisa ser robusto a isso.

**R16. Latência variável.** O delay entre o áudio ao vivo no estúdio e o áudio no nosso decoder varia de cinco a trinta segundos dependendo do buffer do encoder, CDN e rede. Isso afeta sincronia de timestamp.

**R17. Drift entre relógio do servidor e timestamp do stream.** O áudio que chega não traz timestamp absoluto. Se calcularmos o tempo de veiculação pelo relógio do servidor no momento de chegada, sofremos com a latência variável de R16. Precisamos calcular timestamp baseado em posição no buffer e ajuste por latência conhecida da emissora.

### 3.3 Riscos Originados na Lógica de Detecção

**R18. Falso positivo por música ou jingle compartilhado.** Se o comercial contém trilha sonora de música conhecida ou usa vinheta/jingle de banco de áudio comum, qualquer outro programa que tocar a mesma música pode disparar match parcial.

**R19. Falso positivo por trecho curto repetitivo.** Comerciais com trechos curtos genéricos (sirenes, aplausos, transição padrão) podem dar match em outros conteúdos.

**R20. Falso positivo entre versões diferentes do mesmo comercial.** Cortes de quinze segundos costumam ser subset do corte de trinta segundos. Sem desambiguação, pode haver match duplicado.

**R21. Falso negativo por DJ falando por cima.** Em certos formatos, especialmente rádio popular, o locutor fala por cima do início ou final do comercial. Isso adiciona ruído correlacionado que reduz a fração de hashes coincidentes.

**R22. Falso negativo por sobreposição com vinheta da emissora.** Algumas emissoras emitem identificação sonora ("vinheta de PA") sobre a parte inicial ou final dos comerciais, causando o mesmo tipo de degradação.

**R23. Falso negativo por compressão excessiva ou versão remasterizada.** Se a emissora reprocessou o comercial com compressão muito mais agressiva que a simulada na referência, o fingerprint pode não casar.

**R24. Threshold único insuficiente.** Threshold fixo de match score gera muito falso negativo em emissoras de processamento agressivo e muito falso positivo em emissoras de processamento leve. É necessário threshold adaptativo por emissora.

**R25. Coincidência de hash em janela curta.** Em qualquer janela de quatro a oito segundos, é estatisticamente possível que o stream gere alguns hashes coincidentes com a referência por puro acaso. Sem o histograma de delta, isso vira falso positivo.

### 3.4 Riscos Operacionais e Sistêmicos

**R26. Memory leak em workers de longa duração.** Processos rodando vinte e quatro horas por dia tendem a acumular memória por leaks sutis (em libs C, em conexões não fechadas, em buffers não liberados), levando a OOM e crash em horário de produção.

**R27. Estagnação de stream sem detecção.** O stream pode estar conectado mas não entregar áudio (servidor enviando keepalive sem dados), e o ingestor não percebe sem health check ativo.

**R28. Cadastro de comercial novo sem fingerprint.** Comercial entra em campanha às seis da manhã, mas o fingerprint só é gerado às nove. Três horas de veiculação são perdidas.

**R29. Crescimento do índice impactando latência.** Se o índice de hashes crescer descontroladamente (catálogo histórico de comerciais, não apenas ativos), latência de busca por hash aumenta.

**R30. Crescimento do volume de evidências.** Duzentas emissoras com cinquenta detecções por dia, cada evidência com dois minutos de áudio em cento e vinte e oito quilobits por segundo, geram aproximadamente vinte gigabytes por dia, sete terabytes por ano. Sem política de tiering, o custo de storage explode.

**R31. Falha em hot reload.** Atualização do índice durante operação pode causar inconsistência momentânea (matches perdidos durante a recarga).

**R32. Falha de relógio do sistema.** Drift do relógio do servidor causa timestamps imprecisos nas detecções, gerando questionamento do cliente.

**R33. Bloqueio em massa por concentração de IPs.** Se todos os duzentos workers rodam no mesmo IP de saída, é fácil para uma operadora de CDN bloquear ou rate limitar a concentração.

**R34. Perda de evidência por falha no pipeline de gravação.** Detecção é confirmada mas o clip de evidência não é salvo (falha de disco, erro de upload para storage), gerando detecção sem prova.

**R35. Contaminação cruzada entre versões.** Duas versões do mesmo comercial podem dar match no mesmo timestamp; o sistema precisa decidir qual reportar.

**R36. Alteração silenciosa do master.** Cliente substitui um comercial por uma nova versão mantendo o mesmo identificador. Sem detecção dessa mudança, fingerprints velhos rodam contra áudio novo.

**R37. Falso negativo durante cooldown.** Se um comercial é veiculado duas vezes em sequência rápida (situação rara mas existe em programetes), o cooldown da primeira detecção pode mascarar a segunda.

---

## 4. Estratégia Geral de Mitigação

Cada risco da seção três é endereçado abaixo. Detalhamento técnico das soluções está em seções específicas referenciadas.

### 4.1 Mitigações de Riscos da Cadeia de Áudio (R1-R10)

A solução transversal é o **broadcast simulation** aplicado ao master antes da geração do fingerprint. Em vez de fingerprintar o áudio limpo da agência e tentar encontrá-lo no áudio degradado do ar, geramos uma versão da referência que já passou por uma cadeia de processamento similar à de uma emissora típica, e fingerprintamos essa versão. Detalhamento exato dos parâmetros está na seção sete.

Para R5 (pre-emphasis), aplicamos high-pass filter a cem hertz e trabalhamos no domínio mono, descartando informação que poderia variar por canal. Para R7 (SBR), limitamos a banda útil de análise a oito quilohertz para todas as emissoras, ignorando informação acima dessa faixa onde a reconstrução sintética introduz mais erro do que ajuda. Para R8 (time stretching), executamos matching multi-rate, testando o stream também em zero vírgula noventa e sete e um vírgula zero três da velocidade nominal. Para R9 (limitação AM), o limite de oito quilohertz já é compatível, e para AM specificamente reduzimos para quatro vírgula cinco quilohertz. Para R10 (variação entre emissoras), implementamos calibração adaptativa por emissora (seção 9.4).

### 4.2 Mitigações de Riscos de Transporte (R11-R17)

Para R11 (quedas de conexão), implementamos reconexão automática com backoff exponencial e jitter, mantendo estado do worker e alertando após N falhas consecutivas. Para R12 (dropouts), o algoritmo de matching é por natureza robusto a gaps, pois opera em janelas deslizantes; gaps de até dois segundos são absorvidos sem impacto. Para R13 (detecção de crawler), o ingestor usa User-Agent de cliente real (rotacionado entre VLC, Foobar2000, navegadores comuns), implementa jitter na reconexão, e a infraestrutura mantém pool de IPs distribuídos (seção dezessete).

Para R14 (mudança de URL), implementamos health check ativo que verifica entrega de áudio a cada trinta segundos; se URL principal falhar persistentemente, o sistema tenta variantes conhecidas (algumas emissoras mantêm múltiplas URLs em paralelo) e alerta operação para revisão manual.

Para R15 (mudança de formato), o ingestor usa ffmpeg como decoder universal, que adapta automaticamente. Para R16 (latência variável), medimos latência de cada emissora regularmente comparando timestamp NTP do nosso servidor com hora reportada pela emissora em jingles de hora certa quando disponível, ou via boletins de notícia em horário fixo. Para R17 (drift), o timestamp da detecção é calculado pela posição da janela analisada dentro do buffer relativo ao tempo de início da conexão atual, ajustado pela latência conhecida da emissora.

### 4.3 Mitigações de Riscos de Detecção (R18-R25)

Para R18 e R19 (falsos positivos por trilha ou trechos repetitivos), exigimos **cobertura temporal** do match, não apenas pico de hashes em uma janela curta. Um comercial de trinta segundos só é confirmado se houver hashes alinhados em pelo menos vinte de seus trinta segundos. Detalhe na seção dez.

Para R20 (falsos positivos entre versões), implementamos **regra de desambiguação**: quando dois cortes do mesmo comercial dão match no mesmo intervalo temporal e na mesma emissora, escolhe-se a versão de maior cobertura temporal (a versão mais longa que foi efetivamente veiculada).

Para R21 e R22 (falsos negativos por sobreposição de voz), o algoritmo é tolerante a degradação parcial; um match com setenta por cento dos hashes esperados ainda confirma. Para casos limítrofes, ativamos a **camada de verificação neural** (seção onze) que opera sobre embeddings e é mais robusta a ruído correlacionado.

Para R23 (versão remasterizada divergente), o sistema permite cadastro de múltiplos fingerprints por comercial, gerados com diferentes intensidades de broadcast simulation (versão "leve", "média" e "agressiva"). Match em qualquer uma confirma a detecção.

Para R24 (threshold único), implementamos calibração por emissora: durante os primeiros sete dias de monitoramento de uma emissora nova, o sistema coleta estatísticas de match score em janelas conhecidamente sem comerciais e usa esses dados para definir threshold específico que maximiza precision e recall. Detalhe na seção 9.4.

Para R25 (coincidência aleatória), o histograma de delta é a defesa primária. Apenas matches concentrados em um único bin de delta confirmam veiculação real. Coincidências aleatórias se distribuem uniformemente.

### 4.4 Mitigações de Riscos Operacionais (R26-R37)

Para R26 (memory leak), implementamos **restart preventivo** dos workers a cada vinte e quatro horas em horário de baixo tráfego (entre três e cinco da manhã), em janelas escalonadas para que apenas um por cento dos workers reinicie simultaneamente. Cada worker é supervisionado por systemd ou Kubernetes que garante restart automático em caso de crash.

Para R27 (estagnação), o ingestor monitora bytes recebidos por intervalo. Se não receber áudio por trinta segundos consecutivos, força reconexão. Se três reconexões falharem, alerta operação.

Para R28 (cadastro sem fingerprint), o pipeline de geração de fingerprint é disparado automaticamente no momento do upload do master, e o índice é atualizado em produção em até cinco minutos. Comerciais com fingerprint pendente entram em fila prioritária.

Para R29 (crescimento do índice), apenas fingerprints de comerciais ativos (com campanha em andamento ou prevista para próximos sete dias) ficam no índice quente em memória. Fingerprints de comerciais inativos ficam em armazenamento frio (Postgres) e são recarregados sob demanda quando a campanha é reativada.

Para R30 (crescimento de evidências), implementamos **tiering automático**: trinta dias quentes em SSD local, depois migração para Cloudflare R2 ou S3 Glacier Instant Retrieval. Política de retenção permanente conforme regras do contrato com cliente.

Para R31 (falha em hot reload), o índice em memória é atualizado de forma copy-on-write: nova versão do índice é construída em paralelo, validada com testes de regressão automáticos contra um conjunto fixo de áudios de teste, e só então substitui a versão antiga via swap atômico de ponteiro.

Para R32 (drift de relógio), todos os servidores rodam chrony sincronizando com pool NTP brasileiro (a.st1.ntp.br, b.st1.ntp.br, c.st1.ntp.br) com monitoramento de drift; alerta dispara se drift exceder cinquenta milissegundos.

Para R33 (bloqueio por concentração), distribuímos workers entre pelo menos três datacenters distintos e múltiplos IPs por servidor. Cada emissora é atribuída a um IP de saída específico, e essa atribuição é estável ao longo do tempo (a emissora vê sempre o mesmo cliente).

Para R34 (perda de evidência), o pipeline de evidência é resiliente: se upload para R2 falhar, o clip fica em fila local persistente até retry bem-sucedido; alerta dispara se a fila acumular mais de dez itens.

Para R35 (contaminação entre versões), aplicamos a regra de desambiguação descrita em R20.

Para R36 (alteração silenciosa do master), calculamos hash SHA-256 do arquivo master no momento do upload e comparamos sempre que for usado. Mudança no hash dispara regeneração de fingerprint.

Para R37 (cooldown mascarando segunda veiculação), o cooldown é por par (comercial, emissora) e dura apenas o tempo de duração do comercial mais cinco segundos. Veiculações consecutivas separadas por mais que esse tempo são detectadas independentemente.

---

## 5. Arquitetura de Alto Nível

### 5.1 Visão de Componentes

O sistema é composto por sete serviços lógicos, podendo coabitar em servidores compartilhados na fase inicial e serem separados conforme escala demanda:

**Catalog Service.** Gerencia cadastro de emissoras, comerciais, campanhas e clientes. Fonte da verdade para configuração. Persiste em Postgres.

**Fingerprint Service.** Pipeline em batch que processa comerciais novos: aplica broadcast simulation, gera fingerprint, escreve no Postgres e dispara hot reload do índice em produção.

**Stream Ingestor.** Conjunto de workers (um por emissora) que mantém conexão persistente com o link de streaming, decodifica, mantém buffers de evidência (AAC) e análise (PCM 16kHz mono), e empurra janelas de análise para o Match Engine.

**Match Engine.** Recebe janelas de áudio dos ingestors, gera hashes, consulta o índice e roda o algoritmo de matching. Mantém state machine por par (emissora, comercial) e emite eventos de detecção confirmada.

**Index Service.** Mantém em memória o conjunto de fingerprints ativos. Carrega do Postgres na inicialização e suporta hot reload disparado pelo Fingerprint Service. Em deployment monolítico, é uma estrutura compartilhada dentro do mesmo processo do Match Engine.

**Evidence Service.** Recebe eventos de detecção, extrai do buffer do ingestor o trecho de [t_inicio − 60s, t_fim + 60s], encoda em AAC 128kbps, faz upload para storage de evidência (Cloudflare R2) e atualiza registro no Postgres.

**API Gateway.** Expõe endpoints REST para clientes internos (frontend, integrações) e para webhooks. Autenticação via JWT, rate limiting, audit log.

### 5.2 Diagrama de Fluxo de Dados

```
                    ┌──────────────────────┐
                    │   Catalog Service    │
                    │  (Postgres-backed)   │
                    └──────────┬───────────┘
                               │ cadastro de comercial
                               ▼
                    ┌──────────────────────┐
                    │ Fingerprint Service  │
                    │ (Python, async job)  │
                    │  - broadcast sim     │
                    │  - peak picking      │
                    │  - hash generation   │
                    └──────────┬───────────┘
                               │ insere/atualiza
                               ▼
                    ┌──────────────────────┐
                    │   Postgres + Redis   │
                    │  (índice persistido) │
                    └──────────┬───────────┘
                               │ hot reload event (NATS)
                               ▼
                    ┌──────────────────────┐
                    │    Index Service     │ ◀──┐
                    │  (in-memory, Go)     │    │
                    └──────────┬───────────┘    │
                               │ consulta hash  │
                               ▼                │
┌──────────┐  janela    ┌──────────────────────┐│
│  Stream  │ análise    │     Match Engine     ││
│ Ingestor │ ─────────▶ │    (Go, async)       ││
│  (Go)    │            │  - histograma delta  ││
│          │  buffer    │  - state machine     ││
│ ffmpeg   │ evidência  │  - neural verifier   ││
└─────┬────┘            └──────────┬───────────┘│
      │                            │ match      │
      │                            │ confirmado │
      │                            ▼            │
      │                 ┌──────────────────────┐│
      │  request clip   │   Evidence Service   ││
      ◀─────────────────│  (Go + ffmpeg)       ││
      │                 │  - extrai 2 min       ││
      │  clip           │  - encoda AAC        ││
      ├────────────────▶│  - upload R2          ││
      │                 └──────────┬───────────┘│
      │                            │            │
      │                            ▼            │
      │                 ┌──────────────────────┐│
      │                 │  Postgres + R2       ││
      │                 │  (detection record)  ││
      │                 └──────────┬───────────┘│
      │                            │            │
      │                            ▼            │
      │                 ┌──────────────────────┐│
      │                 │   API + Webhooks     │┘
      │                 │  (Go, REST)          │
      │                 └──────────────────────┘
```

### 5.3 Comunicação entre Componentes

**Stream Ingestor → Match Engine:** in-process via canal Go (mesmo binário) na fase inicial; via gRPC streaming em deployment distribuído. A janela de análise é PCM float32 16kHz mono de 4 segundos, com overlap de 2 segundos entre janelas consecutivas.

**Match Engine → Evidence Service:** evento via NATS subject `detections.confirmed`, payload JSON contendo station_id, commercial_id, match_start_ms, match_end_ms, confidence.

**Fingerprint Service → Index Service:** evento via NATS subject `index.reload`, payload contendo lista de commercial_ids adicionados/removidos. Index Service atualiza in-memory map.

**API Gateway → Catalog Service:** in-process na fase inicial.

**Webhooks externos:** disparados pelo API Gateway após confirmação de evidência uploadada com sucesso. Retry com backoff exponencial até cinco tentativas.

### 5.4 Modelo de Deployment

**Fase 1 (até trinta emissoras):** monolito Go contendo Stream Ingestor, Match Engine, Index Service, Evidence Service e API rodando em um único servidor dedicado. Postgres em servidor separado. Fingerprint Service em container Python no mesmo servidor de aplicação.

**Fase 2 (trinta a cem emissoras):** separação de Stream Ingestors em múltiplos servidores (cada um cuidando de até cinquenta emissoras), Match Engine ainda monolítico mas escalado verticalmente. Postgres com réplica de leitura.

**Fase 3 (cem a duzentas emissoras):** Match Engine particionado por hash do station_id (cada instância cuida de um subset). Index Service replicado em todas as instâncias. NATS cluster para event bus. Postgres com alta disponibilidade (Patroni ou managed).

---

## 6. Stack Tecnológico

### 6.1 Stack Definitiva

| Componente | Tecnologia | Justificativa |
|------------|-----------|---------------|
| Workers de stream | Go 1.22+ | Concorrência via goroutines escala muito além de Python ou Node para I/O persistente. Compilação estática facilita deploy. Performance previsível. |
| Pipeline de fingerprint | Python 3.11+ | Ecossistema científico (numpy, scipy, librosa) é incomparável. Geração é batch, não bottleneck. Reuso de audfprint. |
| Match engine | Go 1.22+ | Mesmo binário dos workers, latência baixa, tipos estáticos para garantir corretude do hash matching. |
| Verificador neural | Python 3.11+ + ONNX Runtime | Modelo CLAP exportado para ONNX, servido em Python via FastAPI ou em Go via onnxruntime-go. |
| Banco transacional | PostgreSQL 16 | Padrão indústria, suporte a JSONB para metadata flexível, particionamento nativo para tabela de detecções. |
| Cache / índice secundário | Redis 7 | Hot reload coordenado, cache de configuração. |
| Object storage | Cloudflare R2 | Sem egress fee, custo baixíssimo (uns 15 USD/TB/mês), API S3-compatible. |
| Event bus | NATS 2.10+ | Latência baixa, configuração simples, suporte a JetStream para persistência. |
| Decoder de áudio | ffmpeg 6.0+ | Padrão indústria, suporta todos os formatos de streaming, estável em execução de longa duração quando rodado como subprocess controlado. |
| Container | Docker | Padrão. |
| Orquestração fase 1-2 | Docker Compose | Simplicidade para escala atual. |
| Orquestração fase 3 | Kubernetes (k3s) | Necessário apenas se separação de workers exigir orquestração ativa. |
| Monitoramento | Prometheus + Grafana | Padrão para métricas. |
| Log agregado | Loki | Integra naturalmente com Grafana. |
| Alerting | Alertmanager + Slack/Discord/PagerDuty | Padrão. |
| Cloud primária | Hetzner (servidores dedicados) | Custo de banda muito menor que AWS/GCP, essencial para workload de streaming 24/7. |
| Cloud secundária | OVH ou Contabo | Distribuição de IPs de saída, redundância. |

### 6.2 Justificativas Detalhadas

**Por que Go nos workers e não Rust ou Python:**

Python foi descartado para workers porque o GIL inviabiliza concorrência real, e duzentas conexões persistentes com processamento contínuo causam contenção. Mesmo com asyncio, a parte de processamento numérico precisa sair do event loop para threads ou processos, complicando arquitetura.

Rust foi considerado e é tecnicamente superior para o caso, mas o tempo de desenvolvimento é tipicamente 1.5x a 2x maior em projeto greenfield com equipe não especializada. Para um sistema que precisa ir a produção rápido e cuja gargalo dominante é I/O de rede e ffmpeg subprocess (não CPU em código nativo), Go entrega 90% do que Rust entregaria com metade do esforço.

Go ganha por: goroutines triviais para concorrência alta, ecosystem maduro para HTTP streaming, garbage collector com latência tunável (GOGC), tooling de profiling (pprof) excelente, deploy de binário estático único.

**Por que Python no pipeline de fingerprint:**

A geração de fingerprint é batch. Um comercial novo é processado uma vez quando entra. Não há requisito de latência sub-segundo. Em compensação, a análise espectral, peak picking e geração de hashes têm implementações maduras em Python com numpy/scipy, e o audfprint (referência canônica do Dan Ellis em Columbia) está em Python. Reescrever isso em Go seria reinventar a roda sem ganho.

**Por que PostgreSQL e não MongoDB ou DynamoDB:**

Os dados são relacionais por natureza (campanhas têm comerciais, comerciais têm fingerprints, detecções referenciam tudo). Postgres dá joins eficientes, integridade referencial, JSONB para flexibilidade quando necessária, particionamento nativo da tabela de detecções por mês. Volume não é alto o suficiente para justificar NoSQL.

**Por que Hetzner e não AWS:**

Workload de streaming 24/7 é dominado por banda. AWS cobra ~0.09 USD/GB de egress, Hetzner inclui 1 PB grátis no servidor dedicado. Para 200 streams a ~150 GB/dia inbound (negligenciável), e talvez 10 GB/dia de egress (evidências para clientes), a economia anual é da ordem de 5 a 10 mil USD. Servidor dedicado AX52 (16 cores Ryzen, 64 GB RAM, 2 TB NVMe) custa ~70 USD/mês. AWS equivalente custaria 10x mais.

**Por que Cloudflare R2 e não S3 para evidências:**

R2 não cobra egress. Como evidências são consultadas pelos clientes (download de áudio de comprovação), egress acumula rápido. R2 entrega o mesmo SLA prático com custo dramaticamente menor.

**Por que NATS e não Kafka ou RabbitMQ:**

Volume de eventos é baixo (estimativa: 200 emissoras × 50 detecções/dia = 10 mil eventos/dia, ~0.1 evento/segundo). Kafka é overkill, RabbitMQ é mais pesado operacionalmente. NATS tem footprint mínimo, latência sub-milissegundo, e JetStream cobre necessidade de persistência quando precisar.

### 6.3 Bibliotecas Externas Específicas

**Fingerprinting (Python):**
- `numpy >= 1.26` para arrays e FFT
- `scipy >= 1.11` para sinais (spectrogram, find_peaks)
- `librosa >= 0.10` para utilidades de carregamento de áudio
- Inspiração algorítmica: `audfprint` (https://github.com/dpwe/audfprint), MIT license. Não usado diretamente como lib mas como referência de implementação.

**Stream ingest (Go):**
- `os/exec` para controle de subprocess ffmpeg
- `golang.org/x/sync/errgroup` para coordenação de goroutines
- `github.com/nats-io/nats.go` para cliente NATS
- `github.com/jackc/pgx/v5` para Postgres
- `github.com/redis/go-redis/v9` para Redis

**Match engine (Go):**
- `github.com/edsrzf/mmap-go` para memory-mapped index opcional
- `gonum.org/v1/gonum` para utilities matemáticas

**Verificação neural:**
- Modelo: CLAP (Contrastive Language-Audio Pretraining) da LAION, exportado para ONNX
- Runtime: `onnxruntime` (Python) ou `github.com/yalue/onnxruntime_go` (Go)
- Alternativa: PANNs (Pretrained Audio Neural Networks) se CLAP for muito grande

**Storage e cloud:**
- `github.com/aws/aws-sdk-go-v2` (compatível com R2 via endpoint customizado)

**Observabilidade:**
- `github.com/prometheus/client_golang` para métricas
- `go.uber.org/zap` para logging estruturado


---

## 7. Pipeline de Geração de Fingerprint (Referência)

Este pipeline roda toda vez que um comercial novo é cadastrado, ou quando o master de um comercial existente é atualizado, ou quando o sistema decide regenerar fingerprints (por exemplo, após mudança de parâmetros de calibração).

### 7.1 Fluxo Geral

```
Master (WAV/MP3) 
   │
   ▼
[Validação] ─── verificar formato, calcular SHA-256
   │
   ▼
[Normalização inicial] ─── decode para PCM 48kHz estéreo float32
   │
   ▼
[Broadcast Simulation v1: leve] ─── compressão moderada + AAC 96kbps
[Broadcast Simulation v2: média] ─── compressão agressiva + AAC 64kbps
[Broadcast Simulation v3: pesada] ─── compressão muito agressiva + HE-AAC 48kbps
   │
   ▼
[Para cada versão simulada]
   ▼
[Pré-processamento de análise] ─── downsample 16kHz mono + HPF 100Hz + normalização LUFS
   │
   ▼
[STFT + Constellation Map] ─── janela 4096, hop 2048
   │
   ▼
[Peak Picking] ─── 2D max filter, top-K por segundo
   │
   ▼
[Hash Generation] ─── peak pairs com target zone
   │
   ▼
[Persistência] ─── Postgres (fingerprint_hashes) + Redis (hot index)
   │
   ▼
[Disparar hot reload] ─── NATS event
```

### 7.2 Comandos Concretos

**Etapa 1 - Validação e normalização inicial:**

```bash
# Calcula SHA-256 do master para tracking
sha256sum master.wav > master.wav.sha256

# Decodifica para WAV intermediário padronizado
ffmpeg -i master.wav -ar 48000 -ac 2 -c:a pcm_f32le -y master_norm.wav
```

**Etapa 2 - Broadcast Simulation (três versões):**

Versão **leve** (simula emissora com processamento moderado, ex: rádio classical/cultural):

```bash
ffmpeg -i master_norm.wav \
  -af "loudnorm=I=-16:TP=-1.5:LRA=11,acompressor=threshold=-20dB:ratio=3:attack=10:release=200" \
  -c:a aac -b:a 96k -ar 44100 -y broadcast_light.aac
```

Versão **média** (simula FM popular típica, ex: maioria das emissoras comerciais):

```bash
ffmpeg -i master_norm.wav \
  -af "loudnorm=I=-14:TP=-1:LRA=6,acompressor=threshold=-18dB:ratio=4:attack=5:release=80,acompressor=threshold=-12dB:ratio=6:attack=2:release=40,alimiter=limit=-0.3" \
  -c:a aac -b:a 64k -ar 44100 -y broadcast_medium.aac
```

Versão **pesada** (simula FM muito agressiva ou AM com baixa fidelidade):

```bash
ffmpeg -i master_norm.wav \
  -af "loudnorm=I=-12:TP=-0.5:LRA=4,acompressor=threshold=-22dB:ratio=6:attack=2:release=30,acompressor=threshold=-15dB:ratio=8:attack=1:release=15,acompressor=threshold=-8dB:ratio=10:attack=0.5:release=10,alimiter=limit=-0.1" \
  -c:a libfdk_aac -profile:a aac_he -b:a 48k -ar 44100 -y broadcast_heavy.aac
```

**Etapa 3 - Pré-processamento de análise (aplicado em cada versão simulada):**

```bash
ffmpeg -i broadcast_medium.aac \
  -ar 16000 -ac 1 \
  -af "highpass=f=100,loudnorm=I=-23:TP=-2:LRA=11" \
  -c:a pcm_f32le -y analysis_medium.wav
```

A saída é PCM float32 16 kHz mono pronta para análise espectral. O mesmo pré-processamento será aplicado ao stream em produção (seção 8.4).

### 7.3 Algoritmo de Geração de Fingerprint

Implementação em Python (referência de pseudocódigo, deve ser consolidada em módulo `fingerprint/generator.py`):

```python
import numpy as np
from scipy.signal import stft
from scipy.ndimage import maximum_filter

# Parâmetros do algoritmo (CONSTANTES DO SISTEMA)
SAMPLE_RATE = 16000
WINDOW_SIZE = 4096          # 256ms
HOP_SIZE = 2048             # 128ms (overlap 50%)
N_FFT = 4096
PEAK_NEIGHBORHOOD_F = 20    # bins de frequência
PEAK_NEIGHBORHOOD_T = 20    # frames de tempo
PEAK_AMPLITUDE_PERCENTILE = 75  # threshold relativo
TARGET_ZONE_T_MIN = 1       # frames (~128ms)
TARGET_ZONE_T_MAX = 16      # frames (~2s)
TARGET_ZONE_F = 50          # bins
FAN_OUT = 5                 # quantos pares por anchor
FREQ_BITS = 9               # 512 bins úteis (2.6kHz a 8kHz aprox)
FREQ_MIN_BIN = 25           # ignora frequências abaixo de ~100Hz
FREQ_MAX_BIN = 2048         # limita a 8kHz

def generate_fingerprint(audio: np.ndarray, sample_rate: int = SAMPLE_RATE) -> list[tuple[int, int]]:
    """
    Retorna lista de (hash_uint32, time_offset_frames).
    """
    assert sample_rate == SAMPLE_RATE
    
    # STFT
    f, t, Zxx = stft(audio, fs=sample_rate, nperseg=WINDOW_SIZE, 
                     noverlap=WINDOW_SIZE - HOP_SIZE, return_onesided=True)
    magnitude = np.abs(Zxx)
    
    # Limita banda útil
    magnitude = magnitude[FREQ_MIN_BIN:FREQ_MAX_BIN, :]
    
    # Log-magnitude para realce de picos
    log_mag = np.log1p(magnitude)
    
    # Peak picking via maximum filter 2D
    neighborhood = np.ones((PEAK_NEIGHBORHOOD_F, PEAK_NEIGHBORHOOD_T))
    local_max = maximum_filter(log_mag, footprint=neighborhood) == log_mag
    
    # Threshold por percentil
    threshold = np.percentile(log_mag, PEAK_AMPLITUDE_PERCENTILE)
    peaks_mask = local_max & (log_mag > threshold)
    
    # Coleta picos como (freq_bin, time_frame)
    freq_bins, time_frames = np.where(peaks_mask)
    peaks = sorted(zip(time_frames, freq_bins))  # ordenado por tempo
    
    # Geração de hashes via peak pairing
    hashes = []
    for i, (t1, f1) in enumerate(peaks):
        # target zone: pares (t2, f2) com t2 > t1 dentro da janela
        for j in range(i + 1, min(i + 1 + FAN_OUT * 5, len(peaks))):
            t2, f2 = peaks[j]
            dt = t2 - t1
            if dt < TARGET_ZONE_T_MIN:
                continue
            if dt > TARGET_ZONE_T_MAX:
                break  # peaks estão ordenados, posteriores também excederão
            if abs(f2 - f1) > TARGET_ZONE_F:
                continue
            
            # Hash compactado em uint32:
            # bits 0-8:   f1 (9 bits)
            # bits 9-17:  f2 (9 bits)
            # bits 18-31: dt (14 bits)
            hash_value = ((f1 & 0x1FF) << 23) | ((f2 & 0x1FF) << 14) | (dt & 0x3FFF)
            hashes.append((hash_value, int(t1)))
            
            if len([h for h in hashes if h[1] == int(t1)]) >= FAN_OUT:
                break
    
    return hashes
```

**Notas sobre o algoritmo:**

A janela de 4096 samples a 16 kHz dá 256 ms de duração temporal por frame, com hop de 128 ms (overlap de 50%). A resolução de frequência é 16000/4096 = 3.9 Hz por bin. Limitamos a banda útil de ~100 Hz (bin 25) a ~8 kHz (bin 2048), o que dá 2023 bins efetivos. Após peak picking típico, esperamos cerca de 20 a 40 picos por segundo no áudio processado.

O `FAN_OUT` controla quantos pares de hash são gerados por anchor. Cinco é um bom equilíbrio entre densidade de hash (mais robustez) e tamanho do índice. Se cobertura ficar baixa em testes, pode subir para sete ou oito.

A target zone com `dt` entre 1 e 16 frames (128 ms a 2 s) é o range onde alterações de timing são pequenas o suficiente para preservar pares. Limitar `|f2 - f1|` a 50 bins (~195 Hz) mantém pares localmente coerentes, evitando hashes triviais entre extremos espectrais.

O hash empacotado em 32 bits oferece 2^32 ≈ 4 bilhões de buckets, espaço suficiente para que colisões aleatórias sejam raras.

### 7.4 Persistência

Os hashes gerados são persistidos em Postgres (tabela `fingerprint_hashes`, ver seção quatorze) e replicados em Redis no momento do hot reload, em formato compactado (lista binária por hash).

Para um comercial de 30 segundos a 16 kHz processado com hop 128 ms, temos cerca de 234 frames. Com peak picking gerando ~30 picos/segundo e FAN_OUT de 5, temos ~150 hashes/segundo, ou ~4500 hashes por comercial. Para 500 comerciais ativos no índice, são ~2.25 milhões de hashes, o que cabe em ~50 MB de RAM em estrutura compactada.

Cada versão (light, medium, heavy) gera seu próprio conjunto de hashes, todos vinculados ao mesmo `commercial_id` mas com identificador de variante. No matching, qualquer variante que dê match é suficiente para confirmar.

### 7.5 Validação Pós-Geração

Após gerar fingerprints, o pipeline executa validação:

1. Carrega o áudio analysis_medium.wav e roda `match()` contra o próprio índice. Match deve confirmar com confidence > 0.95.
2. Carrega cinco trechos de áudio de "ruído" (programas conhecidos sem o comercial). Match deve falhar (sem confirmação).
3. Aplica perturbações controladas (adiciona ruído branco a -30 dB, time-stretch de 2%, reencode adicional). Match deve manter confidence > 0.6.

Se qualquer validação falhar, o fingerprint é marcado como `quality: low` e não entra no índice de produção, gerando alerta para revisão manual do master.

---

## 8. Pipeline de Ingestão e Análise de Stream

### 8.1 Estrutura do Stream Worker

Cada emissora monitorada tem um worker dedicado, implementado como uma goroutine (ou processo, em deployment distribuído) com a seguinte estrutura:

```go
type StreamWorker struct {
    StationID      string
    StreamURL      string
    StationConfig  StationConfig  // bitrate esperado, latência conhecida, threshold

    FFmpegCmd      *exec.Cmd
    EvidenceWriter io.WriteCloser  // pipe AAC para buffer de evidência
    AnalysisReader io.ReadCloser   // pipe PCM para análise

    EvidenceBuffer *RingBuffer     // 5 minutos de AAC
    AnalysisBuffer *RingBuffer     // 30 segundos de PCM float32

    MatchEngine    *MatchEngine
    Logger         *zap.Logger
    Metrics        *WorkerMetrics

    LastByteAt     time.Time
    ConnectedAt    time.Time
    BytesReceived  uint64

    ctx            context.Context
    cancel         context.CancelFunc
}
```

### 8.2 Comando ffmpeg

O ffmpeg é spawned como subprocess com a seguinte invocação base:

```bash
ffmpeg \
  -reconnect 1 \
  -reconnect_streamed 1 \
  -reconnect_delay_max 5 \
  -reconnect_at_eof 1 \
  -timeout 10000000 \
  -user_agent "${USER_AGENT}" \
  -i "${STREAM_URL}" \
  -map 0:a \
  -f tee \
  -map_metadata -1 \
  "[select=\\'a\\':f=adts:onfail=ignore]pipe:3|[select=\\'a\\':f=f32le:ar=16000:ac=1:onfail=ignore]pipe:4"
```

Explicando cada flag:

- `-reconnect 1` e variantes: ativa reconexão automática nativa do ffmpeg para HTTP streams.
- `-timeout 10000000`: 10 segundos de timeout para conexão e socket idle.
- `-user_agent`: rotacionado entre uma lista de User-Agents de clientes reais (VLC 3.0.x, Foobar2000 1.6.x, Chrome, Firefox).
- `-f tee` com dois outputs: gera duas saídas paralelas a partir da mesma decodificação. A saída pipe:3 vai em ADTS (AAC raw) com bitrate original, usada para evidência. A saída pipe:4 vai em PCM float32 16 kHz mono, usada para análise.
- `-map_metadata -1`: descarta tags ID3 e metadata do stream (alguns servidores enviam metadata em-stream que pode confundir o decoder).

O Go captura `pipe:3` em uma goroutine que escreve no `EvidenceBuffer` (ring buffer de 5 minutos), e captura `pipe:4` em outra goroutine que escreve no `AnalysisBuffer` (ring buffer de 30 segundos) e empurra janelas para o Match Engine.

### 8.3 Ring Buffers

**EvidenceBuffer:** 5 minutos × 128 kbps AAC = ~4.8 MB. Implementado como buffer circular fixo em RAM. Quando uma detecção é confirmada, o Evidence Service requisita o intervalo [t_match_start - 60s, t_match_end + 60s] e o buffer entrega os bytes correspondentes (que então são encodados em arquivo final e uploadeados).

**AnalysisBuffer:** 30 segundos × 16000 samples/s × 4 bytes/sample = ~1.92 MB. Implementado como ring de float32. O Match Engine lê janelas deslizantes de 4 segundos com overlap de 2 segundos (uma janela nova a cada 2 segundos).

Ambos os buffers são thread-safe via mutex ou via atomic pointer rotation.

### 8.4 Pré-processamento da Janela

Antes de gerar hashes da janela do stream, aplicamos exatamente o mesmo pré-processamento usado na referência:

1. Janela de 4 segundos = 64000 samples float32 (16 kHz mono).
2. High-pass filter a 100 Hz (filtro Butterworth de 4ª ordem, implementado via biquad).
3. Normalização de loudness rolling (ajuste para -23 LUFS aproximado, usando RMS deslizante porque LUFS exato seria caro). Alternativa simples e suficientemente boa: normalizar por RMS para target -20 dBFS.

```go
func PreprocessWindow(samples []float32) []float32 {
    out := ApplyHighPass(samples, 100.0, SampleRate)  // biquad HPF
    rms := ComputeRMS(out)
    targetRMS := DBToLinear(-20.0)
    if rms > 1e-6 {
        gain := targetRMS / rms
        for i := range out {
            out[i] *= gain
        }
    }
    return out
}
```

### 8.5 Health Monitoring do Worker

O worker monitora ativamente:

- **Bytes recebidos por intervalo:** se zero por 30 segundos, mata ffmpeg e reinicia.
- **Picos de áudio (peak detection):** se o RMS do áudio ficar abaixo de -60 dBFS por mais de 60 segundos consecutivos, considera-se "dead air" e gera evento de alerta. Pode ser problema do nosso lado (áudio mudo) ou da emissora (problema no ar).
- **Tempo desde última detecção:** se uma emissora com campanha ativa fica mais de N horas sem detectar nenhum dos comerciais esperados, alerta. (N depende da frequência esperada de cada campanha.)

Métricas são exportadas via Prometheus e dashboards mostram saúde de todos os workers em tempo real.

### 8.6 Estratégia de Reconexão

Quando o ffmpeg morre (por queda de stream, EOF inesperado, ou kill por timeout do worker), o worker entra em loop de reconexão:

```
delay = 1 segundo
while not connected:
    sleep(delay + jitter(0, delay/2))
    try connect
    if success:
        reset delay = 1
        log "reconnected"
        break
    else:
        delay = min(delay * 2, 60)  // máximo 1 minuto
        if total_time > 1 hora sem sucesso:
            alert operação
```

O jitter aleatório evita que múltiplos workers tentem reconectar simultaneamente após uma falha de rede compartilhada.

### 8.7 Restart Preventivo

A cada 24 horas de uptime, em janela escalonada (cada worker tem horário de restart aleatório entre 3:00 e 5:00 da manhã), o worker é gracefully terminado e reinicializado. Isso evita acúmulo de memória e mantém o sistema previsível. Durante o restart (1-3 segundos), o worker fica sem capturar; isso é aceitável dado o horário de baixíssimo tráfego.


---

## 9. Algoritmo de Matching

Esta é a parte mais crítica do sistema. A robustez aqui determina precision e recall finais.

### 9.1 Visão Geral

A cada 2 segundos (taxa de uma janela de análise), o Match Engine recebe um buffer de 4 segundos de PCM 16 kHz mono pré-processado. Para essa janela, o algoritmo executa:

1. Geração de hashes da janela (mesmo algoritmo da seção 7.3).
2. Para cada hash, consulta o índice e recebe lista de (commercial_id, variant_id, t_ref).
3. Para cada match retornado, computa `delta = t_ref - t_query` em frames.
4. Acumula deltas em um histograma agrupado por (commercial_id, variant_id).
5. Identifica bins com concentração anômala (acima do esperado por acaso).
6. Atualiza state machine para cada candidato.

O ponto crucial é que o matching **não decide na janela única**. Decisões finais emergem da combinação de múltiplas janelas analisadas ao longo do tempo, geridas pela state machine descrita em 9.5.

### 9.2 Estrutura do Índice em Memória

Em Go, o índice é um mapa concorrente:

```go
type IndexEntry struct {
    CommercialID  uint32  // mapeado de UUID via tabela auxiliar
    VariantID     uint8   // 0=light, 1=medium, 2=heavy
    TimeFrame     uint16  // offset em frames (até ~8 minutos)
}

type Index struct {
    table       map[uint32][]IndexEntry  // hash → entries
    commercials map[uint32]*CommercialMeta  // uuid->id curto, durations, etc
    mu          sync.RWMutex
    version     uint64  // monotonic, incrementado a cada hot reload
}

func (idx *Index) Lookup(hash uint32) []IndexEntry {
    idx.mu.RLock()
    defer idx.mu.RUnlock()
    return idx.table[hash]
}
```

Para hot reload, uma nova versão do índice é construída em paralelo e substituída via `atomic.StorePointer`. Leituras em andamento mantêm a versão antiga até completar; novas leituras pegam a nova versão. Sem lock global, sem stop the world.

### 9.3 Algoritmo de Match por Janela

Pseudocódigo em Go:

```go
type DeltaCounter struct {
    CommercialID uint32
    VariantID    uint8
    DeltaBin     int32
    Count        int
    HashesUsed   []uint32  // para cobertura temporal
}

func MatchWindow(window []float32, queryStartFrame uint64, idx *Index) []MatchCandidate {
    // 1. Gera hashes da janela (mesmo algoritmo da referência)
    queryHashes := GenerateHashes(window)
    
    // 2. Acumula deltas
    counters := make(map[CounterKey]*DeltaCounter)
    
    for _, qh := range queryHashes {
        entries := idx.Lookup(qh.Hash)
        for _, e := range entries {
            // delta em frames: posição na referência - posição na query (em janela)
            delta := int32(e.TimeFrame) - int32(qh.TimeFrame)
            
            key := CounterKey{
                CommercialID: e.CommercialID,
                VariantID:    e.VariantID,
                DeltaBin:     delta / DELTA_BIN_SIZE,  // bins de 2 frames (~256ms)
            }
            
            c := counters[key]
            if c == nil {
                c = &DeltaCounter{...}
                counters[key] = c
            }
            c.Count++
            c.HashesUsed = append(c.HashesUsed, qh.TimeFrame)
        }
    }
    
    // 3. Identifica candidatos
    var candidates []MatchCandidate
    for key, c := range counters {
        // Threshold mínimo de hashes alinhados
        if c.Count < MIN_HASHES_PER_WINDOW {
            continue
        }
        
        // Cobertura temporal: hashes distribuídos pela janela
        coverage := computeCoverage(c.HashesUsed, len(queryHashes))
        if coverage < MIN_COVERAGE {
            continue
        }
        
        candidates = append(candidates, MatchCandidate{
            CommercialID: key.CommercialID,
            VariantID:    key.VariantID,
            DeltaBin:     key.DeltaBin,
            HashCount:    c.Count,
            Coverage:     coverage,
            QueryStartFrame: queryStartFrame,
        })
    }
    
    return candidates
}
```

**Constantes recomendadas (a serem ajustadas em calibração):**

- `DELTA_BIN_SIZE = 2` frames (~256 ms de tolerância de alinhamento, absorve drift de relógio entre referência e stream).
- `MIN_HASHES_PER_WINDOW = 5` para janela de 4 segundos. Ajustável por emissora via threshold adaptativo.
- `MIN_COVERAGE = 0.4` (40% dos hashes da janela devem participar do match para considerar candidato).

### 9.4 Threshold Adaptativo por Emissora

Cada emissora tem cadeia de processamento ligeiramente diferente, então o que constitui "match forte" varia. Implementamos calibração:

**Modo de calibração (primeiros 7 dias de uma emissora nova):**

Durante a calibração, o sistema roda matching com threshold permissivo e registra, para cada janela analisada, qual o maior `HashCount` observado contra qualquer comercial do índice. Como a maioria das janelas em rádio comercial **não** contém comerciais cadastrados, esses valores formam a distribuição de **noise floor** específica da emissora.

Após 7 dias (~302400 janelas analisadas), calculamos:

- `noise_p99` = percentil 99 do HashCount em janelas de não-match
- `noise_max` = máximo observado em janelas de não-match

O threshold para a emissora é definido como `MIN_HASHES = max(noise_p99 * 1.5, 5)`. Isso garante que falsos positivos por acaso sejam estatisticamente improváveis (~1 em 10 mil janelas, ou seja, ~14 vezes por dia, ainda alto demais — daí a necessidade da state machine para confirmar com múltiplas janelas).

**Recalibração:** disparada automaticamente quando:
- Mudança de URL de stream da emissora.
- Mudança detectada no formato/bitrate.
- Aumento significativo de falsos positivos reportados pelo cliente.

### 9.5 State Machine de Detecção

Apenas matches em uma janela isolada não confirmam veiculação. A state machine combina evidências ao longo do tempo:

**Estados:**

- `IDLE`: nenhum match em curso para o par (emissora, comercial).
- `CANDIDATE`: matches recentes detectados, aguardando confirmação.
- `CONFIRMED`: detecção confirmada, evidência sendo gerada.
- `COOLDOWN`: detecção emitida, ignorando novos matches do mesmo par por X segundos.

**Transições:**

```
IDLE
  └── janela com candidato (commercial_id, delta_bin) ──▶ CANDIDATE(score=1)

CANDIDATE(score=N)
  ├── janela seguinte com mesmo (commercial_id, delta_bin)
  │     ──▶ CANDIDATE(score=N+1)
  ├── score >= MIN_CONFIRMATIONS e cobertura_total >= MIN_TEMPORAL_COVERAGE
  │     ──▶ CONFIRMED
  └── 3 janelas consecutivas sem match neste par
        ──▶ IDLE

CONFIRMED
  └── emite evento, dispara Evidence Service ──▶ COOLDOWN(duração = comercial_duration + 5s)

COOLDOWN
  └── tempo expirado ──▶ IDLE
```

**Constantes:**

- `MIN_CONFIRMATIONS = 3` janelas consecutivas.
- `MIN_TEMPORAL_COVERAGE = 0.6`. Calculado como: dado o intervalo de tempo entre o primeiro e último frame da query coberto por hashes do match, esse intervalo deve cobrir pelo menos 60% da duração esperada do comercial. Isso é o que defende contra falsos positivos por trilha musical compartilhada (R18). Uma trilha de 5 segundos dentro de um programa de 30 minutos não atinge cobertura temporal de 60% do comercial de 30 segundos.

### 9.6 Cobertura Temporal: Cálculo

Cobertura temporal é a fração da duração do comercial que foi efetivamente coberta por hashes alinhados. Calculada acumulando `HashesUsed` ao longo das janelas confirmando o candidato:

```go
func ComputeTemporalCoverage(hashesUsed []uint16, commercialDurationFrames uint16) float64 {
    // Bin os frames usados em janelas de 32 frames (~4 segundos)
    bins := make(map[uint16]bool)
    for _, frame := range hashesUsed {
        bins[frame/32] = true
    }
    
    expectedBins := commercialDurationFrames / 32
    if expectedBins == 0 {
        return 0
    }
    return float64(len(bins)) / float64(expectedBins)
}
```

Um comercial de 30 segundos tem ~234 frames totais, ou ~7 bins de 32 frames. Se 5 bins têm hashes, cobertura = 5/7 ≈ 0.71, atende threshold.

### 9.7 Matching Multi-Rate (Tolerância a Time Stretching)

Para absorver R8 (compressão temporal de 1-3% pelas emissoras), executamos matching em três velocidades em paralelo:

1. Velocidade nominal (1.0x).
2. Velocidade 0.97x (referência rodando 3% mais rápido que stream → simulação de stream acelerado).
3. Velocidade 1.03x (oposto).

Implementação: ao invés de reprocessar a referência, o sistema gera **três conjuntos de fingerprints da referência durante a fase de geração**, cada um com a referência ressampleada (via `sox tempo` ou `librosa.effects.time_stretch`). Cada conjunto é indexado separadamente. No matching, a query é consultada nos três índices e o resultado de maior confidence é usado.

O custo é 3x no índice (ainda gerenciável dado o tamanho compacto), mas matching em paralelo nos três é trivial via goroutines.

### 9.8 Desambiguação entre Versões (R20)

Quando dois comerciais distintos do mesmo cliente (tipicamente cortes 15s, 30s, 45s do mesmo conceito) dão match no mesmo intervalo temporal e mesma emissora:

```
SELECT * FROM candidates 
WHERE detected_at BETWEEN t-2s AND t+2s 
  AND station_id = ? 
  AND client_id = ?
ORDER BY commercial_duration DESC, hash_count DESC
LIMIT 1
```

Escolhemos a versão de **maior duração** que confirmou. A lógica é: se o corte de 30s confirmou (cobertura temporal alta sobre 30 segundos de áudio), foi esse que tocou. O corte de 15s sempre vai dar match parcial dentro do de 30s, mas sua cobertura temporal calculada sobre 30 segundos seria baixa, falhando o threshold.

Contraprova: se apenas o corte de 15s tocou, o de 30s não atinge cobertura temporal de 60% sobre 30s (porque os últimos 15s do master não estão no áudio), então só o de 15s confirma.

---

## 10. Camada de Verificação Neural

A camada neural é uma **defesa secundária** ativada apenas para casos limítrofes — candidatos com confidence média que não passam claramente nem reprovam claramente. Para a vasta maioria dos casos, o algoritmo de fingerprint é suficiente e a camada neural não é invocada.

### 10.1 Quando é Ativada

A state machine entra em estado `UNCERTAIN` ao invés de `IDLE` quando:

- O candidato acumulou matches por 3+ janelas consecutivas.
- Mas a cobertura temporal está entre 0.4 e 0.6 (abaixo do threshold de confirmação direta).
- Ou o HashCount está logo acima do noise floor da emissora mas abaixo do limiar sólido.

Neste caso, o sistema chama o **Neural Verifier** para arbitrar.

### 10.2 Modelo

Usamos **CLAP** (Contrastive Language-Audio Pretraining) da LAION, especificamente o checkpoint `630k-audioset-best.pt`, exportado para ONNX.

Alternativa mais leve: **PANNs CNN14** (Pretrained Audio Neural Networks) se latência ou tamanho de modelo for problema.

### 10.3 Pipeline de Verificação

```
1. Recebe (window_audio, candidate_commercial_id, candidate_variant_id)
2. Gera embedding da janela do stream (vetor de 512 ou 1024 dims)
3. Recupera embedding pré-computado da referência (gerado durante o pipeline da seção 7)
4. Calcula cosine similarity
5. Se similarity > 0.85 → confirma match
   Se similarity < 0.70 → rejeita match
   Caso contrário → mantém em UNCERTAIN, testa próxima janela
```

### 10.4 Geração de Embeddings da Referência

Durante o pipeline de fingerprint (seção 7), além de gerar hashes, geramos também o embedding CLAP da referência completa, **e** embeddings de cada janela deslizante de 4 segundos do comercial (com hop de 2 segundos), armazenados em uma tabela `commercial_embeddings`.

Para verificação em runtime, comparamos o embedding da janela do stream com o embedding da janela da referência **alinhado ao delta** sugerido pelo fingerprint match.

### 10.5 Performance

- Embedding de janela de 4s em CLAP via ONNX Runtime CPU: ~50-100 ms em CPU moderna.
- Cosine similarity: trivial.

Como ativação é rara (~1-5% dos candidatos), o custo agregado é negligenciável. Em estimativa: 200 emissoras × 50 detecções/dia × 5% de ativação × 100 ms = ~50 segundos de CPU/dia. Cabe em uma única instância.

### 10.6 Fallback

Se o Neural Verifier estiver indisponível (modelo travado, OOM), o sistema usa apenas o fingerprint clássico. UNCERTAIN candidates expirram normalmente após 3 janelas sem confirmação adicional.


---

## 11. Geração e Armazenamento de Evidência

### 11.1 Fluxo

Quando o Match Engine emite evento `detection.confirmed`, o Evidence Service:

1. Aguarda 60 segundos a partir do `match_end_ms` para garantir que o ring buffer já contém o pós-veiculação completo.
2. Solicita ao worker correspondente o intervalo `[match_start_ms - 60000, match_end_ms + 60000]` do EvidenceBuffer.
3. Recebe os bytes AAC raw correspondentes ao intervalo.
4. Encoda em arquivo final M4A (AAC em container MP4) via ffmpeg.
5. Calcula SHA-256 do arquivo final para integridade.
6. Faz upload para Cloudflare R2 no bucket `evidences`.
7. Atualiza registro `detections` no Postgres com path da evidência e status `available`.
8. Dispara webhook para clientes inscritos.

### 11.2 Comando de Encoding Final

```bash
ffmpeg -f adts -i input.aac \
  -c:a copy \
  -movflags +faststart \
  -metadata title="Evidência ${detection_id}" \
  -metadata artist="Sistema de Monitoramento" \
  -metadata date="${detection_iso8601}" \
  output.m4a
```

`-c:a copy` evita re-encoding (preserva qualidade original do streaming). `+faststart` move o moov atom para o início do arquivo, permitindo streaming progressive download.

### 11.3 Layout no Storage

```
r2://evidences/
  YYYY/                    # ano
    MM/                    # mês
      DD/                  # dia
        {station_id}/      # uuid da emissora
          {detection_id}.m4a
          {detection_id}.json   # metadata completa da detecção
```

Por exemplo:
```
r2://evidences/2026/05/04/3f5a-b1c2.../det-9d8e-7c6b.m4a
```

### 11.4 Política de Retenção e Tiering

- **0 a 30 dias:** SSD local do servidor de evidências (cache quente para acesso instantâneo).
- **30 a 365 dias:** Cloudflare R2 standard (sem egress fee, custo ~15 USD/TB/mês).
- **Após 365 dias:** sujeito a contrato com cliente. Default: mantém em R2 indefinidamente. Para clientes que aceitam, move para R2 Infrequent Access (mais barato) ou exporta dump compactado para arquivo offline.

Cada detecção mantém até 3 cópias em paralelo durante os primeiros 30 dias: SSD local, R2 (replicação automática), e backup em segundo bucket cross-region. A partir de 30 dias, mantém apenas a cópia em R2 com versionamento ativado.

### 11.5 Tratamento de Falhas

**Falha ao recuperar do buffer:** se o EvidenceBuffer não tiver os bytes necessários (worker reiniciou ou stream caiu durante a janela), a detecção é registrada com `evidence_status: missing`. Operação é alertada para revisão manual.

**Falha de upload:** clip é mantido em fila local persistente (filesystem em /var/spool/evidences) com retry exponencial até 24 horas. Após 24h sem sucesso, alerta crítico.

**Falha de encoding:** raríssimo. Se ffmpeg falhar, retry uma vez. Em segunda falha, salva o stream raw (AAC bruto sem container) e marca para review.

---

## 12. Esquema de Dados (PostgreSQL)

### 12.1 DDL Completo

```sql
-- Extensões necessárias
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "btree_gist";

-- ===== CLIENTES =====
CREATE TABLE clients (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    contact_email TEXT,
    api_key_hash TEXT,        -- SHA-256 da API key
    webhook_url TEXT,
    webhook_secret TEXT,      -- HMAC secret para assinar webhooks
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===== EMISSORAS =====
CREATE TABLE stations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,           -- inteiro pequeno para uso em índice em memória
    name TEXT NOT NULL,
    band TEXT NOT NULL CHECK (band IN ('AM', 'FM')),
    frequency_mhz NUMERIC(6,2),
    city TEXT,
    state CHAR(2),
    region TEXT,
    stream_url TEXT NOT NULL,
    stream_url_alternates TEXT[] DEFAULT '{}',
    stream_format TEXT,                -- mp3, aac, hls
    stream_bitrate_kbps INT,
    stream_sample_rate_hz INT,
    stream_channels INT,
    expected_latency_seconds NUMERIC(5,2) DEFAULT 10.0,
    monitoring_status TEXT DEFAULT 'paused' CHECK (monitoring_status IN ('active','paused','calibrating','error')),
    last_health_check TIMESTAMPTZ,
    health_status TEXT,
    consecutive_failures INT DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stations_status ON stations(monitoring_status);
CREATE INDEX idx_stations_short_id ON stations(short_id);

-- ===== CONFIGURAÇÃO ADAPTATIVA POR EMISSORA =====
CREATE TABLE station_calibration (
    station_id UUID PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
    min_hashes_per_window INT DEFAULT 8,
    min_temporal_coverage NUMERIC(4,3) DEFAULT 0.600,
    delta_bin_size_frames INT DEFAULT 2,
    noise_floor_p99 INT,
    noise_floor_max INT,
    samples_collected INT DEFAULT 0,
    last_calibrated_at TIMESTAMPTZ,
    notes TEXT
);

-- ===== CAMPANHAS =====
CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id UUID NOT NULL REFERENCES clients(id),
    name TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    status TEXT DEFAULT 'planned' CHECK (status IN ('planned','active','paused','ended')),
    target_stations UUID[] NOT NULL,    -- emissoras alvo da campanha
    expected_airings_per_day INT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT campaign_dates_valid CHECK (end_date >= start_date)
);

CREATE INDEX idx_campaigns_status_dates ON campaigns(status, start_date, end_date);
CREATE INDEX idx_campaigns_client ON campaigns(client_id);

-- ===== COMERCIAIS =====
CREATE TABLE commercials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    campaign_id UUID NOT NULL REFERENCES campaigns(id),
    title TEXT NOT NULL,
    cut_label TEXT,                     -- "30s versão A"
    duration_seconds NUMERIC(6,3) NOT NULL,
    master_storage_path TEXT NOT NULL,
    master_sha256 TEXT NOT NULL,
    fingerprint_status TEXT DEFAULT 'pending' CHECK (fingerprint_status IN ('pending','generating','ready','failed')),
    fingerprint_generated_at TIMESTAMPTZ,
    fingerprint_quality TEXT,           -- 'high', 'medium', 'low' baseado em validação
    fingerprint_hash_count INT,         -- total de hashes em todas as variantes
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_commercials_campaign ON commercials(campaign_id);
CREATE INDEX idx_commercials_status ON commercials(fingerprint_status);
CREATE INDEX idx_commercials_short_id ON commercials(short_id);

-- ===== HASHES DE FINGERPRINT =====
-- Particionado por commercial_id (hash) para escala
CREATE TABLE fingerprint_hashes (
    commercial_id UUID NOT NULL,
    variant_id SMALLINT NOT NULL,       -- 0=light, 1=medium, 2=heavy
    rate_id SMALLINT NOT NULL DEFAULT 0,-- 0=1.0x, 1=0.97x, 2=1.03x
    hash_value BIGINT NOT NULL,         -- uint32 stored as bigint (Postgres não tem unsigned)
    time_frame INT NOT NULL,
    PRIMARY KEY (commercial_id, variant_id, rate_id, hash_value, time_frame)
) PARTITION BY HASH (commercial_id);

-- Criar 16 partições
DO $$
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format('CREATE TABLE fingerprint_hashes_p%s PARTITION OF fingerprint_hashes FOR VALUES WITH (MODULUS 16, REMAINDER %s)', i, i);
        EXECUTE format('CREATE INDEX idx_fph_p%s_hash ON fingerprint_hashes_p%s(hash_value)', i, i);
    END LOOP;
END $$;

-- ===== EMBEDDINGS NEURAIS (para verificação) =====
CREATE TABLE commercial_embeddings (
    commercial_id UUID NOT NULL REFERENCES commercials(id) ON DELETE CASCADE,
    variant_id SMALLINT NOT NULL,
    window_start_ms INT NOT NULL,
    embedding BYTEA NOT NULL,           -- vetor float32 serializado, 512 ou 1024 dims
    PRIMARY KEY (commercial_id, variant_id, window_start_ms)
);

-- ===== DETECÇÕES =====
-- Particionado por mês para retenção e performance
CREATE TABLE detections (
    id UUID NOT NULL DEFAULT uuid_generate_v4(),
    station_id UUID NOT NULL REFERENCES stations(id),
    commercial_id UUID NOT NULL REFERENCES commercials(id),
    campaign_id UUID NOT NULL REFERENCES campaigns(id),
    detected_at TIMESTAMPTZ NOT NULL,
    match_start_offset_ms INT NOT NULL, -- relativo a detected_at
    match_end_offset_ms INT NOT NULL,
    confidence NUMERIC(5,4) NOT NULL,
    hash_count INT NOT NULL,
    delta_bin_strength NUMERIC(5,4),
    temporal_coverage NUMERIC(4,3),
    variant_used SMALLINT,
    rate_used SMALLINT,
    neural_verified BOOLEAN DEFAULT FALSE,
    neural_similarity NUMERIC(5,4),
    evidence_status TEXT DEFAULT 'pending' CHECK (evidence_status IN ('pending','generating','available','missing','failed')),
    evidence_storage_path TEXT,
    evidence_sha256 TEXT,
    evidence_size_bytes BIGINT,
    notes JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, detected_at)
) PARTITION BY RANGE (detected_at);

-- Partições mensais (criar para os próximos 12 meses iniciais)
DO $$
DECLARE
    start_date DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_date DATE;
BEGIN
    FOR i IN 0..11 LOOP
        end_date := start_date + INTERVAL '1 month';
        EXECUTE format('CREATE TABLE detections_%s PARTITION OF detections FOR VALUES FROM (%L) TO (%L)',
                       TO_CHAR(start_date, 'YYYY_MM'), start_date, end_date);
        start_date := end_date;
    END LOOP;
END $$;

CREATE INDEX idx_detections_station_time ON detections(station_id, detected_at DESC);
CREATE INDEX idx_detections_campaign_time ON detections(campaign_id, detected_at DESC);
CREATE INDEX idx_detections_commercial_time ON detections(commercial_id, detected_at DESC);
CREATE INDEX idx_detections_evidence_status ON detections(evidence_status) WHERE evidence_status != 'available';

-- ===== EVENTOS DE SAÚDE DE STREAM =====
CREATE TABLE stream_health_events (
    id BIGSERIAL,
    station_id UUID NOT NULL,
    event_type TEXT NOT NULL,           -- connected, disconnected, reconnected, dead_air, slow, format_change
    event_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    duration_seconds INT,
    details JSONB DEFAULT '{}',
    PRIMARY KEY (id, event_at)
) PARTITION BY RANGE (event_at);

-- Partições mensais
DO $$
DECLARE
    start_date DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_date DATE;
BEGIN
    FOR i IN 0..11 LOOP
        end_date := start_date + INTERVAL '1 month';
        EXECUTE format('CREATE TABLE stream_health_%s PARTITION OF stream_health_events FOR VALUES FROM (%L) TO (%L)',
                       TO_CHAR(start_date, 'YYYY_MM'), start_date, end_date);
        start_date := end_date;
    END LOOP;
END $$;

CREATE INDEX idx_health_station_time ON stream_health_events(station_id, event_at DESC);

-- ===== AUDIT LOG =====
CREATE TABLE audit_log (
    id BIGSERIAL PRIMARY KEY,
    actor TEXT NOT NULL,                -- user_id, system, api_key_id
    action TEXT NOT NULL,
    resource_type TEXT,
    resource_id TEXT,
    details JSONB DEFAULT '{}',
    ip_address INET,
    user_agent TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_occurred ON audit_log(occurred_at DESC);
CREATE INDEX idx_audit_actor_time ON audit_log(actor, occurred_at DESC);

-- ===== USUÁRIOS DO SISTEMA =====
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    name TEXT,
    role TEXT NOT NULL CHECK (role IN ('admin','operator','viewer','client')),
    client_id UUID REFERENCES clients(id), -- null para usuários internos
    is_active BOOLEAN DEFAULT TRUE,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===== TRIGGERS =====
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_clients_updated BEFORE UPDATE ON clients
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_stations_updated BEFORE UPDATE ON stations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_campaigns_updated BEFORE UPDATE ON campaigns
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_commercials_updated BEFORE UPDATE ON commercials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
```

### 12.2 Estimativas de Volume

- Stations: até 1700 registros, ~1 MB.
- Commercials: histórico ilimitado, mas ativos simultâneos ~500. Com histórico de 5 anos, ~10 mil. ~5 MB.
- Fingerprint hashes: ~5 mil hashes/comercial × 3 variantes × 3 rates × 500 ativos = ~22 milhões de linhas. ~2 GB com índice.
- Detections: 200 emissoras × 50/dia × 365 dias = ~3.6 milhões/ano. ~1 GB/ano.
- Health events: ~100 eventos/dia/emissora × 200 emissoras × 365 = ~7 milhões/ano. ~500 MB/ano.

Total: Postgres comporta facilmente em servidor único de 200 GB SSD por anos.


---

## 13. Contratos de API

### 13.1 API REST Externa (Clientes)

Base URL: `https://api.monitoramento.empresa.com.br/v1`

Autenticação: header `Authorization: Bearer <api_key>` ou `X-Api-Key: <api_key>`. API keys são SHA-256 hashed antes de armazenar.

Rate limiting: 60 requisições/minuto por API key, 1000/hora.

#### 13.1.1 Endpoints de Detecção

**GET /detections**

Query params:
- `start_date` (ISO 8601, obrigatório)
- `end_date` (ISO 8601, obrigatório, máximo 90 dias após start_date)
- `campaign_id` (UUID, opcional)
- `commercial_id` (UUID, opcional)
- `station_id` (UUID, opcional)
- `limit` (1-1000, default 100)
- `offset` (default 0)

Response:
```json
{
  "data": [
    {
      "id": "9d8e-7c6b-...",
      "station": {
        "id": "3f5a-b1c2-...",
        "name": "Rádio Exemplo FM",
        "frequency_mhz": 100.5,
        "city": "São Paulo",
        "state": "SP"
      },
      "commercial": {
        "id": "...",
        "title": "Campanha XYZ - 30s versão A",
        "duration_seconds": 30.0
      },
      "campaign": {
        "id": "...",
        "name": "Campanha XYZ Q2"
      },
      "detected_at": "2026-05-04T14:32:00.000Z",
      "match_start_at": "2026-05-04T14:32:00.000Z",
      "match_end_at": "2026-05-04T14:32:30.000Z",
      "confidence": 0.94,
      "evidence_url": "https://api.monitoramento.empresa.com.br/v1/detections/9d8e.../evidence",
      "evidence_available": true
    }
  ],
  "pagination": {
    "total": 1547,
    "limit": 100,
    "offset": 0
  }
}
```

**GET /detections/{id}**

Retorna detecção única com detalhes completos, incluindo confidence breakdown e flags técnicas.

**GET /detections/{id}/evidence**

Retorna o arquivo M4A da evidência (Content-Type: audio/mp4). Stream direto do R2 com signed URL.

#### 13.1.2 Endpoints de Campanha

**GET /campaigns**: lista campanhas do cliente.
**POST /campaigns**: cria campanha.
**GET /campaigns/{id}**: detalhes de campanha.
**GET /campaigns/{id}/summary**: agregados (total de detecções por dia, por emissora).
**PATCH /campaigns/{id}**: atualiza status, datas.

#### 13.1.3 Endpoints de Comercial

**POST /commercials**: cadastra comercial. Aceita multipart com arquivo de áudio.
```
POST /commercials
Content-Type: multipart/form-data

campaign_id: <uuid>
title: "Campanha XYZ - 30s"
cut_label: "30s versão A"
audio: <binary>
```

Response 202:
```json
{
  "id": "...",
  "fingerprint_status": "pending",
  "estimated_ready_at": "2026-05-04T15:35:00.000Z"
}
```

**GET /commercials/{id}**: detalhes incluindo status do fingerprint.

#### 13.1.4 Webhooks

Cliente cadastra webhook URL via `PATCH /clients/me { webhook_url, webhook_secret }`.

Quando detecção é confirmada e evidência fica disponível, sistema dispara:

```
POST {webhook_url}
Content-Type: application/json
X-Signature: HMAC-SHA256 do body com webhook_secret
X-Event-Type: detection.confirmed
X-Delivery-Id: <uuid>

{
  "event_type": "detection.confirmed",
  "detection": { /* mesmo payload do GET /detections/{id} */ }
}
```

Cliente deve responder 2xx em até 5 segundos. Não respondeu ou respondeu 5xx: retry com backoff exponencial (1m, 5m, 15m, 1h, 4h). Após 5 tentativas, dead letter queue.

### 13.2 API Interna (Operadores)

Endpoints adicionais sob `/internal` (auth via JWT de usuário interno):

- `POST /internal/stations` — cadastra emissora.
- `POST /internal/stations/{id}/calibrate` — força recalibração.
- `POST /internal/stations/{id}/test-stream` — testa conectividade.
- `GET /internal/health` — saúde geral do sistema.
- `GET /internal/workers` — status de cada worker.
- `POST /internal/index/reload` — força reload do índice.

### 13.3 Eventos Internos via NATS

| Subject | Publisher | Consumer | Payload |
|---------|-----------|----------|---------|
| `detections.confirmed` | Match Engine | Evidence Service, Notification Service | DetectionEvent |
| `detections.uncertain` | Match Engine | Neural Verifier | UncertainEvent |
| `evidence.ready` | Evidence Service | Notification Service | EvidenceReadyEvent |
| `index.reload` | Fingerprint Service | Index Service | IndexReloadEvent |
| `stream.health` | Stream Ingestor | Health Aggregator | StreamHealthEvent |
| `commercial.created` | Catalog Service | Fingerprint Service | CommercialCreatedEvent |

Esquema de DetectionEvent:
```json
{
  "detection_id": "uuid",
  "station_id": "uuid",
  "commercial_id": "uuid",
  "campaign_id": "uuid",
  "detected_at": "ISO8601",
  "match_start_offset_ms": 0,
  "match_end_offset_ms": 30000,
  "confidence": 0.94,
  "hash_count": 42,
  "temporal_coverage": 0.83,
  "variant_used": 1,
  "rate_used": 0,
  "neural_verified": false
}
```

---

## 14. Infraestrutura

### 14.1 Topologia Recomendada

**Servidor de aplicação A (primário) — Hetzner AX52:**
- 16 cores AMD Ryzen 7950X (ou equivalente)
- 64 GB RAM
- 2× 2 TB NVMe em RAID 1
- IP dedicado IPv4 + bloco IPv6
- Roda: 100 stream workers + Match Engine + Index Service + Evidence Service local

**Servidor de aplicação B (secundário) — Hetzner AX52:**
- Mesma config
- IP de saída diferente (em datacenter diferente, idealmente)
- Roda: outros 100 stream workers + Match Engine + Index Service replicado

**Servidor de banco — Hetzner EX44 ou managed Postgres:**
- 6 cores Intel Xeon
- 64 GB RAM
- 2× 1 TB NVMe RAID 1
- Postgres 16 + réplica streaming para read-only queries

**Servidor de coordenação — Hetzner CX31 (cloud):**
- 2 vCPU, 8 GB RAM
- Roda: NATS (master), Catalog Service, API Gateway, Fingerprint Service (cron jobs), Prometheus, Grafana, Loki, Alertmanager

**Storage de evidência:**
- Cloudflare R2 (bucket `evidences-prod`)
- Bucket secundário em região alternativa para backup
- Cache local em SSD nos servidores A e B (30 dias quentes)

**Total mensal estimado de infra core:** ~280 USD (2× AX52 ~140 USD, 1× EX44 ~70 USD, 1× CX31 ~10 USD, R2 ~50 USD por crescimento de evidências). Banda inclusa nos servidores Hetzner.

### 14.2 Distribuição de Workers

Atribuição de emissoras a servidores via consistent hashing baseado em station_id, garantindo que cada emissora "vê" sempre o mesmo IP de origem (mitigação R33). Operação manual permite override caso uma emissora bloqueie o IP padrão.

### 14.3 Networking

- Servidores A e B em VLAN privada (Hetzner vSwitch ou WireGuard mesh).
- Conexão com Postgres via IP privado.
- NATS exposto apenas na VLAN privada.
- API pública atrás de Cloudflare (mitigação de DDoS, cache de assets estáticos).
- Saída para streams das emissoras via NAT do datacenter (IPs públicos dos servidores).

### 14.4 Backup e Disaster Recovery

**Postgres:**
- Backup full diário via pg_basebackup, retido por 30 dias.
- WAL archiving contínuo (point-in-time recovery).
- Backups armazenados em R2 cross-region.
- Teste de restore mensal (full restore para servidor descartável, validação de queries).

**Configuração de aplicação:**
- Tudo em git, deploy via CI/CD.
- Secrets em Vault ou Hetzner Secrets Manager.

**Evidências:**
- R2 com versionamento ativado.
- Bucket de backup secundário em segunda região.

**RTO (Recovery Time Objective):** 1 hora para restaurar serviço completo.
**RPO (Recovery Point Objective):** 5 minutos (perda máxima de dados em desastre).

### 14.5 Provisionamento e Configuração

- IaC via Terraform para recursos cloud (Hetzner Cloud, Cloudflare, R2).
- Configuração de servidores via Ansible.
- Containers via Docker Compose nos servidores A/B (fase 1-2). Migração para K3s na fase 3 se complexidade demandar.
- Repositório monorepo com:
  ```
  /
  ├── workers/          (Go - stream ingestor + match engine)
  ├── fingerprint/      (Python - geração)
  ├── api/              (Go - API gateway)
  ├── neural/           (Python - CLAP verifier)
  ├── infra/
  │   ├── terraform/
  │   ├── ansible/
  │   └── docker/
  ├── migrations/       (sqlc/goose para Postgres)
  └── docs/
  ```

---

## 15. Observabilidade

### 15.1 Métricas (Prometheus)

**Por worker (label: station_id):**
- `worker_bytes_received_total` (counter)
- `worker_audio_seconds_processed_total` (counter)
- `worker_reconnects_total` (counter)
- `worker_dead_air_events_total` (counter)
- `worker_uptime_seconds` (gauge)
- `worker_buffer_evidence_seconds` (gauge)
- `worker_buffer_analysis_seconds` (gauge)
- `worker_audio_rms_dbfs` (gauge)
- `worker_format_changes_total` (counter)

**Por Match Engine (label: instance_id):**
- `match_windows_processed_total` (counter)
- `match_window_duration_seconds` (histogram)
- `match_candidates_emitted_total` (counter, labeled by status: confirmed/rejected/uncertain)
- `match_index_lookups_total` (counter)
- `match_index_hit_ratio` (gauge)
- `match_index_size_hashes` (gauge)
- `match_neural_verifications_total` (counter)

**Por Evidence Service:**
- `evidence_clips_generated_total` (counter)
- `evidence_clip_duration_seconds` (histogram)
- `evidence_upload_duration_seconds` (histogram)
- `evidence_upload_failures_total` (counter)
- `evidence_queue_size` (gauge)

**Sistema:**
- `system_index_version` (gauge)
- `system_active_commercials` (gauge)
- `system_active_stations` (gauge)
- `db_query_duration_seconds` (histogram)

### 15.2 Logging

- Estruturado em JSON via zap (Go) ou structlog (Python).
- Coletado por Promtail e enviado a Loki.
- Níveis: debug (dev), info (prod default), warn, error, fatal.
- Cada log carrega correlation_id quando aplicável (ex: detection_id em todo log relacionado a uma detecção).

### 15.3 Tracing

OpenTelemetry com exporter para Jaeger (ou Tempo no Grafana). Spans em:
- Recepção de janela no Match Engine
- Lookup no índice
- Geração de candidate
- Confirmação e emissão de evento
- Geração de evidência
- Upload para R2
- Disparo de webhook

### 15.4 Dashboards (Grafana)

**Dashboard "Operações":**
- Mapa de saúde de todas as 200 emissoras (verde/amarelo/vermelho)
- Detecções por hora nas últimas 24h
- Latência média de confirmação
- Fila de evidências pendentes

**Dashboard "Detecções":**
- Taxa de detecção por campanha
- Confidence distribution
- Versões usadas (light/medium/heavy)
- Neural verifications

**Dashboard "Infra":**
- CPU, memória, disco, rede por servidor
- Latência de Postgres
- Throughput NATS
- Uso de R2

### 15.5 Alertas (Alertmanager)

| Alerta | Severidade | Trigger | Ação |
|--------|-----------|---------|------|
| StreamDownProlongado | critical | worker sem áudio > 10 min | PagerDuty + Slack |
| MultipleStreamsDown | critical | > 5% emissoras down | PagerDuty + Slack |
| DBLatencyHigh | warning | p99 query > 200ms | Slack |
| EvidenceQueueGrowing | warning | fila > 50 itens | Slack |
| EvidenceUploadFailures | critical | > 10 falhas/hora | PagerDuty + Slack |
| DetectionRateAnomaly | warning | detecções caem > 50% vs 7d médio | Slack |
| WorkerHighMemory | warning | RSS > 2 GB | Slack |
| ClockDrift | warning | drift > 50ms | Slack |
| IndexReloadFailed | critical | hot reload falhou | PagerDuty |
| NeuralVerifierDown | warning | indisponível > 5 min | Slack |
| DiskSpaceLow | warning | < 20% livre | Slack |
| DiskSpaceCritical | critical | < 5% livre | PagerDuty |

### 15.6 Runbooks

Cada alerta crítico tem runbook documentado em `/docs/runbooks/{alert_name}.md`, contendo:
- Sintomas
- Causas comuns
- Comandos de diagnóstico
- Procedimento de correção
- Como escalar

Exemplo: `StreamDownProlongado.md`:
1. Verificar via dashboard se é uma emissora ou várias.
2. Se uma: testar URL manualmente com `curl -I` e `ffprobe`.
3. Se a URL responde mas não entrega áudio, verificar logs do worker.
4. Se URL não responde, contatar a emissora ou marcar como `paused` e alertar comercial.
5. Se múltiplas: verificar IP de saída do servidor (`curl ifconfig.me`), checar se foi blocked.
6. Se IP blocked, fazer failover para servidor B com IP alternativo.


---

## 16. Segurança

### 16.1 Autenticação e Autorização

**Usuários internos:** login via email/senha (bcrypt cost 12) com 2FA TOTP obrigatório para roles admin e operator. Sessões via JWT com expiração de 8 horas e refresh token rotacionado de 7 dias.

**Clientes externos:** API keys de 32 bytes random gerados no servidor, hashados com SHA-256 antes de armazenar. O cliente vê a key apenas uma vez no momento da criação. Cada key tem escopo (quais campanhas/comerciais ela enxerga) e rate limit individual.

**Webhooks:** payload assinado com HMAC-SHA256 usando segredo compartilhado, header `X-Signature: sha256=<hex>`. Cliente deve verificar antes de processar.

**Comunicação interna:** mTLS entre serviços em deployment distribuído (Fase 3). Em monolito (Fase 1-2), comunicação local via socket Unix ou loopback é aceitável sem TLS.

### 16.2 Roles e Permissões

| Role | Ler detecções próprias | Ler todas detecções | Cadastrar comercial | Gerenciar emissoras | Gerenciar usuários |
|------|------------------------|---------------------|---------------------|---------------------|--------------------|
| client | sim | não | sim (própria conta) | não | não |
| viewer | não | sim | não | não | não |
| operator | não | sim | sim | sim | não |
| admin | não | sim | sim | sim | sim |

### 16.3 Proteção de Dados

- Postgres com encryption at rest via LUKS no servidor host.
- Backups encriptados com chave armazenada em Vault (separado do servidor de banco).
- TLS 1.3 obrigatório para API externa, TLS 1.2 mínimo para webhooks de saída.
- Cloudflare R2 com encryption at rest nativa, signed URLs com TTL de 1 hora para acesso a evidências.
- Senhas dos usuários nunca trafegam em log; PII de clientes (email, nome) tem mascaramento em logs estruturados.

### 16.4 Audit Log

Toda ação sensível gera entrada imutável em `audit_log` com actor, action, resource e timestamp:

- Criação, edição, exclusão de campanha, comercial, emissora, usuário.
- Mudança de threshold de calibração.
- Geração ou revogação de API key.
- Acesso a evidência (quem baixou, quando).
- Mudança de status de monitoramento de uma emissora.

A tabela `audit_log` não tem privilégio de UPDATE ou DELETE para o usuário da aplicação. Apenas DBA tem privilégio de DELETE para purga após período legal mínimo.

### 16.5 Compliance e Aspectos Legais

**LGPD:** dados pessoais limitados a contatos comerciais dos clientes. Política de privacidade publicada. DPO definido. Procedimento de atendimento a solicitações do titular documentado.

**Direitos autorais sobre evidências:** as gravações são geradas para comprovação contratual de veiculação publicitária paga pelo próprio cliente. Uso é amparado em (i) contrato com o cliente final, (ii) finalidade específica e limitada de comprovação, e (iii) política de retenção respeitando prazo contratual. Recomenda-se validação jurídica antes do go-live, especialmente em relação à postura das emissoras quanto a captação de seu sinal de streaming público para fins comerciais.

### 16.6 Resposta a Incidentes

Plano documentado em `/docs/security/incident_response.md`, com fases:

1. Detecção (alerta automatizado ou reporte manual).
2. Triagem (severidade, escopo, classificação como incidente de segurança ou operacional).
3. Contenção (isolar sistema afetado, rotacionar credenciais comprometidas).
4. Erradicação (remover causa raiz).
5. Recuperação (restaurar serviço com validação).
6. Post-mortem público interno e, quando aplicável, comunicação a clientes afetados.

Contatos de emergência mantidos em sistema fora-de-banda (não no mesmo Postgres operacional).

---

## 17. Plano de Migração e Coexistência com Fornecedor Atual

### 17.1 Fases de Coexistência

**Fase A — Sistema novo em shadow mode (8 semanas estimadas).**

Sistema novo opera em paralelo, monitorando o mesmo conjunto de emissoras e comerciais que o fornecedor atual, mas suas detecções vão apenas para um banco interno de comparação. Fornecedor atual continua atendendo o cliente final sem mudanças.

Durante esta fase, comparamos detecção a detecção:

- Para cada detecção do fornecedor, verificar se o sistema novo também detectou dentro de janela de tolerância de ±5 segundos.
- Para cada detecção do sistema novo sem correspondência no fornecedor, verificar manualmente (audição da gravação) se foi falso positivo nosso ou falso negativo do fornecedor.
- Calcular precision e recall do sistema novo usando o fornecedor como ground truth aproximado.
- Documentar todas as discordâncias, classificando-as.

Critério de avanço para Fase B: concordância igual ou superior a 95% por pelo menos 3 semanas consecutivas, com discordâncias investigadas e documentadas.

**Fase B — Switching gradual por cliente (8 semanas estimadas).**

Selecionar 1 a 2 clientes pequenos e dispostos a participar de piloto formal para passar a receber detecções do sistema novo, mantendo o fornecedor como backup invisível por 4 semanas adicionais. Operação manual: a cada manhã, comparar relatório do sistema novo com o do fornecedor para esses clientes. Se houver discordância material reportada pelo cliente, investigar e corrigir.

Avançar para próximos clientes em ondas de 5 a 10 clientes por semana, após 2 semanas sem incidente.

**Fase C — Switching massivo (4 semanas estimadas).**

Migração de aproximadamente 80% dos clientes para o sistema novo. Fornecedor mantido apenas para 20% mais críticos (clientes maiores, contratos sensíveis ou mais exigentes) por mais 4 semanas de observação.

**Fase D — Corte total do fornecedor.**

Após período total estimado de 5 a 6 meses desde início da Fase A, cortar contrato com fornecedor.

### 17.2 Critérios de Reversão

A qualquer momento durante a migração, se observarmos:

- Concordância caindo abaixo de 90% em janela móvel de 7 dias.
- Reclamação formal de cliente com perda comprovada de detecção crítica.
- Falha grave de infraestrutura no sistema novo com impacto superior a 4 horas.

…paramos a migração, retornamos clientes afetados ao fornecedor, investigamos e só retomamos após resolução. Plano de reversão é executável em até 1 hora (mudança de configuração no roteamento de cada cliente, sem reprocessamento de dados).

### 17.3 Acordo Comercial com Fornecedor durante Coexistência

- Negociar redução de volume gradual no contrato (paga-se cada vez menos à medida que clientes migram).
- Manter visibilidade aos dados do fornecedor (relatórios diários completos) até cortar definitivamente.
- Não comunicar publicamente a saída até que esteja consolidada, evitando antagonização desnecessária e proteção contra retaliação técnica.

---

## 18. Plano de Execução em Fases

### 18.1 Fase 1 — Prova Técnica (Semanas 1 a 6)

**Escopo:** 5 emissoras representativas, 10 comerciais.

**Composição das 5 emissoras (escolha intencional para cobrir os tipos de processamento):**

- 1 AM regional, idealmente em região com infraestrutura de internet menos estável.
- 1 FM popular de grande capital (São Paulo, Rio).
- 1 FM jovem com processamento agressivo (típico de emissoras de hits).
- 1 FM cultural ou clássica com processamento leve.
- 1 FM de interior com link instável.

**Entregas:**

- Pipeline completo de geração de fingerprint funcionando em batch.
- 5 stream workers em produção contínua com restart preventivo.
- Match Engine com state machine completa.
- Dashboard básico de detecções e saúde de stream.
- Geração de evidência (sem upload para R2 ainda; armazenamento local em filesystem).
- Postgres com schema completo e migrations versionadas.
- Cobertura de testes unitários acima de 70% nas camadas críticas.

**Critério de saída:** sistema captura ininterruptamente por 14 dias, com precision ≥ 95% e recall ≥ 90% medidos manualmente sobre detecções conhecidas (manual significa: operador humano ouve as gravações e valida).

**Equipe esperada:** 1 dev sênior backend full-time, 1 dev pleno backend full-time, 1 consultor DSP/áudio part-time (análise dos primeiros resultados e tuning de parâmetros).

### 18.2 Fase 2 — Hardening e Coexistência (Semanas 7 a 18)

**Escopo:** 30 emissoras, 50 comerciais. Fase A da migração executada em paralelo.

**Entregas:**

- Stream workers escalados para 30, com restart preventivo escalonado em horários distintos.
- Calibração adaptativa por emissora implementada e funcionando para todas as 30.
- Camada de verificação neural integrada (CLAP exportado e servido).
- Evidence Service completo com upload para Cloudflare R2.
- API REST completa (versão 1) e webhooks funcionais.
- Frontend administrativo mínimo (gestão de emissoras, comerciais, campanhas, listagem de detecções).
- Dashboards Grafana completos.
- Alertas em produção com runbooks documentados.
- Backup de Postgres configurado e testado (restore de teste mensal).
- Política de retenção de evidências implementada (tiering automático após 30 dias).

**Critério de saída:** concordância igual ou superior a 95% com fornecedor atual por 3 semanas consecutivas. Sistema operando 24/7 com uptime igual ou superior a 99% mensal. Latência p95 de confirmação inferior a 10 segundos.

### 18.3 Fase 3 — Escala e Migração Comercial (Semanas 19 a 30)

**Escopo:** 200 emissoras, 200 ou mais comerciais. Fases B e C da migração.

**Entregas:**

- Distribuição de workers em 2 ou mais servidores físicos com pool de IPs distintos.
- Particionamento do Match Engine por hash de station_id se necessário.
- Postgres em alta disponibilidade (Patroni ou managed) com réplica de leitura.
- Failover automatizado entre servidores de aplicação.
- Documentação operacional completa (runbooks de todos os incidentes esperados).
- Onboarding gradual de clientes para o sistema novo, com processo formalizado.
- API v1 estável e documentada (OpenAPI/Swagger).

**Critério de saída:** 80% dos clientes migrados, fornecedor antigo em uso residual apenas para clientes mais sensíveis. Sem incidentes críticos por 30 dias consecutivos.

### 18.4 Fase 4 — Corte Total e Otimização (Semanas 31 a 36)

**Entregas:**

- Migração de 100% dos clientes restantes.
- Corte formal do contrato com fornecedor.
- Otimizações de custo (revisão de tiering, rightsizing de servidores).
- Implementação de funcionalidades diferenciadoras frente ao fornecedor:
  - Detecção de volume relativo (comercial tocado com áudio mais baixo do que o ambiente).
  - Detecção de sobreposição (DJ falando durante o comercial).
  - Detecção de versão veiculada vs versão contratada (campanha era para 30s mas tocou 15s).
  - Alertas em tempo real para comerciais críticos.
- Documentação institucional finalizada.

### 18.5 Cronograma Sumário

```
Semana    1   2   3   4   5   6   7   8   9   10  11  12  ...  30  31  ...  36
Fase 1    ████████████████████████
Fase 2                            ████████████████████████████████████████████████
Fase 3                                                            ████████████████████████████████████████████████
Fase 4                                                                                                            ████████████████████████
```

Total estimado: 36 semanas (aproximadamente 9 meses) para corte total do fornecedor. Possível compressão se equipe maior, possível extensão se houver problemas técnicos imprevistos especialmente em emissoras com cadeia de processamento atípica.

### 18.6 Marcos de Decisão (Go/No-Go)

| Marco | Quando | Critério | Decisão |
|-------|--------|----------|---------|
| Go Fase 2 | Fim semana 6 | Precision ≥ 95% em 5 emissoras | Avançar ou estender Fase 1 |
| Go Fase B (migração) | Fim semana 14 | Concordância ≥ 95% com fornecedor por 3 semanas | Iniciar piloto comercial ou continuar Fase A |
| Go Fase 3 | Fim semana 18 | Sistema estável em 30 emissoras por 4 semanas | Escalar ou estender hardening |
| Go Corte | Fim semana 30 | 80% migrado, sem incidente crítico em 30 dias | Cortar fornecedor ou estender coexistência |

---

## 19. Metodologia de Validação

### 19.1 Testes Unitários

- Cobertura mínima exigida: 70% nas camadas de lógica de negócio (matching, fingerprint, state machine, parsers).
- Cobertura ideal: 85% no Match Engine, que é o core do sistema.
- Bibliotecas: `testing` padrão Go com `testify` para assertions, `pytest` em Python.
- Testes obrigatórios em PR via CI antes de merge.

### 19.2 Testes de Integração

- **Pipeline completo end-to-end:** alimenta áudio gravado conhecido (incluindo um comercial cadastrado no minuto 5) ao Stream Ingestor mockado, valida que detecção é emitida no minuto 5 ± 5 segundos com confidence acima de 0.7.
- **API:** testes de contrato garantindo que endpoints não mudam formato sem versionamento explícito.
- **Hot reload:** valida que adicionar comercial novo durante operação ativa resulta em detecção em até 5 minutos.
- **Reconexão:** simula queda de stream e valida que worker reconecta e retoma análise sem perder detecções relevantes.

### 19.3 Golden Set para Regressão de Detecção

Mantemos um conjunto fixo (golden set) de pares (áudio_de_emissora, comercial_esperado_ou_null). Sempre que parâmetros do algoritmo mudam (ajuste de threshold, mudança em peak picking, novo modelo neural), rodamos contra o golden set:

- Nenhuma detecção previamente confirmada deixa de confirmar.
- Nenhum trecho previamente classificado como ausência de comercial passa a gerar match.
- Métrica resumo (precision e recall agregados) deve igualar ou superar a baseline.

Composição mínima do golden set:

- 200 detecções confirmadas com gravação completa (capturadas durante a operação real ou no shadow mode com fornecedor).
- 500 trechos de áudio de programas sem comerciais cadastrados, de várias emissoras (deve gerar zero falsos positivos).
- 50 trechos contendo música popular potencialmente similar a trilhas de comerciais (deve gerar zero falsos positivos).
- 20 trechos com comerciais sobrepostos por DJ ou vinheta de PA (deve detectar com confidence reduzida mas detectar).
- 20 trechos com versões diferentes do mesmo comercial (15s vs 30s) (deve identificar a versão correta).

### 19.4 Testes de Carga

Antes da Fase 3, simulamos carga de 200 streams via:

- Stack de mock streams gerados por ffmpeg lendo arquivos pré-gravados em loop, servidos por servidor Icecast local rodando em container.
- Match Engine deve manter latência p95 inferior a 100 ms por janela.
- Memória deve estabilizar (sem leak detectável em 72 horas de execução contínua, RSS dentro de 10% após estabilização inicial).
- CPU deve ficar abaixo de 60% médio em servidor de aplicação (margem para picos).

### 19.5 Testes de Resiliência (Chaos)

Antes da Fase 3, executamos exercícios de chaos engineering:

- Matar Postgres por 5 minutos: API deve retornar 503 com retry-after, workers continuam capturando, Match Engine continua com índice em memória.
- Matar NATS: Match Engine empilha eventos localmente, drena após retorno.
- Cortar rede de saída por 10 minutos: workers em modo reconnect, sem crash, drenam após retorno.
- Encher disco de evidências: alerta dispara, sistema entra em modo degradado (não gera novas evidências mas continua detectando).
- OOM em um worker: supervisor reinicia em até 30 segundos.

### 19.6 Métricas de Validação Contínua em Produção

- **Precision:** das detecções emitidas, quantas são verdadeiras (validadas amostralmente por amostragem cega de operador, ouvindo 10 detecções aleatórias por dia). Meta: ≥ 99%.
- **Recall:** das veiculações reais (conhecidas via fornecedor durante coexistência ou via reportes manuais), quantas detectamos. Meta: ≥ 95%.
- **Latência de confirmação:** tempo entre fim do comercial (timestamp inferido) e emissão do evento. Meta: p95 inferior a 10 segundos.
- **Latência de evidência:** tempo entre fim do comercial e disponibilização do clip M4A em URL. Meta: p95 inferior a 90 segundos.
- **Uptime de captura por emissora:** tempo conectado e recebendo áudio / tempo total. Meta: ≥ 99.5% mensal.
- **Falsos positivos por mil detecções:** Meta: inferior a 10.

Métricas são rastreadas em dashboard semanal e revisadas em reunião de operação.

---

## 20. Estimativa de Custos

### 20.1 Infraestrutura Mensal (Operação Estável, 200 emissoras)

| Item | Custo mensal (USD) |
|------|--------------------|
| 2× Hetzner AX52 (servidores de aplicação) | 140 |
| 1× Hetzner EX44 (banco de dados primário) | 70 |
| 1× Hetzner CX31 (coordenação, monitoring, NATS) | 10 |
| Cloudflare R2 (storage de evidências, cresce com tempo) | 50 a 200 |
| Cloudflare CDN/proxy + DNS para API | 20 |
| Domínios e certificados | 5 |
| Vault gerenciado ou self-hosted | 0 a 30 |
| Ferramentas de monitoring (addons Grafana Cloud opcionais) | 0 a 50 |
| **Total infra** | **~295 a 525** |

### 20.2 Equipe (Custo Brasil, valores aproximados de mercado)

**Para fase de implementação (mês 1 a 6):**

- 1 dev sênior backend: ~20.000 BRL/mês.
- 1 dev pleno backend: ~12.000 BRL/mês.
- 1 consultor DSP/áudio part-time durante meses 1 a 3: ~15.000 a 25.000 BRL no período total.

Custo total da implementação (6 meses): aproximadamente 220.000 BRL.

**Para fase de operação (após go-live):**

- 1 dev sênior part-time (manutenção, evoluções): ~10.000 BRL/mês.
- 1 SRE/operações part-time (operação 24/7 com on-call): ~8.000 BRL/mês.

Custo recorrente: aproximadamente 18.000 BRL/mês de equipe + 1.500 a 2.500 BRL/mês de infra (aproximadamente 300 a 500 USD).

Total recorrente: aproximadamente 20.000 BRL/mês.

### 20.3 Comparativo com Fornecedor Atual

Para comparar custo total do projeto frente ao custo atual do fornecedor, é necessário conhecer o valor pago hoje (não documentado nesta especificação por confidencialidade).

De forma geral, o projeto se justifica financeiramente se o fornecedor cobra valor superior a aproximadamente 35.000 BRL/mês ao longo de 24 meses, considerando custos de equipe e infra ao longo do período. Acima desse valor, o payback do investimento inicial ocorre dentro do primeiro ano.

Independentemente do payback financeiro estrito, há valor estratégico em:

- Eliminar dependência de fornecedor com atendimento ruim.
- Capacidade de oferecer features que o fornecedor não oferece (análise de fidelidade, alertas em tempo real, integrações customizadas com sistemas internos do cliente).
- Possibilidade de revender o serviço para outros clientes além dos atuais, transformando custo em receita.
- Controle total sobre roadmap, qualidade e prioridades.

### 20.4 Custos Não Recorrentes Adicionais

- Hardware ou licenças específicas: nenhum (toda stack é open source ou pay-as-you-go).
- Consultoria jurídica para validação de aspectos legais: ~5.000 a 15.000 BRL no projeto.
- Eventual pentest antes de go-live público da API: ~10.000 a 25.000 BRL.

---

## 21. Riscos do Projeto e Contingências

Diferente da seção 3 (riscos técnicos do produto em operação), esta seção lista riscos de execução do **projeto** em si.

### 21.1 Risco de Atraso na Fase 1

**Probabilidade:** alta.  
**Causa típica:** subestimação da complexidade de tuning do algoritmo. A primeira tentativa do cliente em fingerprinting já falhou, indicando que o problema tem nuances que só aparecem em contato com áudio real.  
**Impacto:** atraso de 2 a 4 semanas na Fase 1, cascateando em todas as fases subsequentes.  
**Mitigação:** alocar tempo de buffer de 30% nas estimativas, priorizar entrega de pipeline funcional ponta a ponta antes de otimização de qualidade. Iterar em ciclos curtos de uma semana com revisão de métricas.

### 21.2 Risco de Falha de Detecção em Emissoras Específicas

**Probabilidade:** média.  
**Causa típica:** algumas emissoras com cadeia de processamento atípica (muito antigas, ou muito modernas com processamento por IA) podem falhar mesmo com broadcast simulation tripla.  
**Impacto:** algumas emissoras na base de 1700 podem ser não atendíveis pelo nosso sistema.  
**Mitigação:** tratar caso a caso, possivelmente com fingerprints customizados (parâmetros específicos para a emissora). Em última instância, manter o fornecedor para essas emissoras específicas, sem invalidar o caso geral. Aceitar inicialmente que cobertura de 95% das emissoras é vitória, não 100%.

### 21.3 Risco de Bloqueio em Massa por Emissoras

**Probabilidade:** baixa, impacto alto.  
**Causa típica:** algumas emissoras grandes ou redes podem interpretar nossa atividade como abusiva e bloquear nossos IPs em massa.  
**Impacto:** perda temporária de capacidade de captura de várias emissoras simultaneamente.  
**Mitigação:** pool diversificado de IPs em datacenters distintos desde a Fase 1, distribuição de workers, contato proativo com afiliações de radiodifusão (ABERT, Aerp) para evitar mal-entendido institucional, headers HTTP de cliente real, não conexões persistentes infinitas (usar restart preventivo).

### 21.4 Risco de Mudança Tecnológica Disruptiva

**Probabilidade:** baixa.  
**Causa típica:** surgimento de codec ou plataforma de streaming que invalide nossa abordagem (ex: emissoras adotando criptografia de stream, ou DRM em rádio).  
**Impacto:** necessidade de reescrita parcial.  
**Mitigação:** arquitetura modular permite trocar componentes sem reescrever sistema. Ingestor é separado do Match Engine que é separado do Index. Manter monitoramento de tendências do mercado de broadcast.

### 21.5 Risco de Saída de Pessoa-Chave

**Probabilidade:** média.  
**Causa típica:** dev DSP ou sênior que entende profundamente o algoritmo sai durante a implementação.  
**Impacto:** perda de produtividade temporária, risco de conhecimento tácito perdido.  
**Mitigação:** documentação rigorosa (este documento é parte central disso, e deve ser mantido vivo), pair programming nas decisões críticas, code review obrigatório, runbooks completos, decisões arquiteturais registradas em ADRs (Architecture Decision Records).

### 21.6 Risco Regulatório

**Probabilidade:** muito baixa.  
**Causa típica:** mudança em legislação que afete monitoramento de broadcast ou captura de stream.  
**Impacto:** potencial necessidade de adequação ou descontinuação.  
**Mitigação:** acompanhamento jurídico contínuo, sistema é puramente passivo (não interfere no broadcast, apenas observa stream público). Documentar finalidade clara (comprovação contratual de veiculação publicitária).

### 21.7 Risco de Subdimensionamento da Complexidade

**Probabilidade:** média.  
**Causa típica:** o documento atual é detalhado mas não pode antecipar todos os detalhes que aparecem em produção. Bugs sutis em rotas raras, edge cases não imaginados.  
**Impacto:** atrasos repetidos, frustração de equipe.  
**Mitigação:** cultura de iteração rápida, dashboards desde o dia 1, alocação explícita de "tempo de bug" no cronograma (15% do tempo total), reviews semanais.

---

## 22. Decisões Pendentes e Premissas Assumidas

Este documento toma várias decisões. As decisões abaixo foram tomadas com base no contexto disponível mas devem ser revisitadas com stakeholders antes do início efetivo:

### 22.1 Decisões Técnicas Tomadas

| Decisão | Valor escolhido | Quando revisar |
|---------|-----------------|----------------|
| Linguagem dos workers | Go | Antes do início da Fase 1; Rust é alternativa válida se equipe tiver expertise. |
| Cloud primária | Hetzner | Antes da Fase 1; AWS/GCP se houver requisito corporativo de compliance ou contratos existentes. |
| Modelo neural | CLAP (LAION) | Após Fase 1, com base em testes empíricos; PANNs CNN14 é alternativa mais leve. |
| Frequência máxima de análise | 8 kHz | Após Fase 1, com base em observação de degradação por SBR; pode descer para 6 kHz se necessário. |
| Janela de análise | 4 segundos com hop de 2 segundos | Após Fase 1, conforme tradeoff entre latência e precisão. |
| Período de coexistência | 4 a 6 meses | Pode acelerar com mais testes de regressão automatizados. |
| Tiering de evidências | 30 dias quentes em SSD, depois R2 standard | Pode ajustar conforme padrão de acesso real. |

### 22.2 Premissas Assumidas

1. Empresa tem acesso aos masters dos comerciais via cliente (confirmado).
2. Empresa tem licença/legitimidade para gravar streams de rádio para fins de monitoramento contratado pelos clientes (assumido; jurídico deve validar antes do go-live público).
3. Concordância com fornecedor atual pode ser usada como ground truth aproximado (assumido; pode haver casos onde o fornecedor está errado e nós certos, ou vice-versa).
4. Volume não cresce além de 500 emissoras simultâneas em horizonte de 2 anos (assumido; arquitetura suporta mais com mais hardware).
5. Latência de evidência de 90 segundos é aceitável para clientes (a confirmar comercialmente; alguns clientes premium podem exigir menos).
6. Equipe tem ou pode adquirir conhecimento de Go, Python, Postgres e DSP básico de áudio (assumido).

### 22.3 Decisões Não Tomadas (Aguardando Input)

Decisões que dependem de informação adicional dos stakeholders:

- Qual interface administrativa será construída? Esta especificação assume web própria mínima. Alternativa: integração com CRM existente, sem frontend novo.
- Qual SLA contratual será oferecido aos clientes finais? Impacta dimensionamento de redundância e plano de DR.
- Quais clientes pilotos para Fase B? Depende de relacionamento comercial.
- Política exata de retenção de evidências por cliente? Default proposto: 12 meses, configurável por contrato.
- Como será o processo de cobrança/precificação para clientes? (Por emissora monitorada? Por detecção? Mensalidade fixa?)
- Existe interesse em vender o serviço para clientes além da carteira atual? Impacta priorização de features de SaaS multi-tenant.

---

## 23. Referências e Material de Estudo

- Wang, Avery. "An Industrial-Strength Audio Search Algorithm." Proceedings of the 4th International Conference on Music Information Retrieval (ISMIR), 2003. *O paper original do Shazam, base da abordagem de constellation/peak-pair.*
- Ellis, Daniel P. W. *audfprint*. https://github.com/dpwe/audfprint. *Implementação de referência em Python, MIT license.*
- Six, Joren. *Olaf*. https://github.com/JorenSix/Olaf. *Implementação leve em C, referência para otimização.*
- Wu, Yusong et al. "Large-Scale Contrastive Language-Audio Pretraining with Feature Fusion and Keyword-to-Caption Augmentation." ICASSP 2023. *CLAP, modelo neural sugerido para verificação.*
- Kong, Qiuqiang et al. "PANNs: Large-Scale Pretrained Audio Neural Networks for Audio Pattern Recognition." IEEE/ACM TASLP, 2020. *Alternativa mais leve ao CLAP.*
- ITU-R BS.1770-4. *Algorithms to measure audio programme loudness and true-peak audio level.* 2015. *Padrão de loudness usado no broadcast simulation.*
- ITU-R BS.450-3. *Transmission standards for FM sound broadcasting at VHF.* 2001. *Padrão de pre-emphasis FM.*
- Documentação ffmpeg: https://ffmpeg.org/documentation.html
- Documentação PostgreSQL 16: https://www.postgresql.org/docs/16/
- Documentação NATS JetStream: https://docs.nats.io/nats-concepts/jetstream
- Cloudflare R2 docs: https://developers.cloudflare.com/r2/

---

## 24. Apêndice A — Comandos ffmpeg de Referência

### A.1 Inspeção de Stream

```bash
# Verificar se URL é acessível e que formato entrega
ffprobe -v error -show_format -show_streams -of json "$STREAM_URL"

# Capturar 30 segundos para análise manual
ffmpeg -i "$STREAM_URL" -t 30 -c copy sample.aac

# Capturar 60 segundos como WAV para análise espectral
ffmpeg -i "$STREAM_URL" -t 60 -ar 44100 -ac 2 sample.wav
```

### A.2 Pipeline de Broadcast Simulation

Ver seção 7.2 para os três comandos completos (light, medium, heavy).

### A.3 Encoding de Evidência

Ver seção 11.2.

### A.4 Comandos de Teste do Pipeline

```bash
# Gerar fingerprint de um arquivo (a implementar no fingerprint/cli.py)
python -m fingerprint.cli generate \
  --input commercial.wav \
  --output commercial.fp.json \
  --variants light,medium,heavy \
  --rates 0.97,1.00,1.03

# Testar match com fingerprint conhecido
python -m fingerprint.cli match \
  --reference commercial.fp.json \
  --query stream_sample.wav \
  --threshold 0.6

# Inspecionar tamanho do índice em produção
redis-cli --scan --pattern "fp:hash:*" | wc -l

# Forçar reload do índice
curl -X POST http://localhost:8080/internal/index/reload \
  -H "Authorization: Bearer $ADMIN_JWT"
```

### A.5 Análise de Stream em Produção

```bash
# Ver últimos 60 segundos capturados de uma emissora
docker exec -it ingestor-server cat /var/spool/streams/$STATION_ID/last60.aac \
  | ffplay -

# Ver estatísticas de saúde do worker
curl http://localhost:9090/api/v1/query?query=worker_audio_seconds_processed_total
```

---

## 25. Apêndice B — Tabela de Constantes do Sistema

| Constante | Valor inicial | Onde aplica | Ajustável |
|-----------|---------------|-------------|-----------|
| SAMPLE_RATE | 16000 Hz | Pipeline análise | Não |
| WINDOW_SIZE | 4096 samples (256 ms) | STFT | Em revisão de algoritmo |
| HOP_SIZE | 2048 samples (128 ms) | STFT | Em revisão de algoritmo |
| FREQ_MIN_BIN | 25 (~100 Hz) | Limite inferior análise | Sim, por banda (AM/FM) |
| FREQ_MAX_BIN | 2048 (~8 kHz) | Limite superior análise | Sim, por banda |
| FREQ_MAX_BIN_AM | 1152 (~4.5 kHz) | Limite para emissoras AM | Sim |
| PEAK_NEIGHBORHOOD_F | 20 bins | Peak picking | Tuning |
| PEAK_NEIGHBORHOOD_T | 20 frames | Peak picking | Tuning |
| PEAK_AMPLITUDE_PERCENTILE | 75 | Threshold de pico | Tuning |
| TARGET_ZONE_T_MIN | 1 frame | Hash pairing | Tuning |
| TARGET_ZONE_T_MAX | 16 frames | Hash pairing | Tuning |
| TARGET_ZONE_F | 50 bins | Hash pairing | Tuning |
| FAN_OUT | 5 | Hashes por anchor | Tuning |
| ANALYSIS_WINDOW_SECONDS | 4 | Janela do stream | Tuning |
| ANALYSIS_HOP_SECONDS | 2 | Hop do stream | Tuning |
| EVIDENCE_BUFFER_MINUTES | 5 | Ring buffer evidência | Não |
| ANALYSIS_BUFFER_SECONDS | 30 | Ring buffer análise | Não |
| MIN_HASHES_PER_WINDOW | 5 | Match threshold default | Por emissora (calibração) |
| MIN_TEMPORAL_COVERAGE | 0.6 | Confirmação | Tuning |
| MIN_CONFIRMATIONS | 3 | State machine | Tuning |
| DELTA_BIN_SIZE_FRAMES | 2 | Tolerância alinhamento | Tuning |
| COOLDOWN_EXTRA_SECONDS | 5 | Após fim do comercial | Tuning |
| RECONNECT_INITIAL_DELAY_S | 1 | Backoff | Não |
| RECONNECT_MAX_DELAY_S | 60 | Backoff | Não |
| RECONNECT_JITTER_FRACTION | 0.5 | Backoff | Não |
| WORKER_RESTART_INTERVAL_H | 24 | Restart preventivo | Sim |
| HEALTH_CHECK_INTERVAL_S | 30 | Stream health | Sim |
| DEAD_AIR_THRESHOLD_DBFS | -60 | Detecção mudo | Sim |
| DEAD_AIR_DURATION_S | 60 | Detecção mudo | Sim |
| EVIDENCE_PRE_SECONDS | 60 | Buffer antes do match | Não |
| EVIDENCE_POST_SECONDS | 60 | Buffer depois do match | Não |
| NEURAL_VERIFY_THRESHOLD_HIGH | 0.85 | CLAP cosine | Tuning |
| NEURAL_VERIFY_THRESHOLD_LOW | 0.70 | CLAP cosine | Tuning |
| RATE_VARIANTS | [0.97, 1.00, 1.03] | Multi-rate matching | Tuning |
| BROADCAST_SIM_VARIANTS | [light, medium, heavy] | Pipeline referência | Não |

---

## 26. Apêndice C — Estrutura de Repositório

```
monitoramento/
├── README.md
├── docs/
│   ├── plano_implementacao.md         (este documento)
│   ├── adr/                            (Architecture Decision Records)
│   ├── runbooks/
│   │   ├── stream_down.md
│   │   ├── high_false_positive.md
│   │   ├── evidence_upload_failure.md
│   │   └── ...
│   ├── architecture/
│   └── api/
│       └── openapi.yaml
├── workers/                            (Go - serviços principais)
│   ├── cmd/
│   │   ├── ingestor/main.go
│   │   ├── matchengine/main.go
│   │   ├── evidence/main.go
│   │   └── api/main.go
│   ├── internal/
│   │   ├── ingestor/
│   │   │   ├── worker.go
│   │   │   ├── ffmpeg.go
│   │   │   └── ringbuffer.go
│   │   ├── match/
│   │   │   ├── engine.go
│   │   │   ├── hashes.go
│   │   │   ├── histogram.go
│   │   │   └── statemachine.go
│   │   ├── index/
│   │   │   ├── store.go
│   │   │   └── reload.go
│   │   ├── evidence/
│   │   │   ├── service.go
│   │   │   ├── extractor.go
│   │   │   └── uploader.go
│   │   ├── catalog/
│   │   ├── neural/
│   │   │   └── client.go               (cliente HTTP do servidor Python)
│   │   ├── storage/
│   │   ├── nats/
│   │   ├── metrics/
│   │   └── api/
│   │       ├── handlers/
│   │       └── middleware/
│   ├── pkg/
│   │   ├── audio/
│   │   │   ├── highpass.go
│   │   │   ├── rms.go
│   │   │   └── stft.go
│   │   ├── ringbuffer/
│   │   ├── ffmpeg/
│   │   └── ids/
│   ├── go.mod
│   └── go.sum
├── fingerprint/                        (Python - geração)
│   ├── fingerprint/
│   │   ├── __init__.py
│   │   ├── cli.py                      (entrypoint)
│   │   ├── generator.py                (algoritmo de hash)
│   │   ├── broadcast_sim.py            (cadeia ffmpeg)
│   │   ├── persistence.py              (Postgres + Redis)
│   │   ├── validation.py               (testes pós-geração)
│   │   ├── embeddings.py               (CLAP)
│   │   └── workers.py                  (Celery ou similar para batch)
│   ├── tests/
│   ├── pyproject.toml
│   └── poetry.lock
├── neural/                             (Python service - verificador)
│   ├── neural/
│   │   ├── server.py                   (FastAPI)
│   │   ├── model.py                    (CLAP loader, ONNX)
│   │   └── verify.py
│   ├── models/                         (artefatos ONNX, gitignored)
│   └── pyproject.toml
├── frontend/                           (opcional, fase 2+)
│   └── ...
├── infra/
│   ├── terraform/
│   │   ├── hetzner/
│   │   ├── cloudflare/
│   │   └── modules/
│   ├── ansible/
│   │   ├── playbooks/
│   │   ├── roles/
│   │   │   ├── postgres/
│   │   │   ├── nats/
│   │   │   ├── workers/
│   │   │   ├── monitoring/
│   │   │   └── common/
│   │   └── inventory/
│   ├── docker/
│   │   ├── docker-compose.yml          (dev e fase 1)
│   │   ├── docker-compose.prod.yml
│   │   └── Dockerfiles/
│   │       ├── workers.Dockerfile
│   │       ├── fingerprint.Dockerfile
│   │       └── neural.Dockerfile
│   └── k8s/                            (fase 3)
│       └── helm/
├── migrations/                         (goose)
│   ├── 0001_initial.up.sql
│   ├── 0001_initial.down.sql
│   └── ...
├── scripts/
│   ├── seed_dev.sh
│   ├── load_test.py
│   ├── compare_with_vendor.py          (script de validação na fase A)
│   └── golden_set/
│       ├── audios/
│       ├── expected.json
│       └── run_regression.sh
└── .github/
    └── workflows/
        ├── ci.yml                      (testes em PR)
        ├── build.yml                   (build de imagens)
        ├── deploy_staging.yml
        └── deploy_production.yml
```

---

## 27. Conclusão e Próximos Passos

Este documento descreve em detalhe o sistema completo de monitoramento de veiculação de comerciais em rádio AM e FM via streaming. Ele cobre desde os fenômenos físicos do broadcast que precisam ser modelados, passando pelo algoritmo de detecção, infraestrutura, operação, segurança, plano de migração e validação.

A abordagem adotada — fingerprinting acústico estilo Shazam adaptado com broadcast simulation múltipla, threshold adaptativo por emissora, state machine com cobertura temporal exigida, e camada neural opcional para casos limítrofes — é a estratégia tecnicamente mais sólida disponível para o problema, com ampla validação na literatura e em produtos comerciais consagrados.

O sistema é projetado para ser robusto contra os 37 riscos técnicos catalogados na seção 3, e para operar de forma confiável desde a Fase 1 (5 emissoras) até a Fase 4 (200 emissoras com migração concluída do fornecedor atual).

### 27.1 Próximos Passos Imediatos

1. **Revisão deste documento por stakeholders.** Validar premissas (seção 22.2), aprovar decisões pendentes (seção 22.3), confirmar cronograma (seção 18.5).
2. **Setup do repositório monorepo** com a estrutura definida no apêndice C.
3. **Provisionamento inicial de infraestrutura** para Fase 1 (1 servidor Hetzner AX52, 1 servidor para Postgres, conta R2).
4. **Identificação das 5 emissoras representativas** para Fase 1 (em conjunto com área comercial).
5. **Identificação dos 10 comerciais para Fase 1** (idealmente já validados pelo fornecedor atual, para facilitar comparação).
6. **Início da Fase 1** com sprint planning de 6 semanas.

### 27.2 Como Manter Este Documento Vivo

Este documento é vivo. Toda decisão técnica relevante tomada durante a implementação deve resultar em atualização das seções pertinentes ou em ADR (Architecture Decision Record) que complemente. Decisões que invalidem premissas aqui assumidas devem ser explicitamente registradas.

Revisão programada: ao final de cada fase, o documento é revisitado em sessão dedicada e atualizado.

### 27.3 Critério de Sucesso Final do Projeto

O projeto será considerado bem sucedido quando:

1. 100% dos clientes atualmente atendidos pelo fornecedor estiverem sendo atendidos exclusivamente pelo sistema interno.
2. Métricas de produção (precision, recall, latência, uptime) estiverem dentro das metas estabelecidas em 19.6 por pelo menos 90 dias consecutivos.
3. Custo total de operação for inferior ao custo do fornecedor anterior em pelo menos 40%.
4. Zero incidentes de severidade crítica nos últimos 60 dias.
5. Equipe tiver autonomia operacional, sem dependência do consultor DSP/áudio inicial.

Atingidos esses cinco critérios, o sistema é declarado em operação estável e o projeto encerrado, transitionando para modo de manutenção evolutiva.

---

**Fim do documento.**

