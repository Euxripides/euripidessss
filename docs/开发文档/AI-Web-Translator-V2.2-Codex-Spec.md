# AI Web Translator V2.2 — Chrome 扩展完整开发规格书

> 用途：将本文件直接交给 Codex，作为项目生成、实现、调试、测试和验收依据。  
> 项目类型：Chrome Extension，Manifest V3。  
> 默认翻译方向：英文 → 简体中文。  
> 默认 AI Provider：DeepSeek。  
> 目标版本：V2.2。  
> 主要场景：AI 网站、区块链网站、开发文档、数据分析平台、SaaS 管理后台、GitHub、技术论坛和动态单页应用。

---

## 0. 给 Codex 的总指令

你需要从零创建一个完整、可运行、可构建、可测试、可打包的 Chrome 网页翻译扩展。

不要只生成示例代码、伪代码或静态页面。所有核心功能必须真实实现。

开发过程必须遵守以下规则：

1. 使用 TypeScript。
2. 使用 Manifest V3。
3. 使用 Vite 构建。
4. Popup 和 Options 使用 React。
5. Content Script 不引入 React，避免污染网页。
6. 所有翻译 API 请求都由 Background Service Worker 发起。
7. Content Script 永远不能持有 API Key。
8. 所有 AI 返回结果只允许作为纯文本写入 DOM。
9. 禁止使用 `innerHTML` 插入 AI 返回内容。
10. 禁止使用 `eval`、`new Function`、远程脚本和 `unsafe-eval`。
11. 每个阶段完成后必须执行：
    - TypeScript 类型检查；
    - 单元测试；
    - 构建；
    - 必要的 E2E 测试。
12. 遇到编译或测试错误时直接修复，不要只描述问题。
13. 不允许用核心功能 TODO 替代真实实现。
14. 最终必须生成：
    - `dist/`
    - 可安装 ZIP
    - `README.md`
    - `CHANGELOG.md`
    - `PRIVACY.md`
    - `LICENSE`
15. 最终 ZIP 解压后应可以直接通过 Chrome 的“加载已解压的扩展程序”安装。
16. 不保证任何网页固定 3 秒完成翻译，但普通页面应尽量在 1 秒左右开始出现译文，2～5 秒完成主要可见内容。
17. 对浏览器本身无法实现的限制必须明确说明，不得虚假宣称支持。

---

# 1. 产品目标

开发一个高质量、可长期维护的 AI 网页翻译 Chrome 扩展。

不能采用“一个文本节点一次 API 请求”的低效方案，而应使用完整的批量翻译流水线：

```text
页面加载
  ↓
语言检测
  ↓
DOM 扫描
  ↓
文本过滤
  ↓
语义分类
  ↓
文本归一化
  ↓
上下文提取
  ↓
去重
  ↓
缓存查询
  ↓
视口优先排序
  ↓
批量分组
  ↓
并发翻译
  ↓
结果校验
  ↓
精确回写
  ↓
动态内容持续监听
```

核心目标：

- 翻译质量明显优于逐节点机器翻译；
- 支持 DeepSeek；
- 支持 OpenAI Compatible API；
- 支持百度和有道作为备用；
- 支持动态页面；
- 支持 React、Vue、Next.js、Nuxt；
- 支持 Open Shadow DOM；
- 支持 iframe；
- 支持中英对照；
- 支持悬停查看原文；
- 支持术语库；
- 支持缓存；
- 支持网站规则；
- 支持翻译统计；
- 支持快捷键；
- 支持右键菜单；
- 支持暂停、继续、停止、恢复和重新翻译。

---

# 2. 非目标

V2.2 不要求完成以下能力：

- Firefox 正式适配；
- Safari 正式适配；
- 本地大模型推理；
- 云端账号系统；
- 多设备同步；
- 团队协作；
- 服务端代理；
- 云端术语库；
- 自动 OCR 整页图片；
- 翻译视频字幕；
- 翻译 Canvas 内文字；
- 翻译 closed Shadow DOM；
- 绕过浏览器跨域安全限制。

OCR 可以保留实验入口，但不作为 V2.2 核心验收项。

---

# 3. 技术栈

必须使用：

- TypeScript；
- Vite；
- Chrome Extension Manifest V3；
- React；
- React Router；
- Zustand；
- 原生 CSS 或 Tailwind CSS；
- IndexedDB；
- Chrome Storage API；
- Chrome Commands API；
- Chrome Context Menus API；
- Chrome Scripting API；
- MutationObserver；
- AbortController；
- Web Worker；
- Vitest；
- Playwright；
- ESLint；
- Prettier。

推荐：

- `zod`：运行时配置和消息校验；
- `idb`：封装 IndexedDB；
- `nanoid`：生成请求 ID；
- `clsx`：UI class 组合。

Content Script 中禁止加载 React 和大型 UI 运行时。

---

# 4. 浏览器兼容性

最低支持：

- Chrome 120+；
- Edge 120+；
- 其他 Chromium 120+ 浏览器。

开发时尽量减少 Chrome 私有 API 的扩散，为未来 Firefox 适配保留接口层。

---

# 5. 推荐项目结构

