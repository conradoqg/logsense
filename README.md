# logsense

TUI de logs rápida e agradável usando Bubble Tea, Bubbles e Lipgloss. Lê logs de arquivo (com follow/tail) e/ou stdin, detecta formato, parseia e exibe em tabela estruturada com filtros, busca e inspector. Integra-se à OpenAI opcionalmente para detecção de schema, resumo e explicações.

## Instalação

Requer Go 1.22+.

```
go run ./cmd/logsense --help
```

Para build:

```
go build -o logsense ./cmd/logsense
```

## Uso básico

- Lendo via stdin:

```
cat testdata/json_lines.ndjson | logsense
```

- Lendo arquivo com follow:

```
logsense --file=/var/log/app.log --follow
```

- Modo demo (sem entrada):

```
logsense
```

## Flags principais

- `--file=PATH`: caminho do arquivo de logs
- `--follow`: seguir (tail -f)
- `--stdin`: força stdin (por padrão autodetecta se pipe)
- `--max-buffer=50000`: tamanho do ring buffer
- `--block-size-mb=16`: ao ler arquivo sem `--follow`, carrega apenas os últimos N MB (0 = arquivo inteiro)
- `--theme=dark|light`
- `--offline`: desativa OpenAI
- `--openai-model=...`, `--openai-base-url=...`
- `--log-level=info|debug`
- `--time-layout=...` forçar layout de tempo
- `--format=json|regex|logfmt|apache|syslog` forçar formato
- `--export=csv|json --out=PATH` exporta visão filtrada

## Atalhos

- Espaço: Pausar/Continuar
- `/`: Buscar (texto ou regex entre barras, ex: `/error|warn/`)
- `f`: Aba de filtros (em construção)
- `Enter`: Inspector
- `c`: Limpar buffer visível
- `t`: Alternar follow
- `e`: Exportar visão filtrada (usa flags `--export` e `--out` quando fornecidas)
- `s`: Resumir (OpenAI, em breve)
- `i`: Explicar (OpenAI, em breve)
- `r`: Re-detectar formato
- `g/G`: Ir para topo/base
- `?`: Ajuda (popup)
- `x`: Estatísticas da coluna selecionada (min/média/máx, distribuição ou valores distintos)

## OpenAI

Variáveis de ambiente:

- `OPENAI_API_KEY`: obrigatória para usar LLM
- `LOGSENSE_OPENAI_MODEL`: default `gpt-4o-mini`
- `LOGSENSE_OPENAI_BASE_URL`: compatibilidade com proxies

O cliente implementa timeout, usando `github.com/sashabaranov/go-openai`. Chamadas são sinalizadas na UI (spinner e mensagem).

## Observações

- Leitura de arquivos grandes é feita por bloco (últimos N MB) para evitar cargas de memória desnecessárias.
- Detecção de formato: heurística rápida nas primeiras ~10 linhas; se houver muitas falhas de parsing consecutivas, é feita redetecção. Se heurísticas forem inconclusivas e houver OpenAI configurado, chama o LLM (indicador visual na barra de status).

## Testes e exemplos

```
go test ./...
```

Arquivos de exemplo em `testdata/`:

- `json_lines.ndjson`
- `logfmt.log`
- `apache_combined.log`
- `syslog.log`
- `k8s_container.json`

## Roadmap

- Filtros avançados (nível, intervalo de tempo, expressões govaluate)
- Summarize/Explain via OpenAI com redaction opcional
- Cache de schema por assinatura de origem
- Export de Markdown summary
- Tema claro/escuro refinado
