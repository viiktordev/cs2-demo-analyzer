# pro-coach-cs2

Ferramenta de análise de demos de CS2. Backend em Go (`net/http` puro) que
parseia arquivos `.dem` e serve um frontend estático em HTML/CSS/JS — sem
framework e sem build step.

Demos comprimidas são aceitas direto: FACEIT entrega `.dem.zst` e downloads da
Valve vêm em `.dem.bz2`. O formato é detectado pelos magic bytes e descomprimido
em stream, sem passo manual.

Com uma chave de API configurada, um modelo lê as estatísticas e devolve coaching
escrito — pontos fortes, erros recorrentes e o que treinar — além de comparar a
evolução entre partidas. **OpenAI e Anthropic são suportadas**; escolha pela
chave que você tiver. Sem nenhuma, tudo o mais funciona igual.

O fluxo é em duas passadas. O upload roda só um **scan de elenco** — cerca de
100 ms, lendo 2% do arquivo — que devolve o mapa e quem jogou. O parse completo
só começa depois que você escolhe **um** jogador, e coleta as estatísticas dele
apenas. A demo é apagada do servidor em seguida: analisar um segundo jogador
significa enviá-la de novo.

## Requisitos

- Go 1.24+

## Como rodar

```bash
make run
```

Abra <http://localhost:8080>. O binário serve o frontend em `/` e a API em
`/api/*`, então não há CORS envolvido.

Outros alvos: `make build`, `make test`, `make vet`, `make tidy`, `make clean`.

### Configuração

| Flag        | Env            | Padrão                          | Descrição                          |
| ----------- | -------------- | ------------------------------- | ---------------------------------- |
| `-addr`     | `ADDR`         | `:8080`                         | Endereço de escuta                 |
| `-frontend` | `FRONTEND_DIR` | `../frontend`                   | Diretório estático (vazio desliga) |
| `-uploads`  | `UPLOAD_DIR`   | `$TMPDIR/pro-coach-cs2-uploads` | Onde a demo espera entre o scan e a análise |
| `-data`     | `DATA_DIR`     | `./data`                        | Onde as análises ficam guardadas, por SteamID |
| `-provider` | `AI_PROVIDER`  | `auto`                          | `openai`, `anthropic`, ou `auto` para a chave que existir |
| `-openai-key` | `OPENAI_API_KEY` | vazio                       | Chave da OpenAI |
| `-anthropic-key` | `ANTHROPIC_API_KEY` | vazio                  | Chave da Anthropic |
| `-model`    | `AI_MODEL`     | padrão do provider              | Sobrescreve o modelo escolhido |
| —           | `ENV_FILE`     | `.env`, depois `../.env`        | Caminho do arquivo de ambiente |

Os arquivos em `-uploads` são temporários: cada um vive do upload até a análise
que o consome. O servidor limpa os órfãos no boot e no shutdown, e só remove
arquivos com a extensão `.upload` que ele mesmo criou — apontar `-uploads` para
uma pasta cheia não destrói nada de terceiros.

## API

| Método | Rota               | Descrição                                        |
| ------ | ------------------ | ------------------------------------------------ |
| `GET`  | `/api/health`      | Status e versão do serviço                       |
| `GET`  | `/api/demos`       | Lista os uploads, sem elenco nem análise         |
| `POST` | `/api/demos`       | Upload multipart (campo `demo`); devolve mapa e elenco |
| `GET`  | `/api/demos/{id}`  | Um upload: elenco e, se já rodou, a análise      |
| `POST` | `/api/demos/{id}/players/{steamId}/analyze` | Parse completo do jogador escolhido; consome a demo |
| `POST` | `/api/analyses/{id}/coaching` | Roda o Claude sobre a análise e guarda o relatório |
| `GET`  | `/api/players/{steamId}` | Todas as partidas analisadas do jogador |

`analyze` é `POST` porque tem efeito colateral: ele apaga o arquivo. Uma segunda
chamada no mesmo upload responde `410 Gone`. Se o parse falhar, o arquivo é
preservado para nova tentativa.