```text
ai-web-translator/
├─ public/
│  ├─ icons/
│  └─ assets/
├─ src/
│  ├─ background/
│  │  ├─ index.ts
│  │  ├─ message-router.ts
│  │  ├─ translation-manager.ts
│  │  ├─ provider-manager.ts
│  │  ├─ request-queue.ts
│  │  ├─ retry-policy.ts
│  │  ├─ rate-limiter.ts
│  │  ├─ usage-tracker.ts
│  │  ├─ storage-manager.ts
│  │  ├─ frame-registry.ts
│  │  └─ context-menu.ts
│  ├─ content/
│  │  ├─ index.ts
│  │  ├─ page-controller.ts
│  │  ├─ dom-scanner.ts
│  │  ├─ text-filter.ts
│  │  ├─ text-normalizer.ts
│  │  ├─ semantic-classifier.ts
│  │  ├─ context-builder.ts
│  │  ├─ deduplicator.ts
│  │  ├─ batch-builder.ts
│  │  ├─ viewport-priority.ts
│  │  ├─ translation-renderer.ts
│  │  ├─ bilingual-renderer.ts
│  │  ├─ attribute-translator.ts
│  │  ├─ restore-manager.ts
│  │  ├─ mutation-controller.ts
│  │  ├─ spa-controller.ts
│  │  ├─ shadow-dom-controller.ts
│  │  ├─ iframe-controller.ts
│  │  ├─ hover-original.ts
│  │  ├─ selection-translator.ts
│  │  ├─ page-language-detector.ts
│  │  ├─ floating-prompt.ts
│  │  ├─ floating-toolbar.ts
│  │  └─ progress-indicator.ts
│  ├─ providers/
│  │  ├─ provider-types.ts
│  │  ├─ base-provider.ts
│  │  ├─ deepseek-provider.ts
│  │  ├─ openai-compatible-provider.ts
│  │  ├─ baidu-provider.ts
│  │  ├─ youdao-provider.ts
│  │  ├─ provider-registry.ts
│  │  ├─ provider-validator.ts
│  │  └─ provider-error-mapper.ts
│  ├─ cache/
│  │  ├─ memory-cache.ts
│  │  ├─ indexeddb-cache.ts
│  │  ├─ cache-key.ts
│  │  ├─ cache-cleaner.ts
│  │  └─ cache-stats.ts
│  ├─ terminology/
│  │  ├─ glossary-manager.ts
│  │  ├─ builtin-glossary.ts
│  │  ├─ glossary-matcher.ts
│  │  ├─ glossary-import.ts
│  │  └─ glossary-export.ts
│  ├─ prompts/
│  │  ├─ prompt-builder.ts
│  │  ├─ base-system-prompt.ts
│  │  ├─ domain-prompts.ts
│  │  ├─ ui-prompt.ts
│  │  └─ document-prompt.ts
│  ├─ popup/
│  │  ├─ index.html
│  │  ├─ main.tsx
│  │  ├─ App.tsx
│  │  ├─ components/
│  │  ├─ stores/
│  │  └─ styles/
│  ├─ options/
│  │  ├─ index.html
│  │  ├─ main.tsx
│  │  ├─ App.tsx
│  │  ├─ pages/
│  │  ├─ components/
│  │  ├─ stores/
│  │  └─ styles/
│  ├─ shared/
│  │  ├─ types.ts
│  │  ├─ constants.ts
│  │  ├─ messages.ts
│  │  ├─ errors.ts
│  │  ├─ logger.ts
│  │  ├─ crypto.ts
│  │  ├─ language.ts
│  │  ├─ url-pattern.ts
│  │  ├─ placeholders.ts
│  │  ├─ sensitive-text.ts
│  │  ├─ debounce.ts
│  │  └─ throttle.ts
│  └─ workers/
│     ├─ text-processing.worker.ts
│     └─ cache-maintenance.worker.ts
├─ tests/
│  ├─ unit/
│  ├─ integration/
│  ├─ e2e/
│  └─ fixtures/
├─ manifest.config.ts
├─ vite.config.ts
├─ tsconfig.json
├─ package.json
├─ eslint.config.js
├─ playwright.config.ts
├─ README.md
├─ CHANGELOG.md
├─ PRIVACY.md
└─ LICENSE
```

---

# 6. Manifest V3

Manifest 至少应包含：

```json
{
  "manifest_version": 3,
  "name": "AI Web Translator",
  "version": "2.2.0",
  "description": "AI-powered high-quality webpage translator.",
  "permissions": [
    "storage",
    "activeTab",
    "scripting",
    "contextMenus",
    "commands"
  ],
  "host_permissions": [
    "<all_urls>"
  ],
  "background": {
    "service_worker": "background/index.js",
    "type": "module"
  },
  "action": {
    "default_popup": "popup/index.html"
  },
  "options_page": "options/index.html",
  "content_scripts": [
    {
      "matches": ["<all_urls>"],
      "js": ["content/index.js"],
      "run_at": "document_start",
      "all_frames": true,
      "match_about_blank": true
    }
  ],
  "commands": {
    "translate-page": {
      "suggested_key": {
        "default": "Alt+T"
      },
      "description": "翻译当前页面"
    },
    "restore-page": {
      "suggested_key": {
        "default": "Alt+R"
      },
      "description": "恢复当前页面"
    },
    "retranslate-page": {
      "suggested_key": {
        "default": "Alt+Shift+T"
      },
      "description": "重新翻译当前页面"
    }
  }
}
```

要求：

- 最终路径必须与 Vite 构建产物一致；
- 不允许远程脚本；
- 不允许 `unsafe-eval`；
- Background 为 ES Module；
- Content Script 在 `document_start` 运行；
- 每个 frame 独立注入；
- 对无法注入的页面安静跳过。

---

# 7. 核心类型定义

```ts
export type TranslationMode =
  | "translated-only"
  | "bilingual"
  | "hover-original";

export type PageTranslationStatus =
  | "idle"
  | "detecting"
  | "scanning"
  | "translating"
  | "paused"
  | "completed"
  | "failed"
  | "stopped"
  | "restored";

export type ProviderType =
  | "deepseek"
  | "openai-compatible"
  | "baidu"
  | "youdao";

export type DomainMode =
  | "general"
  | "technology"
  | "ai"
  | "blockchain"
  | "finance"
  | "data-analysis"
  | "legal"
  | "medical";

export type SemanticTextType =
  | "heading"
  | "paragraph"
  | "button"
  | "menu-item"
  | "navigation"
  | "label"
  | "placeholder"
  | "tooltip"
  | "table-cell"
  | "dialog"
  | "toast"
  | "code-comment"
  | "other";

export interface TextNodeRecord {
  id: string;
  node: Text | null;
  element: HTMLElement;
  sourceKind: "text-node" | "attribute";
  attributeName?: "placeholder" | "title" | "aria-label" | "alt";
  originalText: string;
  normalizedText: string;
  leadingWhitespace: string;
  trailingWhitespace: string;
  contextText?: string;
  semanticType: SemanticTextType;
  priority: number;
  duplicateGroupId?: string;
  translatedText?: string;
  status:
    | "pending"
    | "cached"
    | "queued"
    | "translating"
    | "translated"
    | "failed"
    | "ignored";
}

export interface TranslationItem {
  id: string;
  text: string;
  context?: string;
  semanticType: SemanticTextType;
  placeholders?: string[];
}

export interface TranslationBatch {
  batchId: string;
  requestId: string;
  tabId?: number;
  frameId?: number;
  sourceLanguage: string;
  targetLanguage: string;
  domainMode: DomainMode;
  items: TranslationItem[];
  estimatedInputTokens: number;
  createdAt: number;
}

export interface TranslationResultItem {
  id: string;
  translatedText: string;
}

export interface TranslationBatchResult {
  batchId: string;
  requestId: string;
  providerId: string;
  model?: string;
  items: TranslationResultItem[];
  elapsedMs: number;
  usage?: {
    inputTokens?: number;
    outputTokens?: number;
    estimatedCostCny?: number;
  };
}

export interface PageTranslationProgress {
  status: PageTranslationStatus;
  totalItems: number;
  completedItems: number;
  cachedItems: number;
  failedItems: number;
  totalBatches: number;
  completedBatches: number;
  elapsedMs: number;
  currentProviderId?: string;
}
```

---

# 8. 设置数据结构

```ts
export interface ExtensionSettings {
  enabled: boolean;

  sourceLanguage: "auto" | string;
  targetLanguage: string;

  defaultProviderId: string;
  fallbackProviderIds: string[];

  translationMode: TranslationMode;
  domainMode: DomainMode;

  autoDetectEnglishPage: boolean;
  autoTranslateEnabled: boolean;
  promptBeforeTranslate: boolean;
  dynamicTranslationEnabled: boolean;
  translateShadowDom: boolean;
  translateIframes: boolean;

  viewportFirst: boolean;
  concurrency: number;
  batchMaxItems: number;
  batchMaxCharacters: number;
  batchMaxEstimatedTokens: number;
  requestTimeoutMs: number;
  maxRetries: number;
  mutationDebounceMs: number;

  cacheEnabled: boolean;
  cacheMaxEntries: number;
  cacheExpireDays: number;

  glossaryEnabled: boolean;
  builtinGlossaryEnabled: boolean;

  hoverShowOriginal: boolean;
  showProgress: boolean;
  showFloatingToolbar: boolean;

  ignoredSelectors: string[];
  ignoredTags: string[];
  siteRules: SiteRule[];

  providers: ProviderConfig[];

  debugLogging: boolean;
}
```

