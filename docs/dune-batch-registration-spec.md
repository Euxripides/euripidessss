# Dune 批量注册系统 — 最终技术规格

## 0. 核心约束

- **全流程自动化**：从邮箱生成到凭据提取，不需任何人工操作
- **交付标准**：必须完整跑通至少 1 个注册流程，否则持续修改直到跑通
- **前端极简**：只显示 1) 邮箱配置 2) 已注册账户列表（邮箱+密码）

## 1. 凭据配置

```
域名:     aurore.online          (腾讯云, NS→Cloudflare)
接收邮箱: ldj1009538134@gmail.com
Gmail App Password: gaxcqwvetvclwcye
IMAP:     imap.gmail.com:993
          用户名: ldj1009538134@gmail.com
          密码:   gaxcqwvetvclwcye
```

## 2. 全自动流程

```
POST /api/dune/batch/start {total: N}
  │
  ├─ [Go] genAccount()
  │      email  = "dune_<8hex>@aurore.online"
  │      username = "u<8hex>"
  │      password = genPassword()  // 16位, 大小写+数字+符号
  │
  ├─ [Bridge] register.mjs
  │     navigate → dune.com/signup
  │     autoDetect: 用 Playwright 自动查找注册表单的 input/button
  │     填 email, username, password → 提交
  │     返回 {ok: true} 或 {captcha: true}
  │     ★ CAPTCHA: 通知前端显示, 暂停等待用户手动点后继续
  │
  ├─ [Go] imapWait(5min)
  │     IMAP 连 Gmail, 搜 from:hello@dune.com, subject:Verify
  │     解析 HTML → 提取验证链接
  │     超时则重试当前账号
  │
  ├─ [Go] httpGET(验证链接)
  │     纯 HTTP, 跟随重定向
  │     检查最终 URL 不含 error
  │
  ├─ [Bridge] login.mjs
  │     navigate → dune.com/login
  │     autoDetect: 自动找登录表单 → 填 email+password → 提交
  │     检查结果 (URL 变化 / 错误提示)
  │
  ├─ [Bridge] onboard.mjs (onboarding 自动跳过)
  │     循环: 找 Skip/Next/Continue 按钮 → 点击 → 等待
  │     退出条件: URL 含 /queries 或 10 秒内无新弹窗
  │
  ├─ [Bridge] extract.mjs
  │     等待主界面 (URL 含 /queries)
  │     提取: cookies (csrf/auth-refresh/auth-user/auth-id-token)
  │           localStorage accessToken (Cognito)
  │           authorization = Bearer <auth-id-token>
  │     返回给 Go
  │
  └─ [Go] saveAccount()
         追加到内存 + accounts.json
         team_id 通过 FindQuery API 获取
         status = "done"
         前端 WebSocket 推送更新
```

## 3. Playwright 自动检测策略

每个页面不硬编码 selector，而是自动查找：

```javascript
// register.mjs — 注册页
async function detectSignupForm(page) {
    // 1. 找 email 输入框
    const emailInput = await page.locator('input[type="email"], input[name*="email"], input[id*="email"], input[placeholder*="email"]').first();
    // 2. 找 username 输入框
    const userInput = await page.locator('input[name*="username"], input[id*="username"], input[name*="name"], input[placeholder*="user"]').first();
    // 3. 找 password 输入框 (通常是 type=password 的上一个)
    const passInputs = await page.locator('input[type="password"]').all();
    // 4. 找提交按钮
    const submitBtn = await page.locator('button[type="submit"], button:has-text("Sign"), button:has-text("Create"), button:has-text("Register"), button:has-text("Continue")').first();
    return { emailInput, userInput, passInput: passInputs[0], passConfirm: passInputs[1], submitBtn };
}

// login.mjs — 登录页
async function detectLoginForm(page) {
    // 类似注册页, 找 email + password + Submit/Log in 按钮
}

// onboard.mjs — onboarding
async function skipOnboarding(page) {
    const skipTexts = ['Skip', 'Next', 'Continue', 'Get started', 'Maybe later', 'Not now'];
    const maxRounds = 20;
    for (let i = 0; i < maxRounds; i++) {
        await page.waitForTimeout(500);
        // 检查是否已到主界面
        if (page.url().includes('/queries') || page.url().includes('/discover')) return;
        // 找 skip 按钮
        for (const text of skipTexts) {
            const btn = page.locator(`button:has-text("${text}"), a:has-text("${text}")`).first();
            if (await btn.isVisible({ timeout: 1000 }).catch(() => false)) {
                await btn.click();
                break;
            }
        }
        // 关弹窗
        const closeBtn = page.locator('[aria-label="Close"], [data-testid="close"], .modal-close');
        if (await closeBtn.isVisible({ timeout: 500 }).catch(() => false)) {
            await closeBtn.click();
        }
    }
}
```

**降级策略**：如果自动检测失败，输出当前页面 HTML 到日志，返回 `{error: "detection_failed", html: "..."}`，前端展示供调试。

## 4. 密码生成规则

```go
func GenPassword() string {
    // 16 位: 大写字母×4 + 小写字母×8 + 数字×2 + 特殊符号×2
    // 使用 crypto/rand, 确保均匀分布
    // 特殊符号从 !@#$%^&* 中随机选 2 个
    // 最终打乱顺序
}
```

