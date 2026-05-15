---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - frontend/src/
  - (design system compartilhado entre Radiocheck e E-radios/Signalads)
---

# E-radios — Design System & Guidelines (V2)

> **Relacionados**: Para detalhes do frontend (componentes, contexts, utils), veja `frontend.md`. Para tech stack, veja `techstack.md`. Para CSS variables (tokens), veja `signalads-frontend/src/styles/variables.css`.

Diretrizes de design, estrutura visual, componentes e micro-interacoes da plataforma Signalads.

---

## 1. O Padrão de Cores Core (Design Tokens - CRÍTICA)

O sistema de cores da Signalads possui uma nomenclatura de variáveis (`primary`, `secondary`, `tertiary`), mas seu **uso prático** diferencia completamente do padrão da indústria. A estética da plataforma é baseada em "Clean Light" com toques vibrantes em Rosa Digital (Tertiary) para **absolutamente todas as ações**.

*   **Fundos e Superfícies (A Base Visível):**
    *   Fundo principal das páginas (`--color-gray-50` / `#f8fafc`): Um tom extremamente claro, quase branco.
    *   Superfícies (Cards, Elementos, Listas, Modais): Sempre `var(--color-white)`.
    *   Bordas estruturais contínuas e divisores: `var(--color-gray-200)` e `var(--color-gray-100)`.

*   **A Cor de Ação Absoluta (Tertiary / Rosa Digital - IMPORTANTÍSSIMO):**
    *   Na Signalads, a cor chamada `--color-tertiary-500` (`#E81E75`) atua na prática como a **Cor Primária de Ação**.
    *   **Tudo** que é clicável, focável, importante ou ativo utiliza essa paleta.
    *   Hover de botões (`--color-tertiary-600`), Ícones de cabeçalhos (Carrinho, Títulos da página), Outline ao dar `<input>:focus` (`box-shadow: 0 0 0 3px rgba(236, 72, 153, 0.1)`).
    *   Seleções ativas (Tabs ativas, Checkboxes ativos, Range Sliders, Badges do Carrinho, Botões de Favoritos, Charts de Custo). A identidade da Signalads é movida pelo Rosa (Tertiary) sob um fundo branco.

*   **As Cores "Primária" e "Secundária" (Uso Restrito):**
    *   `--color-primary-*` (Azul Digital) e `--color-secondary-*` (Roxo Digital) **NÃO** são as cores principais de botões.
    *   Elas são restritas a **Gradientes** (como os Orbs animados de background na página de Login: `linear-gradient(135deg, #ec4899 0%, #8b5cf6 50%, #3b82f6 100%)`) MESMO que gradiente não sejam mais usados em plataforma.
    *   Também podem ser usadas de forma bem específica e esparsa, como "badges" de tipos de conta (Primária para emissora, Secundária para agência), ou status de PMM, mas **nunca** como a ação padrão de uma interface genérica.

*   **Textos (Tipografia em Escala de Cinza):**
    *   Títulos Escuros / Títulos de Cards / Títulos de Seção: `--color-gray-900` (`#06055B` - Azul Marinho muito escuro) ou `--color-gray-800`.
    *   Textos comuns e parágrafos: `--color-gray-700` ou `--color-gray-600`.
    *   Textos de apoio (Labels, Meta, Subtítulos, Dials): `--color-gray-500`. 

---

## 2. Tipografia

A tipografia da Signalads foca numa combinação moderna e funcional:

*   **Títulos e Cabeçalhos (h1, h2, h3, Títulos de Painéis e Cards):**
    *   `var(--font-family-primary)` = `'Space Grotesk', -apple-system, sans-serif`
    *   `font-weight: 700` (Bold)
    *   Espaçamento negativo suave (`letter-spacing: -0.02em`) em layouts com apelo de landing page (ex: Login).
*   **Corpo de Texto, Labels, Values e Botões:**
    *   `var(--font-family-secondary)` = `'Fira Sans Condensed', -apple-system, sans-serif`
    *   Pesos que variam do `400` (textos normais) ao `600` (SemiBold - em valores numéricos e Labels fortes).