默认参数：

```json
{
  "enabled": true,
  "sourceLanguage": "auto",
  "targetLanguage": "zh-CN",
  "translationMode": "translated-only",
  "domainMode": "general",
  "autoDetectEnglishPage": true,
  "autoTranslateEnabled": false,
  "promptBeforeTranslate": true,
  "dynamicTranslationEnabled": true,
  "translateShadowDom": true,
  "translateIframes": true,
  "viewportFirst": true,
  "concurrency": 3,
  "batchMaxItems": 60,
  "batchMaxCharacters": 6000,
  "batchMaxEstimatedTokens": 4000,
  "requestTimeoutMs": 20000,
  "maxRetries": 2,
  "mutationDebounceMs": 400,
  "cacheEnabled": true,
  "cacheMaxEntries": 100000,
  "cacheExpireDays": 90,
  "glossaryEnabled": true,
  "builtinGlossaryEnabled": true,
  "hoverShowOriginal": false,
  "showProgress": true,
  "showFloatingToolbar": true,
  "debugLogging": false
}
```

---

# 9. Provider 配置

```ts
export interface ProviderConfig {
  id: string;
  name: string;
  type: ProviderType;
  enabled: boolean;

  baseUrl?: string;
  endpoint?: string;
  apiKey?: string;
  model?: string;

  appId?: string;
  secretKey?: string;

  temperature?: number;
  maxTokens?: number;
  jsonMode?: boolean;
  stream?: boolean;

  timeoutMs?: number;
  maxConcurrency?: number;
  priority: number;

  extraHeaders?: Record<string, string>;
  extraBody?: Record<string, unknown>;
}
```

API Key 保存在 `chrome.storage.local`。

不要把 API Key 输出到：

- Console；
- 错误日志；
- DOM；
- Content Script；
- 页面脚本；
- 使用统计；
- 导出诊断日志。

---

# 10. Provider 统一接口

```ts
export interface ProviderValidationResult {
  valid: boolean;
  latencyMs?: number;
  model?: string;
  message?: string;
}

export interface TranslationProvider {
  readonly id: string;
  readonly name: string;
  readonly type: ProviderType;

  validateConfig(
    config: ProviderConfig,
    signal?: AbortSignal
  ): Promise<ProviderValidationResult>;

  translateBatch(
    batch: TranslationBatch,
    config: ProviderConfig,
    signal?: AbortSignal
  ): Promise<TranslationBatchResult>;

  estimateCostCny?(
    inputTokens: number,
    outputTokens: number,
    config: ProviderConfig
  ): number;
}
```

Provider 只负责：

- 构建请求；
- 发起请求；
- 解析响应；
- 标准化错误；
- 返回 Token 和耗时。

Provider 不负责：

- DOM；
- 缓存；
- 批次构建；
- 重试；
- Provider 切换；
- 页面状态。

---

# 11. DeepSeek Provider

默认配置：

```json
{
  "id": "deepseek-default",
  "name": "DeepSeek",
  "type": "deepseek",
  "enabled": true,
  "baseUrl": "https://api.deepseek.com",
  "endpoint": "/chat/completions",
  "model": "deepseek-chat",
  "temperature": 0.2,
  "jsonMode": true,
  "stream": false,
  "timeoutMs": 20000,
  "maxConcurrency": 3,
  "priority": 100
}
```

请求示例：

```json
{
  "model": "deepseek-chat",
  "temperature": 0.2,
  "stream": false,
  "messages": [
    {
      "role": "system",
      "content": "系统提示词"
    },
    {
      "role": "user",
      "content": "批量翻译 JSON"
    }
  ],
  "response_format": {
    "type": "json_object"
  }
}
```

实现要求：

- Base URL 可修改；
- Endpoint 可修改；
- Model 可修改；
- JSON Mode 可关闭；
- 支持 AbortSignal；
- 支持请求超时；
- 支持用量解析；
- 支持移除 Markdown JSON 代码块；
- 支持服务端返回非标准 JSON 的容错；
- 不允许静默接受缺失条目。

响应校验：

- `batchId` 一致；
- `items` 为数组；
- 每个 id 唯一；
- 所有原始 id 都存在；
- 不允许未知 id；
- `translatedText` 必须是字符串；
- 不允许 HTML；
- 占位符必须保持一致；
- 不完整时只重试失败条目。

---

# 12. OpenAI Compatible Provider

必须允许用户配置：

- Base URL；
- Endpoint；
- API Key；
- Model；
- Temperature；
- Max Tokens；
- JSON Mode；
- Stream；
- 自定义 Header；
- 自定义 Body。

目标兼容：

- OpenAI；
- DeepSeek；
- 硅基流动；
- OpenRouter；
- 阿里百炼兼容接口；
- 火山方舟兼容接口；
- 本地 Ollama OpenAI Compatible；
- 自建中转接口。

不同厂商不得散落写死在业务代码中。

---

# 13. 百度翻译 Provider

支持：

- APP ID；
- Secret Key；
- 源语言；
- 目标语言；
- 签名；
- 错误码映射；
- 请求限速；
- Fallback。

必须映射常见错误：

```text
52003 → 未授权用户，请检查 APP ID、密钥和服务是否开通
54003 → 请求频率过高
54004 → 账户余额不足
58000 → 客户端 IP 不合法
58001 → 不支持的语言方向
```

不要只显示原始错误码。

---

# 14. 有道翻译 Provider

支持：

- 应用 ID；
- 应用密钥；
- 正确签名；
- 长文本摘要签名；
- errorCode 映射；
- 请求限速；
- Fallback。

错误必须转换成可理解中文提示。

---

# 15. Provider Manager

职责：

- 选择默认 Provider；
- 按优先级组织备用 Provider；
- 检测 Provider 健康；
- 自动重试；
- 自动切换；
- 熔断；
- 恢复；
- 统计成功率；
- 统计平均延迟。

```ts
export interface ProviderHealth {
  providerId: string;
  successCount: number;
  failureCount: number;
  consecutiveFailures: number;
  averageLatencyMs: number;
  lastSuccessAt?: number;
  lastFailureAt?: number;
  circuitState: "closed" | "open" | "half-open";
  circuitOpenUntil?: number;
}
```

建议熔断：

- 连续失败 3 次；
- 打开 60 秒；
- 进入 half-open；
- 发送一条小测试；
- 成功恢复；
- 失败继续熔断。

身份验证失败和余额不足不应自动切回同一 Provider 重试。

---

# 16. DOM 扫描

使用 `TreeWalker`。

扫描：

- Text Node；
- 按钮；
- 链接；
- 标题；
- 段落；
- 表格；
- Label；
- Placeholder；
- Title；
- aria-label；
- Alt；
- Option；
- Tooltip；
- Menu；
- Dialog；
- Drawer；
- Modal；
- Toast；
- 动态插入内容；
- Open Shadow DOM。