## 5. 前端设计

```
┌─ 1. 邮箱配置 ───────────────────────────────────┐
│  域名: aurore.online         接收邮箱: ldj1...@gmail.com │
│  注册数量: [10]  间隔秒数: [60]                        │
│  [开始注册]  [停止]  [清空列表]                        │
└─────────────────────────────────────────────────────┘

┌─ 2. 已注册账户 ──────────────────────────────────┐
│  ⚠ 密码仅当前会话可用。 [导出 CSV]                    │
│                                                    │
│  #  邮箱                            密码       状态  │
│  ─────────────────────────────────────────────── │
│  1  dune_a3f2b8c1@aurore.online     X9k$L2mN   ✅  │
│  2  dune_f7e1d2a9@aurore.online     7P@qW3xZ   ✅  │
│  3  注册中...                           ⏳        │
│     ├ 表单已提交，等待验证邮件...                    │
│  4  dune_9c4b5a3d@aurore.online     -          ❌  │
│     ├ 错误: 邮箱已被注册                            │
│                                                    │
│  [全部复制 JSON]                                    │
└────────────────────────────────────────────────────┘
```

## 6. 测试流程

### 6.1 环境准备
```bash
# 1. 确认 DNS 生效
nslookup -type=MX aurore.online

# 2. 确认 Email Routing 正常
# 发邮件到 test@aurore.online → Gmail 收件箱确认收到

# 3. 确认 IMAP 连通
node -e "
const imap = require('imap-simple');
imap.connect({...}).then(() => console.log('IMAP OK'));
"

# 4. 确认 Playwright 正常
node tools/dune-playwright/test.mjs  # 启动浏览器 → 截图 dune.com

# 5. go get 依赖
go get github.com/emersion/go-imap/v2
go get nhooyr.io/websocket

# 6. 编译
go build -o bin\etl-server.exe .\cmd\server\
```

### 6.2 单次注册测试
```bash
# 启动服务
.\run.ps1

# 注册 1 个账号
curl -X POST http://127.0.0.1:8000/api/dune/batch/start -H "Content-Type: application/json" -d '{"total":1}'

# 查看状态
curl http://127.0.0.1:8000/api/dune/batch/status

# 查看账户
curl http://127.0.0.1:8000/api/dune/batch/accounts
```

### 6.3 验收标准
| 项目 | 通过条件 |
|------|----------|
| 邮箱生成 | `dune_<8hex>@aurore.online` 格式正确 |
| 注册表单 | Playwright 自动检测并提交 |
| 邮件接收 | Gmail 收到 hello@dune.com 验证邮件 |
| 链接提取 | 正确解析 HTML 中的验证链接 |
| 邮箱验证 | HTTP GET 成功验证 |
| 登录 | Playwright 自动登录成功 |
| Onboarding | 自动 Skip 直到 /queries 页面 |
| 凭据提取 | Cookie/Authorization/AccessToken 全部提取 |
| 前端显示 | 账户列表显示邮箱+密码+状态 |
| CSV 导出 | 导出文件包含完整凭据 |

## 7. 文件清单

```
新增文件:
  internal/dunetools/types.go
  internal/dunetools/generator.go
  internal/dunetools/imap.go
  internal/dunetools/manager.go
  internal/dunetools/ws.go
  internal/api/handler_dune_batch.go
  tools/dune-playwright/register-login.mjs    (合并 register+login+onboard+extract 为一个脚本)
  frontend/src/features/tools/DuneBatchReg.tsx

修改文件:
  go.mod                         (+go-imap +websocket)
  cmd/server/main.go             (+初始化 dunetools)
  internal/api/router.go         (+路由注册)
  frontend/src/App.tsx           (+导航入口)
  frontend/src/styles/layout.css (+新页面样式)

删除/不新增:
  tools/dune-playwright/login.mjs      → 合并到 register-login.mjs
  tools/dune-playwright/register.mjs   → 合并到 register-login.mjs
  tools/dune-playwright/onboard.mjs    → 合并到 register-login.mjs
  tools/dune-playwright/extract.mjs    → 合并到 register-login.mjs
  frontend/src/features/tools/DuneAccountList.tsx  → 合并到 DuneBatchReg.tsx
```

## 8. 已确认决策

| # | 决策 | 选择 |
|---|------|------|
| 1 | Onboarding | 方案 A — 自动 Skip |
| 2 | 注册间隔 | 默认 60 秒/个, 可配置 |
| 3 | Gmail | ldj1009538134@gmail.com + 秘钥：gaxc qwve tvcl wcye |
| 4 | 域名 | aurore.online (Cloudflare NS) |
| 5 | CAPTCHA | 自动检测 + 前端通知 + 手动过 |
| 6 | 导出 | CSV (邮箱+密码+Cookie+Auth+Token) |
| 7 | 密码 | 内存保存, API 返回, 不落盘 |
| 8 | Playwright 策略 | 自动检测页面元素, 不硬编码 selector |
| 9 | 完整流程 | 必须全部跑通才交付, 否则持续修改 |
| 10 | 前端 | 仅 2 块: 邮箱配置 + 注册账户列表 |