---

## 3. Padrões de Layout, Arredondamentos e Espaçamentos

O design adota cantos altamente arredondados (Soft UI) e bastante espaço em branco constante (Airy Design). Evitar designs exíguos ou densos.

*   **Border Radius (Bordas):**
    *   Modais, Cards Maiores, Sidebar e Header: `--radius-xl` (geralmente 16px - 24px).
    *   Inputs de formulário, botões da plataforma padrão: `--radius-md` (8px) ou `--radius-lg` (10px a 14px dependendo da área).
    *   Botões de Seleção, Filtros, "Chips" e Badges: `--radius-full` (cantos totalmente redondos/pílulas).
*   **Container de Páginas:**
    *   `max-width: 1440px` no `.container` centrado (exceção no Compare que é mais flexível/side-by-side). Sempre utilize display flex em eixo coluna (`min-height: 100vh`) com `.mainWrapper`.
*   **Split Layouts (Como visto no /compare e no /cart):**
    *   Uso de CSS Grid para separar áreas de visão mestre-detalhe ou ação-sumário (`grid-template-columns: 1fr 320px` ou `400px 1fr`).
    *   Sempre envolva a área que lista recursos com a Sidebar lateral com o `gap: var(--spacing-xl)` (ou `md` para mobile).
*   **Sticky Footers:**
    *   Em páginas focadas em conversão/assistente (Compare, Checkout), as ações de resumo total, "Adicionar ao Carrinho" ficam flutuando no rodapé (`position: fixed; bottom: 0`). Quando isso ocorrer, o container mestre ganha um `padding-bottom: 120px` massivo.

---

## 4. UI Components e Micro-interações Exigidas

Absolutamente nenhum elemento novo deve parecer "seco" e estático. A interface usa micro-interações onipresentes.

### 4.1. Interação do Card de Interface (O Padrão Ouro)
Seja no Marketplace, Cart ou Compare, os blocos de entidades (Emissoras):
1.  Começam com `box-shadow: var(--shadow-sm)` e border de cor cinza (`color-gray-200`).
2.  No `:hover`, eles sempre sofrem translação leve no eixo Y (`transform: translateY(-2px);` ou semelhante) e o shadow aumenta (`box-shadow: var(--shadow-md)` a `--shadow-xl`).
3.  No `:hover`, a borda **obrigatoriamente muda** para `--color-tertiary-300`, acendendo o card em rosa. Todo botão interno de favoritar também "acorda" nessa mesma paleta.
4.  Indicadores de Hover: Na lista do Marketplace, há uma barrinha sutil de gradiente na base e no topo que anima do width 0 para 1 (`transform: scaleX`).

### 4.2. Superfícies Premium (Marketing & Autenticação)
Para logins, wizards pesados e landing pages:
*   Uso de **Glassmorphism:** Fundos com `rgba(255, 255, 255, 0.8)` e `backdrop-filter: blur(20px)`.
*   Animações contínuas de fundo: SVGs flutuando de forma senoidal (Waves) ou "Orbs" div blurrados flutuantes e lentos (pulse/float infinites) posicionados atrês de um `z-index: 1`.

### 4.3. Modais
*   Sempre utilizam overlay de background escuro (`rgba(0,0,0,0.7)`) mesclado com `backdrop-filter: blur(4px)`.
*   Conteúdo branco total, raio `xl`. Quando abrindo, animam vindo de baixo ou se esvaindo (`slideDown` fade-in de leves `-10px` no translateY).

### 4.4. Botões e Ações Core (CTAs)
*   **Cor base de todo Botão Positivo:** `--color-tertiary-500` com hover para `--color-tertiary-600`.
*   Botões de navegação, "Voltar", "Outline" utilizam stroke (borda em cinza ou cor `gray-700`) e fundos transparentes, mas mantêm `radius-md`.
*   Botões têm hover responsivo com translações mínimas de `-1px` a `-2px` acompanhando aumento leve de box-shadow, indicando volume e solidez.
*   "Favoritar/Dar likes": Sempre trazem botões de forma quadrada ou pílula que dão um leve `scale(1.05)` no seu hover interno, utilizando ícone em cinza e acendendo para `--color-tertiary-500`.