默认忽略标签：

```ts
const DEFAULT_IGNORED_TAGS = [
  "SCRIPT",
  "STYLE",
  "NOSCRIPT",
  "CODE",
  "PRE",
  "TEXTAREA",
  "SVG",
  "CANVAS",
  "VIDEO",
  "AUDIO"
];
```

说明：

- `INPUT` 不翻译 value；
- Placeholder 单独翻译；
- `SELECT/OPTION` 使用属性和文本专用逻辑；
- `CODE/PRE` 默认忽略；
- 代码注释为可选实验功能；
- iframe 由每个 frame 的 Content Script 独立处理。

---

# 17. 文本过滤

必须忽略：

- 空字符串；
- 纯数字；
- 纯标点；
- URL；
- Email；
- 文件路径；
- 哈希；
- 钱包地址；
- API Key；
- JWT；
- Base64 长字符串；
- 私钥；
- 助记词；
- 代码片段；
- 长 SQL；
- 长 JSON；
- 用户正在输入的内容；
- 密码输入框；
- 隐藏节点；
- `aria-hidden="true"`；
- `display:none`；
- `visibility:hidden`；
- 已经是目标语言的内容；
- 扩展自己插入的节点。

示例规则：

```regex
^0x[a-fA-F0-9]{40}$
```

```regex
^[A-Za-z0-9-_]+\.[A-Za-z0-9-_]+\.[A-Za-z0-9-_]+$
```

```regex
^[a-fA-F0-9]{32,128}$
```

敏感内容检测不应只靠正则，还应结合：

- 输入框类型；
- 元素名称；
- aria-label；
- autocomplete；
- placeholder；
- 周边文本。

以下字段默认不翻译：

```text
password
secret
private key
seed phrase
mnemonic
api key
access token
authorization
bearer
```

---

# 18. 页面语言检测

检测来源：

1. `<html lang>`；
2. meta language；
3. 页面标题；
4. 首屏可见文本；
5. 英文字母比例；
6. 中文字符比例；
7. 有效单词数；
8. 节点数量。

建议判断：

- 英文字符比例 > 55%；
- 有效英文词数 > 20；
- 中文比例 < 20%；
- 则认为主要为英文页面。

文本过少时不要提示。

刷新页面后必须重新检测。

不要用 `sessionStorage` 阻止刷新后再次提示。

当前页面加载内可用内存 Set 防止重复提示。

---

# 19. 自动翻译提示框

检测到英文页面时，用 Shadow DOM 显示：

```text
检测到英文网页，是否翻译为中文？

[翻译] [暂不] [此网站不再询问]
```

要求：

- 与网页样式隔离；
- 支持浅色和深色；
- 支持拖动；
- 可关闭；
- 不遮挡主要内容；
- 10 秒后自动缩小，不自动确认；
- “此网站不再询问”写入站点规则；
- 页面刷新后重新询问，除非规则明确禁止。

---

# 20. 语义分类

规则示例：

```text
h1-h6                    → heading
p/article/section        → paragraph
button/[role=button]     → button
nav/a                    → navigation
label                    → label
th/td                    → table-cell
[role=menuitem]          → menu-item
[role=dialog] 内文本      → dialog
[role=alert]             → toast
placeholder              → placeholder
title/aria-label         → tooltip 或 other
```

用途：

- 选择 Prompt；
- 控制译文长度；
- 区分缓存；
- UI 文案短译；
- 分批；
- 上下文生成。

---

# 21. 上下文生成

每条文本可携带有限上下文：

- 页面标题；
- 元素标签；
- 最近标题；
- 父级区域；
- 相邻兄弟文本；
- 当前菜单；
- 当前 Dialog 标题；
- 表格表头；
- aria-label；
- semanticType。

单条上下文不超过 150 字符。

禁止上传完整 HTML。

示例：

```json
{
  "id": "node_103",
  "text": "Credits",
  "context": "Account settings menu; nearby items: Billing, Usage, Invoices",
  "semanticType": "menu-item"
}
```

---

# 22. 文本归一化

要求：

- 保留原文；
- 去除重复空白；
- 合并多余换行；
- 记录前导空格；
- 记录尾随空格；
- 保留标点；
- 保留数字；
- 保留单位；
- 保留货币；
- 保留模板变量；
- 保留占位符；
- 保留 Markdown；
- 保留内联代码。

识别占位符：

```text
{{name}}
${amount}
%USER%
{0}
{count}
:username
<name>
```

翻译后校验：

- 所有占位符仍存在；
- 数量一致；
- 内容一致；
- 顺序允许变化，但默认应尽量保持；
- 失败则该条重试。

---

# 23. 去重规则

同一页面出现多次的相同短文本应去重。

缓存和去重 Key 至少包含：

```text
normalizedText
semanticType
sourceLanguage
targetLanguage
domainMode
glossaryVersion
promptVersion
```

不要只按纯文本去重。

例如：

```text
Gas
```

在区块链和能源领域含义不同。

重复项保留所有回写目标，一个翻译结果映射多个节点。

---

# 24. 视口优先

优先级：

1. 当前视口；
2. 距离视口 1 屏以内；
3. 标题、导航和按钮；
4. 主体正文；
5. 页面底部；
6. 折叠区域；
7. 完全不可见区域。

使用：

- `getBoundingClientRect()`；
- `IntersectionObserver`；
- 页面主内容估算；
- 元素语义权重。

首屏翻译完成后继续翻译剩余页面。

---

# 25. Batch 构建

默认：

- 最大 60 条；
- 最大 6000 字符；
- 最大估算 4000 输入 Token；
- UI 短文本与长段落分开；
- 不同语义类型尽量分组；
- 缓存命中项不进入请求；
- 超长段落可按句子切分，但必须可恢复映射。

禁止：

- 一个页面几百个并发请求；
- 一个节点一个请求；
- 把整页 HTML 直接发送；
- 单个 Batch 超过模型上下文。

---

# 26. 并发队列

默认并发：

```text
DeepSeek: 3
OpenAI Compatible: 3
百度: 5
有道: 5
```

用户可设置 1～8。

队列必须支持：

- 暂停；
- 继续；
- 停止；
- 取消；
- 超时；
- 单批次重试；
- Provider 切换；
- 进度事件；
- AbortController；
- 请求去重；
- 相同 Batch Promise 复用。

---

# 27. 重试策略

建议：

```text
网络错误      → 最多重试 2 次
HTTP 429      → 指数退避
HTTP 5xx      → 指数退避
JSON 解析失败 → 缩小 Batch 后重试
部分条目缺失  → 只重试缺失条目
认证失败      → 不重试
余额不足      → 不重试
配置错误      → 不重试
内容拒绝      → 标记失败并跳过
```

退避：

```text
第 1 次：800ms
第 2 次：2000ms
第 3 次：5000ms
```

增加 0～300ms 随机抖动。

---

# 28. AI Prompt

系统 Prompt：

