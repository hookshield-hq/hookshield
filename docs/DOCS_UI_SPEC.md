# 🛡️ HookShield — UX/UI Architecture & Design System Specification
> **Diretrizes de Design e Arquitetura de Interface para o Webhook Hub & Gateway Open-Core**

---

## 1. Princípios Globais de Interface (Design Principles)

Para ser a melhor interface do mercado para desenvolvedores e engenheiros de integração, o HookShield deve equilibrar **densidade de informação** e **elegância minimalista**. 

* **Dark-First Sovereignty:** O modo escuro é o padrão absoluto (`#090D16`), com contraste calculado para evitar fadiga visual em longas sessões de debug. Suporte a Light Mode nativo mantendo a legibilidade.
* **Low-Latency Interactions:** Transições fluídas (máximo `150ms` em ease-out) sem *layout shift*. A interface responde instantaneamente a ações de cópia, criação e filtros.
* **Keyboard-Centric Navigation:** Atalho global `Cmd+K` / `Ctrl+K` para busca e navegação rápida entre endpoints, logs, chaves e configurações sem tirar as mãos do teclado.
* **Glassmorphism & Surface Depth:** Hierarquia visual baseada em camadas translúcidas (`backdrop-blur-md`), bordas finas com brilho sutil (`1px border-white/10`) e sombras projetadas para criar profundidade.

---

## 2. Token de Design & Paleta de Cores (Design Tokens)

### 2.1 Tema Escuro (Padrão)
| Aplicação | Cor / Valor CSS | Descrição |
| :--- | :--- | :--- |
| **Background Principal** | `#090D16` | Azul/Preto ultra-escuro de fundo estático |
| **Surface (Cards/Panéis)** | `rgba(15, 23, 42, 0.65)` | Vidro escuro translúcido com `backdrop-blur-md` |
| **Border / Dividers** | `rgba(255, 255, 255, 0.08)` | Linha fina de definição de estrutura |
| **Primary Accent (Gradient)** | `from-#8B5CF6 to-#06B6D4` | Roxo elétrico para Ciano vibrante |
| **Text Primary** | `#F8FAFC` | Branco frio de alto contraste |
| **Text Secondary** | `#94A3B8` | Cinza neutro para labels e metadados |
| **Status Success** | `#10B981` / `rgba(16, 185, 129, 0.15)` | Verde esmeralda para entregas 2xx |
| **Status Warning/Retry** | `#F59E0B` / `rgba(245, 158, 11, 0.15)` | Âmbar para retentativas em andamento |
| **Status Error** | `#EF4444` / `rgba(239, 68, 68, 0.15)` | Vermelho vibrante para falhas 4xx/5xx |

---

## 3. Especificação das Telas Principais (Core App Views)

### 3.1 Global Header & Navigation
* **Top Bar Fixa:** Logo do HookShield com ícone em gradiente à esquerda.
* **Barra de Busca (`Cmd + K`):** Input centralizado translúcido com atalho visível. Permite pesquisar por *ID de Request*, *Nome de Endpoint*, *Evento* ou *Status Code*.
* **Status do Cluster:** Indicador em tempo real (bolinha pulsante verde) com o throughput global (ex: `1.240 req/s • 99.98% success`).

---

### 3.2 Dashboard Principal (Real-Time Performance)
A Dashboard oferece visão instantânea da saúde da infraestrutura sem poluição.

1. **Metrics Summary (Cards Minimalistas no Topo):**
   * Total Ingestion (Volume de requisições processadas).
   * Delivery Rate (% de sucesso no envio final).
   * Average Latency (Tempo médio de processamento do gateway em `ms`).
   * Active Retries (Fila atual de retentativas pendentes).

2. **Traffic Live Chart (Gráfico Interativo Canvas):**
   * Gráfico de linhas fluido representando o tráfego de entrada vs. saída nas últimas 24h/7d.
   * Alternância rápida para ver latência, taxa de erro ou volume por segundo.

3. **Live Stream Feed (Painel Inferior):**
   * Lista em *real-time* (WebSockets/SSE) dos últimos webhooks trafegados com indicador de status colorido, método HTTP, endpoint de origem e latência.

---

### 3.3 Gestão de Entradas (Ingestion & Endpoints)
Painel onde o desenvolvedor cria e gerencia os pontos de entrada do HookShield.