```bash
curl -s localhost:8080/api/health | jq

# 1. upload — devolve o elenco, sem parsear a partida
curl -s -F "demo=@/caminho/partida.dem.zst" localhost:8080/api/demos | jq

# 2. escolhe um jogador — aqui sim roda o parse
curl -s -X POST localhost:8080/api/demos/$ID/players/$STEAM_ID/analyze | jq
```

Resposta do upload (`201`) — só identificação, ainda sem números:

```json
{
  "id": "1ff4496c…",
  "fileName": "partida.dem.zst",
  "status": "pending",
  "map": "de_mirage",
  "players": [
    { "steamId": "76561198410645260", "name": "MuriloAS", "team": "team_pdlzera", "side": "T" }
  ],
  "uploadedAt": "2026-08-27T04:17:00Z"
}
```

Resposta do `analyze` (`200`) — `status` vira `analyzed` e `analysis` traz a
partida com o jogador escolhido:

```json
{
  "id": "1ff4496c…",
  "status": "analyzed",
  "analysis": {
    "map": "de_mirage",
    "rounds": 22,
    "score": [ { "side": "CT", "name": "team_A", "score": 9 },
               { "side": "T",  "name": "team_B", "score": 13 } ],
    "roundHistory": [ { "number": 1, "winner": "T", "reason": "bomb exploded" } ],
    "player": { "steamId": "76561198410645260", "name": "MuriloAS",
                "rating": 1.68, "adr": 112.18, "kast": 77.27 }
  }
}
```

Erros vêm sempre como JSON: `{"error": "..."}`. Uploads acima de 500 MiB
recebem `413`; arquivos que não são demos válidas recebem `400`.

### Estatísticas por jogador

Cada jogador traz identificação (`steamId`, `name`, `team`, `side`), o básico
(`kills`, `deaths`, `assists`, `headshots`, `damage`, `roundsPlayed`), taxas
derivadas (`kd`, `headshotPct`, `adr`, `kast`, `rating`) e recortes de
performance: `multiKills` (índice 0 = 1k … 4 = 5k), `openingKills`/
`openingDeaths`, `tradeKills`, `clutchesWon`/`clutchesPlayed`, `utilityDamage`,
`enemiesFlashed`, `flashAssists` e `bombsPlanted`/`bombsDefused`.

Junto vem `rounds`: um registro por round com kills, assists, mortes, dano, se
sobreviveu, se foi trocado e se o round contou para o KAST.

Definições que valem registrar:

- **`steamId` é string**, não número — um SteamID64 passa de `2^53` e perderia
  precisão em JavaScript.
- **`rating`** segue a fórmula HLTV 1.0 (output de kills, sobrevivência e rounds
  com multi-kill, cada um normalizado pela média da liga).
- **`kast`** é a fatia de rounds com kill, assist, sobrevivência ou trade. Um
  trade é a morte do assassino por um companheiro em até 5 segundos.
- **`damage`** conta só dano em inimigos e usa `HealthDamageTaken`, que despreza
  o excesso — bater 100 em quem tem 5 de vida conta 5, como no placar do jogo.
- **`clutchesPlayed`** conta todo round iniciado como último vivo do time contra
  ao menos um inimigo, inclusive 1v5 sem chance. Como o último vivo do time
  perdedor está sempre nessa situação, o número de disputas é alto e o de
  vitórias, baixo.
- **`mvps`** fica em 0 em demos cujo servidor não emite o evento `round_mvp` —
  comum em plataformas de terceiros como a FACEIT.
- **O `side` do elenco é o do primeiro tempo**, já que o scan para no início da
  partida. O `side` dentro da análise é o do fim, depois da troca de lados.

## Análise de IA

O `GET /api/health` responde `"coaching": true|false` — é assim que o front sabe
se deve oferecer o botão.

### Escolhendo o provider