```text
你是一名专业网页本地化翻译引擎。

任务：
将输入中的英文网页文本翻译为简体中文。

要求：
1. 只翻译，不解释。
2. 保持 JSON 结构和所有 id 完全不变。
3. 不增加、不删除、不合并条目。
4. 保持占位符、变量、数字、URL、代码、邮箱和专有标识不变。
5. 根据 semanticType 和 context 判断语境。
6. UI 按钮、菜单和标签应简洁、自然，符合中文软件界面习惯。
7. 技术文档应准确，不进行文学化改写。
8. 专业术语必须遵循 glossary。
9. 无法翻译的品牌、模型名、产品名和专有名词保留原文。
10. 不输出 Markdown，不输出代码块，只输出合法 JSON。
11. translatedText 不得包含 HTML。
12. 不得修改品牌名、模型名、产品名、API 名称。
13. 不翻译钱包地址、哈希、Token、私钥、助记词和代码。
14. UI 文案尽量短，避免破坏页面布局。
```

用户消息：

```json
{
  "task": "translate_webpage",
  "batchId": "batch_001",
  "sourceLanguage": "en",
  "targetLanguage": "zh-CN",
  "domainMode": "blockchain",
  "glossary": {
    "wallet": "钱包",
    "gas fee": "Gas 费",
    "smart contract": "智能合约"
  },
  "items": [
    {
      "id": "node_1",
      "text": "Personal settings",
      "context": "Account menu",
      "semanticType": "menu-item",
      "placeholders": []
    }
  ]
}
```

要求返回：

```json
{
  "batchId": "batch_001",
  "items": [
    {
      "id": "node_1",
      "translatedText": "个人设置"
    }
  ]
}
```

---

# 29. 领域模式

内置模式：

- 通用；
- 技术；
- AI；
- 区块链；
- 金融；
- 数据分析；
- 法律；
- 医学。

## AI 术语示例

```text
Prompt → 提示词
System Prompt → 系统提示词
Inference → 推理
Reasoning → 推理
Context Window → 上下文窗口
Embedding → 嵌入
Fine-tuning → 微调
Agent → 智能体
Workflow → 工作流
Token → Token
Temperature → Temperature
Top P → Top P
Model → 模型
```

## 区块链术语示例

```text
Wallet → 钱包
Address → 地址
Contract → 合约
Smart Contract → 智能合约
Gas → Gas
Gas Fee → Gas 费
Nonce → Nonce
Token → Token
Swap → 兑换
Bridge → 跨链桥
Staking → 质押
Liquidity → 流动性
Liquidity Pool → 流动性池
Mint → 铸造
Burn → 销毁
Transaction → 交易
Block → 区块
Hash → 哈希
RPC → RPC
Node → 节点
Mainnet → 主网
Testnet → 测试网
```

## 数据分析术语示例

```text
Dataset → 数据集
Query → 查询
Schema → 模式
Dashboard → 仪表板
Metric → 指标
Dimension → 维度
Pipeline → 数据管道
ETL → ETL
Data Warehouse → 数据仓库
Data Lake → 数据湖
```

---

# 30. 术语库

```ts
export interface GlossaryEntry {
  id: string;
  source: string;
  target: string;
  enabled: boolean;
  caseSensitive: boolean;
  exactMatch: boolean;
  domainModes: DomainMode[];
  action: "translate-as" | "keep-original";
  priority: number;
  createdAt: number;
  updatedAt: number;
}
```

功能：

- 内置术语；
- 用户术语；
- 搜索；
- 新增；
- 编辑；
- 删除；
- 启用；
- 禁用；
- 分组；
- 优先级；
- 大小写敏感；
- 精确匹配；
- 单词边界；
- 保留原文；
- 冲突检测；
- JSON 导入导出；
- CSV 导入导出；
- TSV 导入。

术语变动后必须更新 `glossaryVersion`，避免旧缓存污染。

---

# 31. 缓存

两级缓存。

## 内存缓存

- 当前页面使用；
- Map；
- 页面关闭后释放；
- 保存正在进行的 Promise；
- 避免同一句并发重复请求。

## IndexedDB

```ts
export interface TranslationCacheEntry {
  key: string;
  sourceText: string;
  normalizedText: string;
  translatedText: string;
  sourceLanguage: string;
  targetLanguage: string;
  domainMode: DomainMode;
  semanticType: SemanticTextType;
  providerId?: string;
  model?: string;
  glossaryVersion: string;
  promptVersion: string;
  createdAt: number;
  updatedAt: number;
  lastAccessAt: number;
  hitCount: number;
  expiresAt?: number;
}
```

Key：

```text
sha256(
  normalizedText +
  sourceLanguage +
  targetLanguage +
  domainMode +
  semanticType +
  glossaryVersion +
  promptVersion
)
```

清理：

- 默认最多 100000 条；
- LRU；
- 默认 90 天；
- 批量删除；
- 按站点清理；
- 按时间清理；
- 查看占用空间；
- 查看命中率。

---

# 32. 翻译回写

普通模式：

```ts
textNode.nodeValue =
  leadingWhitespace + translatedText + trailingWhitespace;
```

属性翻译：

- placeholder；
- title；
- aria-label；
- alt。

要求：

- 记录原值；
- 使用纯文本；
- 不写入 HTML；
- 不修改无关属性；
- 不破坏事件；
- 不替换整个元素；
- 不使用 `innerHTML`。

使用 WeakMap：

```ts
const originalTextMap = new WeakMap<Node, string>();
const originalAttributeMap =
  new WeakMap<Element, Map<string, string>>();
```

---

# 33. 恢复原文

恢复必须：

- 停止队列；
- Abort 未完成请求；
- 暂停 MutationObserver；
- 恢复 Text Node；
- 恢复 placeholder；
- 恢复 title；
- 恢复 aria-label；
- 恢复 alt；
- 删除双语节点；
- 删除 Hover 绑定；
- 清理浮动 UI；
- 清理页面状态；
- 释放引用；
- 设置状态为 restored。

恢复后不得继续异步回写旧请求结果。

每次翻译 Session 必须有 `sessionId`，回写前校验当前 session。

---

# 34. 双语模式

块级内容：

```text
Original paragraph

中文译文
```

规则：

- 标题和正文可双语；
- 按钮和菜单默认只替换；
- 内联元素谨慎处理；
- 插入节点使用扩展专属 class；
- 可折叠；
- 样式低干扰；
- 不改变原文；
- 恢复时全部删除；
- 不影响网页原始复制功能。

---

# 35. 悬停原文

功能：

- 页面显示译文；
- 鼠标停留 300ms；
- 显示原文 Tooltip；
- Shadow DOM；
- 自动定位；
- 支持复制；
- Escape 关闭；
- 页面滚动时重定位；
- 不遮挡鼠标；
- 不捕获网页点击。

---

# 36. 动态内容

使用 MutationObserver。

要求：

- 监听 `childList`；
- 监听 `subtree`；
- 必要时监听 `characterData`；
- 400ms 去抖；
- 合并新增节点；
- 忽略扩展节点；
- 忽略已处理节点；
- 优先缓存；
- 小批次翻译；
- 防止递归；
- 不允许每个 Mutation 独立请求；
- 页面重渲染后自动重新应用缓存译文。

支持：