### 4.5. Inputs e Formulários Avançados
*   Outline é sempre invisível.
*   Em `.input:focus`, deve haver uma alteração imediata na cor da borda para `--color-tertiary-400` ou `500`.
*   Uma "Aura" sutil de focus (box-shadow) da mesma base de cor `--color-tertiary` mas na opacidade de `0.1` a `0.3` (ex: `0 0 0 3px rgba(236,72,153, 0.1)`).

### 4.6. Tags, Badges, Chips e Skeletons
*   Filtros aplicados e tags simples: Estilos "Pílula" (Pill) com `radius-full`, fundos em `gray-100` e textos semibold pequenos (`font-size-xs`, `11px`).
*   Badges de Status ou Alerta (ex: "Sem agendamento" no Cart, "Erros"): Usam o background semântico (`--color-warning` (laranja) ou `--color-success` (verde)).
*   **Loaders e Skeletons**: Usar telas esqueléticas elaboradas em Grid replicando a tela real (`MarketplaceLoader`); abolir "Spinners Gigantes", devendo estes serem circunscritos somente em interações duráveis de processamento dentro dos botões (indicador ativo de `loading`).

### 4.7. Empty States (Tutorial Estilizado)
Os estados vazios na E-rádios nunca devem ser apenas avisos passivos. Eles devem atuar como um **"Tutorial Estilizado"** que demonstra o valor da funcionalidade antes mesmo dela ser configurada:
1.  **Interface Fantasma (Shadow UI):** Em vez de um container vazio, desenhe uma versão simplificada/skeleton do que seriam os dados reais (ex: uma tabela silhuetada ou cards com opacidade reduzida).
2.  **Preview Educativo:** Mostre exemplos de dados "fake" ou "mockups" que ilustram o resultado final esperado, ajudando o usuário a entender o "porquê" de realizar a ação.
3.  **Composição Dual:** Em áreas amplas, utilize um layout de duas colunas:
    *   **Lado A (Ação/Valor):** Ícone grande (`64px`+), título direto e convincente, texto de apoio minimalista e o botão de CTA principal em `--color-tertiary-500`.
    *   **Lado B (Visualização):** O mockup estilizado da funcionalidade em uso.
4.  **Zero Atrito:** O botão de ação deve levar diretamente ao fluxo de criação ou configuração, eliminando passos intermediários.

---

## Metarregras do Agente Geração de UI

1. **"Pink is the new blue":** Em dúvida ou precisando definir a cor de um botão, link, destaque ativo, gráfico principal ou indicador? Use a variável **`--color-tertiary-500` / `600`**. Não pinte a tela de azul, e jamais de uma variação escura pura. A identidade visual respira no "Rosa Digital Sobre Fundo Branco/Cinza-0".
2. **Dados Enriquecidos Visualmente:** Nunca exiba lista de números e chaves na tela seca. Apresente informações visuais atraentes: Para idades, simule uma barra em blocos. Para a composição sociodemográfica, pequenas seções de um Stacked Bar. Dê vida a tabelas áridas usando chips e pílulas em cores semânticas atenuadas (High, Low, Medium status usando as variáveis de info-warning-success light).
3. **Resumo Visual:** Crianças visuais e caixas bem separadas. O vazio e o afastamento (`var(--spacing-lg/xl)`) organizam a hierarquia e evitam "poluição" visual, vital para SaaS.
4. **Empty States Proativos (Tutorial Estilizado):** Componentes nunca ficam em estado de array vazio "seco" (apenas ícone e texto). Eles devem seguir obrigatoriamente o padrão da **seção 4.7**, apresentando uma prévia visual do que os dados serão após a ação, mantendo o usuário engajado e educado sobre a ferramenta.
5. **Proibição de Componentes Nativos Imperativos:** Nunca utilize `window.alert`, `window.confirm` ou `window.prompt`. A plataforma da Signalads usa uma experiência Web App fluida, de modo que confirmações, sucessos ou exclusões devem acontecer através de interações ou Modais desenhadas no ecossistema.

   **Implementação obrigatória — `DialogContext`:**
   - O sistema possui um `DialogProvider` global em `src/contexts/DialogContext.js` que já intercepta todos os `alert()` nativos e os exibe como modal automaticamente.
   - Para **confirmações** (`window.confirm`), use o hook `useDialog()`:
     ```js
     import { useDialog } from '../../contexts/DialogContext';
     // dentro do componente:
     const { showConfirm } = useDialog();
     // uso (a função que chama deve ser async):
     const ok = await showConfirm('Mensagem de confirmação?', 'Título opcional');
     if (!ok) return;
     ```
   - Para **alertas/notificações** simples, chame `alert('mensagem')` normalmente — o `DialogProvider` intercepta e exibe a modal automaticamente. Não é necessário nenhum import extra.
   - O overlay das modais usa `z-index: 9999` para sobrepor qualquer elemento da interface.