| Provider | Modelo padrão | Variável |
| --- | --- | --- |
| OpenAI | `gpt-5.5` | `OPENAI_API_KEY` |
| Anthropic | `claude-opus-5` | `ANTHROPIC_API_KEY` |

Com `auto` (o padrão), vale a chave que estiver definida. **Se as duas
estiverem**, `auto` escolhe a Anthropic — defina `AI_PROVIDER=openai` para mandar
na OpenAI e não deixar uma chave esquecida redirecionar a cobrança.

O `GET /api/health` devolve `provider` e `model`, e a interface mostra qual está
em uso no canto superior direito.

Só a chamada muda entre os dois: prompt, schema e parsing são compartilhados
(`internal/coach/coach.go` orquestra; `anthropic.go` e `openai.go` implementam a
interface `client`). O schema é escrito na interseção do que ambos aceitam — o
que na prática significa o subconjunto estrito da OpenAI: toda propriedade em
`required`, `additionalProperties: false` em cada objeto e nada de `maxItems`.
Os limites de tamanho das listas vivem nas descrições, e há teste garantindo que
o schema não saia dessa interseção.

### Configurando a chave

A forma mais prática é um `.env` na raiz do repositório:

```bash
cp .env.example .env
$EDITOR .env          # preencha OPENAI_API_KEY ou ANTHROPIC_API_KEY
make run
```

O servidor procura o `.env` no diretório de trabalho e depois um nível acima, o
que faz o arquivo da raiz valer tanto para `make run` (que roda de dentro de
`backend/`) quanto para o binário executado direto. `ENV_FILE` sobrescreve o
caminho.

**Variáveis já definidas no shell vencem o arquivo**, então dá para sobrepor num
comando só:

```bash
OPENAI_API_KEY=sk-... make run
```

O `.env` está no `.gitignore`. O `.env.example` não — nunca coloque uma chave
real nele. Evite também as flags `-openai-key` e `-anthropic-key`: o valor fica
visível no `ps` e no histórico do shell.

Formato aceito: comentários com `#`, prefixo `export` opcional, e aspas simples
ou duplas. Comentários no fim da linha **não** são removidos, de propósito — uma
chave contendo `#` colada sem aspas não pode ser truncada em silêncio.

```bash
curl -s -X POST localhost:8080/api/analyses/$ID/coaching | jq
```

Sem chave, o endpoint responde `503` com uma mensagem explicando o que falta e
**nada mais é afetado**.

### Como o prompt é montado

O ponto central do desenho é a divisão de trabalho: **o Go calcula, o modelo
interpreta.** Mandar dados brutos seria inviável e pior — uma partida tem ~1,5
milhão de amostras de posição, o que passa de 25M de tokens contra o 1M de
contexto do modelo, e um LLM erra aritmética que o Go acerta de graça.

Então o parse produz features derivadas e compactas, e só elas vão para o
modelo. Na demo de teste isso dá **~4,8 mil tokens de entrada** — na casa de
centavos por análise em qualquer um dos dois providers.

As features vivem em `PlayerStats.Coaching` (`internal/demo/coaching.go`):

| Feature | O que revela |
| --- | --- |
| `deathClusters` | mortes agrupadas por proximidade — "morreu 3× no mesmo canto nos rounds 2, 5 e 6" |
| `accuracyByClass` | acerto por classe de arma |
| `byBuyType` | desempenho por economia: pistol, eco, force, full |
| `winRateWhenSurviving` / `winRateWhenDying` | se a morte do jogador é o que decide o round |
| `utilityWastedRounds`, `teamFlashes` | granada comprada e não usada, e flash no próprio time |

Os clusters de morte usam coordenadas cruas, **sem calibração de radar**: são
comparáveis entre si, mas não viram nome de callout. Isso é proposital — os
radares são assets da Valve, e assim a coisa funciona em qualquer mapa.

O relatório volta em JSON estruturado (`summary`, `strengths`, `mistakes`,
`drills`, `trend`), o que deixa o front renderizar seções fixas em vez de
interpretar prosa.