- React；
- Vue；
- Next.js；
- Nuxt；
- Ant Design；
- Element Plus；
- Material UI；
- Dropdown；
- Tooltip；
- Drawer；
- Modal；
- Toast；
- 无限滚动。

---

# 37. SPA 路由

监听：

- `history.pushState`；
- `history.replaceState`；
- `popstate`；
- URL 变化；
- 主内容变化；
- 页面标题变化。

要求：

- 不重载扩展；
- 重新检测主要内容；
- 保留翻译状态；
- 根据站点规则决定自动翻译；
- 更新页面标题上下文；
- 缓存内容直接回写；
- 防止重复扫描风暴。

---

# 38. Shadow DOM

支持 Open Shadow DOM。

实现：

- 初始递归扫描；
- 记录已观察 root；
- 为每个 root 注册 MutationObserver；
- 新元素创建 shadowRoot 时发现并注册；
- 防重复；
- 清理时断开所有观察器。

明确限制：

- closed Shadow DOM 无法可靠访问；
- README 必须说明；
- UI 不得宣称支持全部 Shadow DOM。

---

# 39. iframe

要求：

- `all_frames: true`；
- 每个 frame 独立扫描和翻译；
- Background 聚合进度；
- 同源和允许注入的跨域 frame 尽量支持；
- 无权限 frame 跳过；
- 单个 frame 失败不阻塞整页；
- 主 frame 显示“部分嵌入内容无法翻译”。

---

# 40. 选中文本翻译

支持：

- 右键菜单；
- 浮动小窗；
- 显示原文；
- 显示译文；
- 复制译文；
- 切换领域；
- 切换 Provider；
- 不修改页面；
- 超长选区自动分批。

右键菜单：

```text
翻译选中文本
翻译当前页面
暂停翻译
恢复当前页面
重新翻译当前页面
```

---

# 41. Popup

建议宽度：380px。

区域：

## 页面状态

- 页面语言；
- 状态；
- 进度；
- Provider；
- Model；
- 耗时；
- 缓存命中；
- 错误数。

## 操作

- 翻译；
- 暂停；
- 继续；
- 停止；
- 恢复原文；
- 重新翻译；
- 打开设置。

## 模式

- 仅译文；
- 中英对照；
- 悬停原文。

## 领域

- 通用；
- 技术；
- AI；
- 区块链；
- 金融；
- 数据分析；
- 法律；
- 医学。

## Provider

- 当前 Provider；
- 当前模型；
- 健康状态；
- 平均延迟；
- 快速切换。

---

# 42. Options 设置页

侧边栏：

```text
常规
翻译引擎
性能
术语库
网站规则
缓存
快捷键
统计
日志
关于
```

## 常规

- 目标语言；
- 自动检测；
- 自动翻译；
- 翻译前询问；
- 动态翻译；
- Shadow DOM；
- iframe；
- 首屏优先；
- 默认模式；
- Hover 原文。

## 翻译引擎

每个 Provider 卡片：

- 名称；
- 类型；
- 启用；
- Base URL；
- Endpoint；
- API Key；
- Model；
- Temperature；
- Max Tokens；
- Timeout；
- 最大并发；
- 优先级；
- JSON Mode；
- 测试连接；
- 删除；
- 复制。

测试连接：

- 使用最小文本；
- 不进入缓存；
- 不计入统计；
- 显示延迟；
- 显示模型；
- 显示错误；
- 支持取消。

## 性能

- Batch 条目；
- Batch 字符；
- Token 上限；
- 并发；
- 超时；
- 重试；
- Mutation 去抖；
- 首屏优先；
- 低性能模式；
- 调试日志。

## 术语库

- 搜索；
- 筛选；
- 新增；
- 编辑；
- 删除；
- 导入；
- 导出；
- 冲突检测；
- 领域分组。

## 网站规则

```ts
export type SiteRuleAction =
  | "always-translate"
  | "ask"
  | "never-translate"
  | "bilingual"
  | "translated-only";
```

匹配：

```text
example.com
*.example.com
docs.*
https://example.com/path/*
```

优先级：

1. 完整 URL；
2. 路径；
3. 子域；
4. 根域；
5. 默认。

## 缓存

- 开关；
- 条目数；
- 占用空间；
- 命中率；
- 清空；
- 按时间清理；
- 导出；
- 重建索引。

## 快捷键

显示：

```text
Alt+T
Alt+R
Alt+Shift+T
```

提供跳转提示：

```text
chrome://extensions/shortcuts
```

## 统计

- 今日请求；
- 今日字符；
- 今日 Token；
- 今日预计费用；
- 缓存命中；
- 平均耗时；
- Provider 成功率；
- 最近错误；
- 7 天趋势；
- 30 天趋势。

## 关于

- 版本；
- Changelog；
- 隐私；
- 存储说明；
- 导出诊断日志；
- 恢复默认。

---

# 43. 浮动进度条

示例：

```text
正在翻译 128 / 420
DeepSeek · 缓存 86 · 2.4 秒

[暂停] [停止]
```

要求：

- Shadow DOM；
- 小型；
- 可拖动；
- 可折叠；
- 完成后自动隐藏；
- 显示错误数；
- 不遮挡主要按钮；
- 深浅色适配。

---

# 44. 使用统计

```ts
export interface UsageRecord {
  id: string;
  timestamp: number;
  providerId: string;
  model?: string;
  hostname?: string;
  inputCharacters: number;
  outputCharacters: number;
  inputTokens?: number;
  outputTokens?: number;
  estimatedCostCny?: number;
  latencyMs: number;
  success: boolean;
  cacheHit: boolean;
  errorCode?: string;
}
```

统计：

- 今日；
- 7 天；
- 30 天；
- Provider；
- 模型；
- 域名；
- 成功率；
- 缓存命中；
- 延迟。

费用只能标记为“预计”。

Provider 没有返回 Token 时允许按字符估算，但必须明显标注估算值。

---

# 45. 错误类型

```ts
export type TranslationErrorCode =
  | "INVALID_CONFIG"
  | "AUTH_FAILED"
  | "RATE_LIMITED"
  | "BALANCE_INSUFFICIENT"
  | "NETWORK_ERROR"
  | "TIMEOUT"
  | "PROVIDER_UNAVAILABLE"
  | "INVALID_RESPONSE"
  | "JSON_PARSE_ERROR"
  | "PLACEHOLDER_MISMATCH"
  | "CONTENT_REJECTED"
  | "CANCELLED"
  | "UNKNOWN";

export class TranslationError extends Error {
  code: TranslationErrorCode;
  providerId?: string;
  retryable: boolean;
  statusCode?: number;
  details?: unknown;
}
```

用户提示：

```text
DeepSeek API Key 无效，请在设置中检查。
```

详情中可显示：

```text
HTTP 401 Unauthorized
```

---

# 46. 消息通信

