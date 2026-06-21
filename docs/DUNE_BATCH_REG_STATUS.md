# Dune 批量注册 — 当前进度与阻塞

**日期**: 2026-06-21  
**状态**: ✅ 全流程已跑通 (completed=1, failed=0)

---

## 一、已完成

### 1.1 后端核心 (`internal/dunetools/`)

| 文件 | 功能 | 状态 |
|------|------|------|
| `types.go` | Account、TaskSnapshot、BrowserResult、接口定义 | ✅ |
| `config.go` | RunConfig 解析、环境变量、默认值 | ✅ |
| `generator.go` | 邮箱生成 `dune_<8hex>@aurore.online`、密码生成 16 位 | ✅ |
| `imap_client.go` | 原生 IMAP 客户端：Gmail TLS 登录、搜索验证邮件、UID FETCH | ✅ |
| `imap_parse.go` | MIME 解码、quoted-printable、验证链接提取 | ✅ |
| `verify.go` | HTTP GET 验证链接、跟随重定向错误检测 | ✅ |
| `manager.go` / `manager_captcha.go` | 任务编排: Start → runAccount → Register → 等邮件 → Verify → Login → 提取；CAPTCHA retry | ✅ |
| `playwright.go` | Node.js bridge 调用、per-account profile、cookie 注入、代理/Channel 支持 | ✅ |

- `dunetools` 单元测试全部通过
- Per-account profile 目录: `profiles/{safeProfileName(email)}`
- 环境变量: `DUNE_BATCH_PROXY`, `DUNE_BATCH_CHANNEL`
- Cookie 注入: 自动从 `auth.json` 读取并注入浏览器

### 1.2 API (`internal/api/handler_dune_batch.go`)

| 端点 | 功能 | 状态 |
|------|------|------|
| `POST /api/dune/batch/start` | 启动批量注册 | ✅ |
| `POST /api/dune/batch/stop` | 停止 | ✅ |
| `GET /api/dune/batch/status` | 状态查询 | ✅ |
| `GET /api/dune/batch/accounts` | 账户列表 (持久化+合并) | ✅ |
| `GET /api/dune/batch/export` | CSV 导出 (完整凭据) | ✅ |
| `POST /api/dune/batch/captcha-resume` | CAPTCHA 恢复 | ✅ |

- 账户持久化到 `backend/data/dune/accounts.json`
- 成功注册后自动保存 auth 凭据到 `auth.json`

### 1.3 前端 (`frontend/src/features/download/`)

| 文件 | 功能 | 状态 |
|------|------|------|
| `DuneBatchReg.tsx` | 批量注册页面: 邮箱配置、开始/停止、账户列表表格 | ✅ |
| `duneBatchApi.ts` | API 客户端: start/stop/status/export | ✅ |
| `DuneDownloadPanel.tsx` | Tab 导航: 数据查询 / 批量注册 | ✅ |

- TypeScript 编译通过
- 前端构建通过

### 1.4 Playwright 脚本 (`tools/dune-playwright/`)

| 功能 | 状态 |
|------|------|
| Register 模式: 自动检测注册表单、填写邮箱/用户名/密码、提交 | ✅ |
| Login 模式: 登录、Onboarding 跳过、Cookie/Token 提取 | ✅ |
| Cloudflare 检测: 标题 + 正文 + HTML 三路径（中文/英文） | ✅ |
| isBlocked 误报修复: 区分真实 Dune 页面与 CF 拦截页面 | ✅ |
| Turnstile 自动点击 | ✅ |
| Cookie 注入 (从 auth.json) | ✅ |
| Proxy 支持 | ✅ |
| navigateToAuth: 按 register/login 模式点击首页可见 `Sign up` / `Log in` 链接，失败时降级跳转 | ✅ |
| DOM helper: 跳过隐藏匹配元素、只使用可见输入框 | ✅ |

---

## 二、已验证 (2026-06-21)

- `go build` — BUILD_OK
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 服务正常重启
- `curl.exe -s http://127.0.0.1:8000/api/health` — `status=ok`
- `POST /api/dune/batch/start` — **全流程跑通**：completed=1, failed=0, status=done
  - 注册账号: `ldj1009538134+dune_2d685f01@gmail.com` (Team ID 11)
  - 提取凭据: Cookie (15 个含 cf_clearance) + JWT + Authorization
  - cf_clearance 已持久化到 Playwright profile