* **Listagem Simplificada:** Cards em grade ou tabela densa mostrando:
  * URL Única do Endpoint (`https://in.hookshield.dev/v1/ep_xyz123`).
  * Botão de cópia em 1 clique com feedback visual micro-animated.
  * Status (Ativo, Pausado, Modo Debug).
  * Taxa de sucesso individual.
* **Modal / Drawer de Criação:**
  * **Nome & Descrição:** Identificador claro (ex: *Stripe Webhook Gateway*).
  * **Autenticação de Entrada:** Opção para ativar verificação de Token no Header, HMAC Signature ou IP Whitelisting.

---

### 3.4 Transformador de Payload (Header & Body Pipeline Editor)
A funcionalidade central para modificar dados de saída e enriquecer a requisição antes de atingir o destino final.

* **Split Editor Layout:**
  * **Lado Esquerdo (Input Data):** Editor com realce de sintaxe JSON/Payload recebido no evento.
  * **Centro (Transform Pipeline):** Interface visual em blocos ou editor leve de scripts/máscaras (JSONPath/JavaScript leve).
    * **Add/Modify Headers:** Tabela chave-valor simples (ex: `Authorization: Bearer {{secrets.MY_API_KEY}}`).
    * **Add/Modify Body:** Adição de campos estáticos, data/timestamp ou renomeação de chaves (ex: converter `user_id` em `customerId`).
  * **Lado Direito (Output Preview):** Visualização em tempo real de como o payload final e os headers serão enviados ao destino após o processamento.

---

### 3.5 Configuração de Retentativas (Retry Policy)
Interface para configurar a resiliência do sistema em caso de falha do serviço de destino.

* **Visual Exponential Backoff Selector:**
  * Escolha de estratégia via seletor visual: *Fixed Interval*, *Exponential Backoff*, ou *Custom Rule*.
* **Parâmetros Claras:**
  * Máximo de Tentativas (Ex: `5 tentivas`).
  * Intervalo Inicial (Ex: `10s`).
  * Backoff Multiplier (Ex: `2x`).
  * Max Delay Limit (Ex: `1 hora`).
* **Dead Letter Queue (DLQ) Toggle:** Alternador rápido para enviar eventos falhos após *N* tentativas para uma fila de armazenamento permanente de auditoria.

---

### 3.6 Auditoria & Debugger de Logs (Request Inspection)
Ferramenta para inspecionar requisições com precisão cirúrgica.

* **Filtros Avançados:** Filtre por *Data/Hora*, *Endpoint*, *Status HTTP (2xx, 4xx, 5xx)*, *Event Type* ou *Payload Key*.
* **Request Detail View (Drawer Lateral):**
  * **Timeline do Evento:** Visualização cronológica precisa: `Ingestion -> Authentication -> Pipeline Transformation -> Delivery Attempt 1 (Failed 503) -> Retry Attempt 2 (Success 200)`.
  * **Tabs de Inspeção:**
    1. *Original Request:* Headers e Body originais recebidos.
    2. *Transformed Request:* Headers e Body modificados enviados.
    3. *Destination Response:* Headers e Body retornados pelo servidor final.
  * **Botão "Replay Request":** Reenvia a requisição exatamente igual ou com payload modificado diretamente pelo painel para testes instantâneos.

---

### 3.7 Gerenciamento de Chaves de API (API Keys & Access Tokens)
Segurança simplificada sem complexidade desnecessária.

* **Tabela de API Keys:**
  * Nome/Propósito da Chave (ex: *Production Ingestion Key*).
  * Prefixo Visível (ex: `hs_live_8f3a...`).
  * Data de Criação e Último Uso (*Last Used 2m ago*).
  * Escopo de Permissões (*Read-Only*, *Ingest*, *Admin*).
* **Fluxo de Criação de Chave:**
  * Modal limpo gerando o secret em tela.
  * Botão de cópia única com aviso de segurança visual.
  * Mascaramento automático após fechar a janela.

---

## 4. Próximos Passos recomendados para Desenvolvimento

1. Salvar este documento como `DOCS_UI_SPEC.md` no repositório `hookshield-hq/hookshield`.
2. Utilizar as definições de tokens CSS para construir os componentes base no Next.js com Tailwind CSS (Cards, Editors, Modals e Headers).
3. Conectar a estrutura de rotas do frontend com a API do backend em Go.