```ts
export type ExtensionMessage =
  | { type: "TRANSLATE_PAGE"; payload: TranslatePagePayload }
  | { type: "PAUSE_TRANSLATION"; payload: SessionPayload }
  | { type: "RESUME_TRANSLATION"; payload: SessionPayload }
  | { type: "STOP_TRANSLATION"; payload: SessionPayload }
  | { type: "RESTORE_PAGE"; payload: SessionPayload }
  | { type: "RETRANSLATE_PAGE"; payload: TranslatePagePayload }
  | { type: "GET_PAGE_STATUS"; payload: FramePayload }
  | { type: "TRANSLATE_BATCH"; payload: TranslationBatch }
  | { type: "GET_SETTINGS" }
  | { type: "UPDATE_SETTINGS"; payload: Partial<ExtensionSettings> }
  | { type: "TEST_PROVIDER"; payload: ProviderConfig };
```

要求：

- 使用 zod 校验；
- 每条消息包含 requestId；
- frame 消息包含 tabId 和 frameId；
- 错误统一格式；
- 不允许任意字符串消息；
- 不向 Content Script 返回 API Key；
- Background 只返回必要状态。

---

# 47. 日志

级别：

- debug；
- info；
- warn；
- error。

生产默认只输出 warn 和 error。

禁止日志包含：

- API Key；
- Secret；
- Authorization；
- 完整网页正文；
- 密码；
- 私钥；
- 助记词；
- Access Token；
- Bearer Token。

诊断日志可以包含：

- Provider ID；
- Model；
- HTTP 状态码；
- 延迟；
- Batch 大小；
- 错误类型；
- 扩展版本；
- 浏览器版本；
- 堆栈。

导出日志前再次脱敏。

---

# 48. 性能要求

必须实现：

- TreeWalker；
- 分片扫描；
- `requestIdleCallback`；
- fallback；
- 每处理一定节点让出主线程；
- Web Worker 文本预处理；
- 批量缓存读取；
- 批量缓存写入；
- Mutation 去抖；
- WeakMap；
- 首屏优先；
- 可见性排序；
- Promise 去重；
- 请求去重；
- Session 校验；
- 清理引用。

目标：

- 10000 DOM 节点扫描不明显冻结页面；
- 单次主线程阻塞尽量低于 50ms；
- 缓存命中时接近即时；
- 中等页面 1 秒左右开始显示译文；
- 主要可见内容 2～5 秒内完成；
- 动态菜单 300～1000ms 内开始处理。

---

# 49. 页面布局保护

要求：

- UI 文案采用短译法；
- 不修改元素宽高；
- 不强制换行；
- 不覆盖原 CSS；
- 不插入全局样式；
- Shadow DOM 隔离扩展 UI；
- 菜单和按钮优先简短；
- 译文过长时不擅自缩放网页字体。

示例：

```text
Sign out → 退出登录
Personal settings → 个人设置
Usage → 用量
Billing → 账单
Credits → 额度
```

---

# 50. 特殊内容

必须保留：

- Emoji；
- 数字；
- 单位；
- 货币；
- URL；
- Email；
- 文件路径；
- Markdown；
- HTML 实体；
- 模板变量；
- 内联代码；
- 品牌名；
- 产品名；
- 模型名。

示例：

```text
10 GB
3.5%
$20
¥100
2 ms
1.5M
```

默认保留品牌：

```text
OpenAI
DeepSeek
GitHub
Google
Microsoft
BNB Chain
Ethereum
Binance
PostgreSQL
MySQL
React
Vue
Next.js
```

---

# 51. 安全

禁止：

- `eval`；
- `new Function`；
- 远程脚本；
- AI HTML 直接插入；
- Content Script 持有 API Key；
- 日志输出 Key；
- `window.postMessage` 发送敏感配置；
- 自动读取剪贴板；
- 翻译密码框；
- 翻译私钥；
- 翻译助记词；
- 自动上传图片；
- 自动发送整页 HTML。

翻译结果必须：

- 作为纯文本写入；
- 通过占位符校验；
- 通过长度合理性校验；
- 通过 HTML 检测；
- 通过 id 完整性校验。

---

# 52. 隐私

`PRIVACY.md` 必须说明：

1. 翻译时，网页纯文本会发送给用户选择的翻译服务商；
2. 默认不上传完整 HTML；
3. API Key 保存在本地；
4. 不翻译密码框；
5. 不主动读取剪贴板；
6. 不收集遥测，除非未来明确加入且用户主动开启；
7. 不向扩展开发者服务器上传翻译内容；
8. 用户可清理缓存和统计；
9. 自定义 Provider 的数据政策由对应服务商决定。

---

# 53. 网站限制

明确说明：

- `chrome://` 页面不能注入；
- Chrome Web Store 页面通常不能注入；
- closed Shadow DOM 无法可靠翻译；
- Canvas 文字无法直接翻译；
- 图片文字需要 OCR；
- 部分跨域 iframe 不能翻译；
- 某些频繁重渲染页面可能反复覆盖；
- 中文变长可能影响布局；
- AI 可能翻译错误；
- 速度取决于网络和 Provider；
- 不保证固定 3 秒完成。

---

# 54. 单元测试

至少覆盖：

- 文本过滤；
- URL 检测；
- Email 检测；
- 钱包地址检测；
- 哈希检测；
- JWT 检测；
- API Key 检测；
- 占位符提取；
- 占位符校验；
- 文本归一化；
- 语义分类；
- Cache Key；
- Batch 构建；
- Provider 响应解析；
- Provider 错误映射；
- 术语匹配；
- 网站规则；
- 页面语言检测；
- JSON 代码块清理；
- 缺失条目重试。

---

# 55. 集成测试

覆盖：

- 扫描 → 分组 → 翻译 → 回写；
- 缓存命中；
- Provider Fallback；
- Provider 熔断；
- 重试；
- 暂停；
- 继续；
- 停止；
- 恢复；
- Session 过期不回写；
- Mutation 动态翻译；
- SPA 路由；
- Open Shadow DOM；
- iframe；
- 双语；
- Hover；
- 属性翻译；
- 网站规则；
- 术语库变化导致缓存版本更新。

---

# 56. E2E

使用 Playwright 创建测试页面：

- 静态文章；
- React 动态页面；
- Vue 动态页面；
- Next 风格 SPA；
- Modal；
- Dropdown；
- Tooltip；
- Toast；
- 无限滚动；
- Shadow DOM；
- iframe；
- 大表格；
- 技术文档；
- 区块链后台；
- 设置页；
- Popup。

必须测试：

- 安装扩展；
- 填写测试 Provider；
- 翻译；
- 恢复；
- 刷新后重新提示；
- 动态弹窗自动翻译；
- 切换双语；
- 快捷键；
- 右键菜单；
- 缓存命中；
- 错误提示。

---

# 57. 验收标准

## 核心功能

- 可配置 DeepSeek；
- 可测试连接；
- 可翻译当前页面；
- 可暂停；
- 可继续；
- 可停止；
- 可恢复；
- 可重新翻译；
- 可翻译动态弹窗；
- 支持 React/Vue；
- 支持 Open Shadow DOM；
- 支持 iframe；
- 支持缓存；
- 支持术语；
- 支持双语；
- 支持 Hover；
- 支持网站规则；
- 支持统计；
- 支持快捷键；
- 支持右键菜单。

## 稳定性