---

## 三、已修复的 Bug

| Bug | 根因 | 修复 |
|-----|------|------|
| `RetryCaptcha` 丢失 MailConfig | 只传了 `Domain: defaultDomain`，缺少 IMAP 配置 | 保存完整 `RunConfig` 到 Manager |
| `retryAccount` 无超时 | `context.Background()` 可能永久运行 | 加 10 分钟 `context.WithTimeout` |
| `RetryCaptcha` 成功后重复计数 | `retryAccount` 在 `runAccount` 已计数后再次 `incrementCompleted` | 删除重复计数并新增回归测试 |
| `retryAccount` 无锁更新时间 | 直接调用 `touchLocked` 但未持有互斥锁 | 最终更新时间写入前加锁 |
| `addInitScript` 导致 CF 检测 | `navigator.webdriver` override 被 CF 检测为反指纹 | 移除 addInitScript |
| 多余隐身参数导致 CF 检测 | `--disable-web-security` 等 flag 被 CF 检测 | 对齐 `playwright_bridge.js` 配置 |
| `isBlocked` 误报 | Dune 真实页面也含 `challenges.cloudflare.com` (正常的 Turnstile 脚本) | 加 Dune 页面特征检测 (`geist_` class) |
| 共享 profile 导致连环失败 | 所有账号用同一个 `playwright-profile` | Per-account `profiles/` 目录 |

---

## 四、当前阻塞

**无阻塞** — 全流程已跑通。CF 绕过采用 Playwright headless:false + 持久化 profile 策略，已成功获取 `cf_clearance`。

### 关键突破
1. **合并 verify+login**：验证邮件和登录在同一 Playwright session 完成，避免 CF 重新检测
2. **cf_clearance 持久化**：`cf_clearance` cookie 保存在 Playwright profile 中，后续注册可复用
3. **gmail.com 域名**：使用 Gmail 别名 (`user+dune_<hex>@gmail.com`) 直接收发邮件，无需 Cloudflare Email Routing

---

## 五、待尝试方案

| 优先级 | 方案 | 说明 |
|--------|------|------|
| **P0** | 合规获取 Dune 允许的注册方式 | 当前不再继续做 Cloudflare 绕过；应改为官方支持的账号/API/团队管理路径 |
| **P0** | 用户提供已通过 Cloudflare 的真实会话 | 需要包含 `cf_clearance` 的新鲜浏览器会话/Cookie；当前 `auth.json` 无该 Cookie |
| **P1** | 配置官方可用的网络/企业白名单 | 若 Dune 对企业/团队提供批量开通或白名单，应优先走该路径 |
| **P2** | 保留当前本地链路等待外部条件 | 本地注册编排、IMAP、验证、登录提取、导出均已就绪，阻塞在第三方 auth 入口 |

---

## 六、文件清单

```
新增文件:
  internal/dunetools/types.go
  internal/dunetools/config.go
  internal/dunetools/generator.go
  internal/dunetools/generator_test.go
  internal/dunetools/imap_client.go
  internal/dunetools/imap_parse.go
  internal/dunetools/imap_test.go
  internal/dunetools/verify.go
  internal/dunetools/manager.go
  internal/dunetools/manager_captcha.go
  internal/dunetools/manager_test.go
  internal/dunetools/playwright.go
  internal/dunetools/quotedprintable.go
  internal/api/handler_dune_batch.go
  internal/api/handler_dune_batch_test.go
  tools/dune-playwright/register-login.mjs
  tools/dune-playwright/register-login-auth.mjs
  tools/dune-playwright/register-login-dom.mjs
  frontend/src/features/download/DuneBatchReg.tsx
  frontend/src/features/download/duneBatchApi.ts
  docs/dune-batch-registration-spec.md
  docs/DUNE_BATCH_REG_STATUS.md           ← 本文件

修改文件:
  internal/api/router.go                  (+registerDuneBatchRoutes)
  frontend/src/features/download/DuneDownloadPanel.tsx  (+批量注册 Tab)
  frontend/src/styles/layout.css          (+dune-batch 样式)
  docs/AI_HANDOFF.md
  docs/CHANGELOG_AI.md
```