### Comparação entre partidas

As análises são gravadas em `-data`, uma por arquivo, indexadas por SteamID:

```
data/players/<steamId>/<analysisId>.json
```

Isso sobrevive a restart — o store em memória não. Quando o jogador já tem
partidas anteriores, os agregados delas entram no prompt e o relatório ganha uma
seção `trend`. Só agregados: carregar o detalhe round a round de cada partida
antiga inflaria o contexto sem melhorar a leitura.

Um diretório por jogador torna "todas as partidas deste jogador" um `readdir`,
que é toda a indexação necessária nessa escala. Trocar por SQLite depois é
reimplementar `Save`, `Get` e `ByPlayer` em `internal/store/analyses.go`.

## Estrutura

```
backend/
  cmd/api/            entrypoint: flags, servidor HTTP, graceful shutdown
  internal/api/       rotas, handlers, middlewares, helpers de resposta
  internal/demo/      parsing de .dem (demoinfocs-golang v5)
                      roster.go: scan barato do elenco (para no MatchStart)
                      parser.go: Analyze() → partida + 1 jogador
                      collector.go: eventos → estatísticas do jogador alvo
                      stats.go: taxas derivadas (KAST, ADR, rating)
                      compression.go: zstd/gzip/bzip2 transparente
  internal/coach/     coach.go: prompt, schema e parsing (comum aos providers)
                      anthropic.go / openai.go: as duas implementações
  internal/config/    leitura do .env
  internal/store/     memory.go: ciclo do upload (pending → analyzing → analyzed)
                      analyses.go: análises em disco, por SteamID
frontend/
  index.html          dropzone, seletor, resultado, detalhe, IA e histórico
  css/style.css       tema escuro, sem dependências
  js/api.js           wrappers de fetch/XHR para /api/*
  js/app.js           eventos de UI e renderização
```

## Testes

```bash
make test
```

`internal/demo/parser_test.go` roda contra qualquer demo colocada em
`backend/internal/demo/testdata/` (`.dem` ou comprimida) e valida que o
histórico de rounds bate com o placar. O teste é pulado quando não há nenhuma,
já que demos são grandes demais para versionar.

## Estado atual e próximos passos

O `Summary` de hoje é raso de propósito: mapa, tick rate, duração, placar final
e histórico de rounds. Ainda por fazer:

- utility mais fina (flashes efetivas por duração, linhas de smoke)
- posicionamento e mapas de calor
- persistência real no lugar do store in-memory (hoje um restart perde tudo)
- processamento assíncrono da análise (o parse é síncrono na request)
- comparar jogadores exigiria guardar a demo entre análises
- o relatório de IA não acompanha o `GET /api/demos/{id}`: o botão sempre aparece
  e o servidor devolve o relatório já gerado sem cobrar de novo

### Notas de parsing

- O evento `bullet_damage` é CS2-only mas **muitos servidores não o emitem** — a
  FACEIT inclusa. Os acertos vêm de `PlayerHurt`, senão a precisão sairia zerada
  em silêncio. Efeito colateral: uma rajada de shotgun é um disparo com vários
  acertos, então a precisão da classe `heavy` lê alto.
- `EquipmentValueFreezeTimeEnd()` está **atrasado um round** no momento em que o
  `RoundFreezetimeEnd` dispara; o valor certo ali é `EquipmentValueCurrent()`.
- O dinheiro no fim do round já inclui a premiação, então a sobra é medida no fim
  do freeze time.

- A `demoinfocs` v4 não parseia demos CS2 recentes (`unable to find existing
  entity`); o projeto usa a v5, onde o header do demo deixou de ser exposto — o
  nome do mapa vem de `CSVCMsg_ServerInfo`.
- O round de faca aparece como um `RoundEnd` antes do `MatchStart`; o histórico é
  zerado nesse evento para bater com `TotalRoundsPlayed`.