- 超时不死锁；
- 取消后不回写；
- 恢复后不残留；
- 刷新后重新检测；
- 429 自动退避；
- 不无限重试；
- Mutation 不递归；
- Provider 失败自动切换；
- 单个 frame 失败不阻塞；
- API 错误友好展示。

## 安全

- API Key 不进入 Content Script；
- API Key 不进入日志；
- 不翻译密码；
- 不翻译私钥；
- AI 结果只作为纯文本；
- 无 eval；
- 无远程脚本；
- 无 unsafe-eval。

---

# 58. 开发阶段

## 阶段 1：工程基础

- 初始化 Vite；
- TypeScript；
- React；
- Manifest；
- Background；
- Content Script；
- Popup；
- Options；
- 消息系统；
- ESLint；
- Prettier；
- Vitest；
- 构建成功。

## 阶段 2：最小翻译链路

- DOM Scanner；
- Text Filter；
- Normalizer；
- Semantic Classifier；
- Context Builder；
- Batch Builder；
- DeepSeek Provider；
- Queue；
- Renderer；
- Restore。

完成后必须实现：

```text
点击翻译
→ 扫描页面
→ DeepSeek 批量翻译
→ 回写
→ 恢复
```

## 阶段 3：缓存和术语

- Memory Cache；
- IndexedDB；
- Cache Key；
- 术语库；
- Prompt Builder；
- 缓存统计。

## 阶段 4：动态页面

- MutationObserver；
- SPA；
- Shadow DOM；
- iframe；
- 动态小批次；
- 防递归；
- 重渲染恢复。

## 阶段 5：完整 UI

- Popup；
- Options；
- Provider 管理；
- 网站规则；
- 术语管理；
- 缓存管理；
- 统计；
- 深色模式。

## 阶段 6：高级体验

- 双语；
- Hover；
- 选中翻译；
- 右键菜单；
- 快捷键；
- 浮动工具条；
- 进度条。

## 阶段 7：测试和发布

- Unit；
- Integration；
- E2E；
- 大页面压测；
- 构建 ZIP；
- README；
- CHANGELOG；
- PRIVACY；
- LICENSE。

---

# 59. package.json 脚本

至少包含：

```json
{
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build",
    "typecheck": "tsc --noEmit",
    "lint": "eslint .",
    "lint:fix": "eslint . --fix",
    "format": "prettier --write .",
    "test": "vitest run",
    "test:watch": "vitest",
    "test:e2e": "playwright test",
    "test:all": "npm run typecheck && npm run lint && npm run test && npm run test:e2e",
    "package": "node scripts/package-extension.mjs"
  }
}
```

---

# 60. README

必须包含：

- 项目介绍；
- 功能；
- 截图位置；
- 安装；
- 开发；
- 构建；
- 打包；
- DeepSeek 配置；
- OpenAI Compatible 配置；
- 百度配置；
- 有道配置；
- 翻译模式；
- 领域模式；
- 术语库；
- 网站规则；
- 快捷键；
- 缓存；
- 隐私；
- 常见错误；
- 已知限制；
- 故障排查。

---

# 61. 常见错误文案

DeepSeek：

```text
API Key 无效，请检查密钥。
账户余额不足，请充值后重试。
请求过于频繁，扩展将在稍后自动重试。
模型不存在，请检查模型名称。
请求超时，请检查网络或提高超时时间。
DeepSeek 服务暂时不可用，已尝试备用翻译源。
```

百度：

```text
52003：未授权用户，请检查 APP ID、密钥和服务开通状态。
54003：请求频率过高。
54004：账户余额不足。
58000：客户端 IP 不合法。
58001：不支持当前语言方向。
```

通用：

```text
翻译服务返回了无法解析的结果。
部分内容翻译失败，已保留原文。
当前页面不允许扩展注入。
部分 iframe 无法翻译。
closed Shadow DOM 无法访问。
```

---

# 62. 最终交付

最终生成：

```text
dist/
ai-web-translator-v2.2.0.zip
README.md
CHANGELOG.md
PRIVACY.md
LICENSE
```

ZIP 解压后可直接：

```text
Chrome
→ 扩展程序
→ 开发者模式
→ 加载已解压的扩展程序
→ 选择 dist
```

---

# 63. 最终执行检查清单

Codex 在结束前逐项确认：

```text
[ ] npm install 成功
[ ] npm run typecheck 成功
[ ] npm run lint 成功
[ ] npm run test 成功
[ ] npm run build 成功
[ ] npm run test:e2e 成功
[ ] dist 存在
[ ] manifest 路径正确
[ ] Background 可启动
[ ] Popup 可打开
[ ] Options 可打开
[ ] DeepSeek 测试连接可用
[ ] 页面翻译可用
[ ] 恢复原文可用
[ ] 暂停和停止可用
[ ] 动态弹窗可翻译
[ ] 缓存可用
[ ] 术语库可用
[ ] 网站规则可用
[ ] 双语模式可用
[ ] Hover 原文可用
[ ] 快捷键可用
[ ] 右键菜单可用
[ ] API Key 未出现在 Content Script
[ ] API Key 未出现在日志
[ ] AI 结果未通过 innerHTML 插入
[ ] 无 eval
[ ] 无远程脚本
[ ] ZIP 可安装
[ ] README 完整
[ ] PRIVACY 完整
```

---

# 64. Codex 最终输出格式

完成后，Codex 应输出：

1. 已完成模块列表；
2. 项目目录；
3. 构建结果；
4. 测试结果；
5. ZIP 路径；
6. 安装方法；
7. DeepSeek 配置方法；
8. 已知限制；
9. 未完成项；如果没有则明确写“无核心未完成项”；
10. 关键性能结果；
11. 安全检查结果。

不要只回复“项目已完成”，必须给出可验证结果。

---

# 65. 额外实现建议

以下建议不是硬性要求，但建议实现：

- 使用 `zod` 校验 Provider 配置；
- 使用 `idb` 管理 IndexedDB；
- 使用 `WeakMap` 保存原文；
- 使用 `AbortController` 管理页面翻译 Session；
- 使用 `IntersectionObserver` 做首屏优先；
- 使用 `requestIdleCallback` 分片扫描；
- 使用 Web Worker 做文本归一化和 Token 粗估；
- 使用 `nanoid` 生成 requestId；
- 使用指数退避和熔断；
- Provider 测试连接使用极小请求；
- 对短 UI 文本使用专门 Prompt；
- 对正文和 UI 分批；
- 缓存命中先立即回写，再请求未命中内容；
- Dynamic Mutation 只处理新增区域；
- 恢复页面时彻底断开观察器；
- 每次翻译建立独立 sessionId；
- 回写前检查 sessionId 是否仍有效。

---

# 66. 完成标准

只有同时满足以下条件，项目才算完成：

- 核心功能真实可运行；
- 能直接构建；
- 能直接安装；
- 能实际调用 DeepSeek；
- 能翻译普通页面；
- 能翻译动态页面；
- 能恢复原文；
- 能处理错误；
- 有测试；
- 有文档；
- 有隐私说明；
- 有可安装 ZIP；
- 没有核心 TODO；
- 没有明文泄露 API Key；
- 没有危险 DOM 注入；
- 没有明显无限循环和请求风暴。