6. **Logos de Emissoras — `SmartImage` + `getAppSheetImageUrl`:** Toda imagem de logo de emissora deve ser renderizada com o componente `SmartImage` em conjunto com a função `getAppSheetImageUrl`. Nunca use `<img src={logo}>` direto.

   **Implementação obrigatória:**
   ```js
   import SmartImage from '../../components/SmartImage';
   import { getAppSheetImageUrl } from '../../utils/imageUtils';

   // Uso:
   <SmartImage
     src={getAppSheetImageUrl(broadcaster.logo)}
     alt={broadcaster.name}
     className={styles.seuEstilo}
   />
   ```
   - **`getAppSheetImageUrl(path)`**: Se `path` começa com `http`, retorna como está (URL GCS). Se for caminho parcial (ex: `Rádios 2_Images/...`), monta `${API_URL}/image/proxy?fileName=...`. Se for `undefined`/vazio, retorna `''`.
   - **`SmartImage`**: Exibe skeleton animado enquanto carrega, e ícone `MdRadio` como fallback automático em caso de erro ou src vazio. Aplica o `className` passado ao wrapper externo — garanta que o wrapper tenha `width`, `height` e `overflow: hidden` definidos no CSS.
   - **Campo correto no banco**: O logo da emissora é salvo em `broadcasterProfile.logo` (User model). **Nunca** use `broadcasterProfile.generalInfo.logoUrl` — esse campo não existe.
   - O padrão de referência é o Marketplace (`src/pages/Marketplace/index.js`), que já usa essa abordagem corretamente.

7. **Footer Padrão — `<Footer />` obrigatório em todas as páginas:** Toda tela do sistema, incluindo páginas públicas (ex: Termos de Uso, Política de Privacidade, Login, Cadastro), deve encerrar seu conteúdo com o componente `Footer` localizado em `src/components/Footer`.

   **Implementação obrigatória:**
   ```js
   import Footer from '../../components/Footer';

   // No JSX, sempre como último elemento antes de fechar o wrapper da página:
   <div className={styles.page}>
     {/* conteúdo da página */}
     <Footer />
   </div>
   ```
   - **Nunca** crie um rodapé manual com texto de copyright, links ou ano — use sempre o componente `Footer`.
   - O `Footer` já gerencia o ano dinâmico (`new Date().getFullYear()`), links legais (`/terms`, `/policy`), redes sociais, contatos e o modal de contato.
   - **Nunca** use `position: fixed` ou `overflow-x: hidden` em elementos pai das páginas que contenham componentes com `position: sticky` — isso quebra o comportamento de fixação.
   - Os links "Termos de Uso" e "Privacidade" dentro do `Footer` apontam respectivamente para `/terms` e `/policy`.
