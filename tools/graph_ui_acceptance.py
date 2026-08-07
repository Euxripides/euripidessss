# 地址关系图 V2.0 UI 整改 — 视觉验收脚本（设计文档 §18.3 / 整改方案 §12）
#
# 输出（保存到 docs/screenshots/）：
#   graph-workspace-global.png      全局模式 1440×900
#   graph-workspace-focus.png       聚焦模式 1440×900
#   graph-workspace-inspector.png   Inspector（统计 Tab）1440×900
#   graph-workspace-mobile.png      移动端 390×844 + 全屏 Drawer
#
# 同时输出结构断言（顶栏 72px / 节点 390×64 / Inspector 372px / 语义色 /
# 图例常驻 / 退出聚焦 / 40 次滚轮 DOM 稳定 / 控制台无错误）。

import asyncio
import json
import os
import sys

from playwright.async_api import async_playwright

BASE = "http://127.0.0.1:8000"
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT_DIR = os.path.join(ROOT, "docs", "screenshots")
FOCUS_ADDR = "0x000000000000000000000000000000000000dead"

REPORT = {}
CONSOLE_ERRORS = []


async def open_graph_page(page):
    await page.goto(BASE, wait_until="domcontentloaded")
    # 初始（其他页面）：应用顶部横条保留
    REPORT["其他页面保留顶部横条"] = await page.locator(".app-header").count() == 1
    menu = page.locator(".ant-menu-item").filter(has_text="地址关系图").first
    await menu.wait_for(state="visible", timeout=20000)
    await menu.click()
    try:
        await page.wait_for_selector(".flow-workspace-header", timeout=20000)
    except Exception:
        await page.screenshot(path=os.path.join(OUT_DIR, "debug-mount-fail.png"), full_page=False)
        html = await page.content()
        print("DEBUG: workspace header not found. url=", page.url)
        print("DEBUG: html head:", html[:400])
        raise
    # 图谱页：顶部横条隐藏 + 页面占满视口
    REPORT["图谱页隐藏顶部横条"] = await page.locator(".app-header").count() == 0
    page_h = await page.evaluate("() => document.querySelector('.analytics-graph-page')?.offsetHeight ?? -1")
    vh = await page.evaluate("() => window.innerHeight")
    REPORT["图谱页高度≈100vh"] = abs(page_h - vh) <= 2
    # 品牌标题与侧栏收起（进入地址关系图自动全屏）
    try:
        brand = await page.locator(".flow-workspace-brand strong").inner_text()
        REPORT["品牌标题=资金流向追踪"] = brand == "资金流向追踪"
        side_info = await page.evaluate(
            "() => { const el = document.querySelector('.side');"
            " return { collapsed: !!document.querySelector('.ant-layout-sider-collapsed'), w: el ? el.offsetWidth : -1 }; }"
        )
        REPORT["侧栏收起(72px)"] = side_info["collapsed"] and side_info["w"] == 72
    except Exception as exc:
        print("DEBUG: brand/sider check failed:", exc)
    # 等待图谱渲染（fetchGraph + 布局 + fitView）
    try:
        await page.wait_for_selector(".react-flow__node", timeout=40000)
    except Exception as exc:
        print("DEBUG: react-flow node timeout, url=", page.url, exc)
        print("DEBUG: console errors:", CONSOLE_ERRORS[-10:])
        body = await page.evaluate("() => document.body ? document.body.innerHTML.slice(0, 1200) : 'NO BODY'")
        print("DEBUG: body html:", body)
        await page.screenshot(path=os.path.join(OUT_DIR, "debug-no-nodes.png"), full_page=False)
    await page.wait_for_timeout(1800)


async def focus_address(page, address):
    search = page.get_by_label("地址搜索")
    await search.fill(address)
    await search.press("Enter")
    await page.wait_for_selector(".focus-address-node.relation-selected", timeout=15000)
    await page.wait_for_timeout(1500)


async def structural_checks(page, tag):
    checks = {}

    async def css(selector, prop):
        el = page.locator(selector).first
        if await el.count() == 0:
            return None
        return await el.evaluate("(node, p) => getComputedStyle(node)[p]", prop)

    async def size(selector):
        return await page.evaluate(
            "(s) => { const el = document.querySelector(s); return el ? { w: el.offsetWidth, h: el.offsetHeight } : null; }",
            selector,
        )

    header = await size(".flow-workspace-header")
    checks[f"{tag}: 顶栏高度 72px"] = header and header["h"] == 72

    node = await size(".focus-address-node")
    checks[f"{tag}: 节点 390×64"] = node and node["w"] == 390 and node["h"] == 64

    inspector = await size(".flow-inspector")
    checks[f"{tag}: Inspector 360–390px"] = inspector and 360 <= inspector["w"] <= 390

    checks[f"{tag}: 图例常驻可见"] = await page.locator(".flow-legend").is_visible()
    checks[f"{tag}: 画布暗色点阵背景"] = "radial-gradient" in await css(
        ".flow-canvas-shell", "background-image"
    ) and (await css(".flow-canvas-shell", "background-color")) == "rgb(7, 21, 34)"
    checks[f"{tag}: 页面背景 #06111e"] = (await css(
        ".analytics-graph-page", "backgroundColor"
    )) == "rgb(6, 17, 30)"
    checks[f"{tag}: 计数徽章"] = await page.locator(".flow-canvas-count-badge").is_visible()

    sel = await css(".focus-address-node.relation-selected", "borderTopColor")
    checks[f"{tag}: 选中节点青色 #26d9e8"] = sel == "rgb(38, 217, 232)"

    # 全局模式节点统一为未聚焦色（设计 §11.3），语义色仅在聚焦模式校验
    if await page.locator(".focus-address-node.relation-upstream").count() > 0:
        up = await css(".focus-address-node.relation-upstream", "borderTopColor")
        checks[f"{tag}: 上游蓝色 #3f97ff"] = up == "rgb(63, 151, 255)"
    if await page.locator(".focus-address-node.relation-downstream").count() > 0:
        down = await css(".focus-address-node.relation-downstream", "borderTopColor")
        checks[f"{tag}: 下游橙色 #f59e32"] = down == "rgb(245, 158, 50)"

    checks[f"{tag}: 边标签存在"] = await page.locator(".analytics-flow-edge-label").count() > 0
    checks[f"{tag}: 完整地址可见"] = await page.locator(".focus-address-text").first.is_visible()
    full = await page.locator(".focus-address-text").first.inner_text()
    checks[f"{tag}: 地址非缩略(长度≥40)"] = len(full) >= 40

    REPORT.update(checks)


async def run():
    os.makedirs(OUT_DIR, exist_ok=True)
    async with async_playwright() as p:
        browser = await p.chromium.launch(channel="msedge", headless=True)

        # ── 1. 全局模式 1440×900 ──
        ctx = await browser.new_context(viewport={"width": 1440, "height": 900})
        page = await ctx.new_page()
        console_errors = []
        page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: console_errors.append(str(e)))
        CONSOLE_ERRORS.extend(console_errors)
        await open_graph_page(page)
        await page.screenshot(path=os.path.join(OUT_DIR, "graph-workspace-global.png"), full_page=False)
        await structural_checks(page, "全局")
        global_nodes = await page.locator(".react-flow__node").count()
        global_edges = await page.locator(".react-flow__edge").count()
        REPORT["全局: 节点数>0"] = global_nodes > 0
        REPORT["全局: 边数>0"] = global_edges > 0
        await ctx.close()

        # ── 2. 聚焦模式（搜索 0xdead）──
        ctx = await browser.new_context(viewport={"width": 1440, "height": 900})
        page = await ctx.new_page()
        page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: console_errors.append(str(e)))
        await open_graph_page(page)
        await focus_address(page, FOCUS_ADDR)
        await page.screenshot(path=os.path.join(OUT_DIR, "graph-workspace-focus.png"), full_page=False)
        await structural_checks(page, "聚焦")
        REPORT["聚焦: 模式标签"] = await page.locator(".flow-canvas-mode-label").is_visible()
        REPORT["聚焦: 退出聚焦按钮"] = await page.locator(".flow-exit-focus").is_visible()
        REPORT["聚焦: 已选择徽章"] = await page.locator(".focus-selected-label").is_visible()

        # 合并搜索：数据集外合法地址 → 提示可前往地址详情页（原顶部横条功能）
        await page.get_by_label("地址搜索").fill("0x1111111111111111111111111111111111111111")
        await page.get_by_label("地址搜索").press("Enter")
        await page.wait_for_timeout(600)
        notices = await page.locator(".ant-message-notice").all_inner_texts()
        REPORT["数据集外地址提示前往详情页"] = any("前往地址详情页" in n for n in notices)

        # Inspector 折叠/展开（1440px dock 模式）
        await page.get_by_label("收起地址详情").click()
        await page.wait_for_timeout(400)
        REPORT["1440px: 折叠后展开条出现"] = await page.locator(".flow-inspector-collapse-bar").is_visible()
        REPORT["1440px: 折叠后画布占满"] = await page.locator(
            ".flow-workspace-body > .flow-inspector-dock .flow-inspector"
        ).count() == 0
        await page.get_by_label("展开地址详情").click()
        await page.wait_for_timeout(400)
        REPORT["1440px: 展开后 Inspector 恢复"] = await page.locator(".flow-inspector-dock .flow-inspector").is_visible()

        # ── 3. Inspector（统计 Tab）──
        await page.locator(".flow-inspector .ant-tabs-tab").filter(has_text="统计").click()
        await page.wait_for_timeout(900)
        await page.screenshot(path=os.path.join(OUT_DIR, "graph-workspace-inspector.png"), full_page=False)
        REPORT["Inspector: 相邻地址列表"] = await page.locator(".flow-neighbor-item").count() > 0
        REPORT["Inspector: 交易记录加载"] = await page.locator(".flow-inspector .ant-tabs-tab").filter(has_text="交易记录").count() > 0

        # ── 4. 40 次滚轮压力测试（设计 §18.3）──
        canvas = page.locator(".flow-canvas-shell")
        box = await canvas.bounding_box()
        await page.mouse.move(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2)
        node_count_before = await page.locator(".react-flow__node").count()
        edge_count_before = await page.locator(".react-flow__edge").count()
        for i in range(40):
            await page.mouse.wheel(0, -120 if i < 20 else 120)
            await page.wait_for_timeout(30)
        await page.wait_for_timeout(800)
        node_count_after = await page.locator(".react-flow__node").count()
        edge_count_after = await page.locator(".react-flow__edge").count()
        interacting = await page.evaluate(
            "() => document.querySelector('.flow-workspace-interacting') !== null"
        )
        REPORT["压力: 40 次滚轮节点数不变"] = node_count_before == node_count_after
        REPORT["压力: 40 次滚轮边数不变"] = edge_count_before == edge_count_after
        REPORT["压力: 交互结束动画恢复(无 interacting 类)"] = not interacting

        # 右上角 × 关闭地址详情 = 退出聚焦 + 折叠面板
        await page.get_by_label("关闭详情").click()
        await page.wait_for_timeout(400)
        REPORT["1440px: ×关闭后面板折叠"] = await page.locator(".flow-inspector-collapse-bar").is_visible()
        mode_text = await page.locator(".flow-canvas-mode-label").inner_text()
        REPORT["1440px: ×关闭后退出聚焦"] = "聚焦模式" not in mode_text
        await page.get_by_label("展开地址详情").click()
        await page.wait_for_timeout(400)
        REPORT["1440px: 关闭后可重新展开"] = await page.locator(".flow-inspector").is_visible()
        await ctx.close()

        # ── 5. 移动端 390×844（Drawer 全屏）：先桌面进入图谱，再缩小视口 ──
        ctx = await browser.new_context(viewport={"width": 1440, "height": 900})
        page = await ctx.new_page()
        page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: console_errors.append(str(e)))
        await open_graph_page(page)
        await page.set_viewport_size({"width": 390, "height": 844})
        await page.wait_for_timeout(800)
        await focus_address(page, FOCUS_ADDR)
        await page.wait_for_timeout(1200)
        drawer = page.locator(".flow-inspector-drawer")
        REPORT["移动端: Drawer 打开"] = await drawer.is_visible()
        if await drawer.is_visible():
            box = await drawer.bounding_box()
            REPORT["移动端: Drawer 全屏宽"] = box and box["width"] >= 389
        await page.screenshot(path=os.path.join(OUT_DIR, "graph-workspace-mobile.png"), full_page=False)
        await ctx.close()

        # ── 6. 中档断点：1024px 可折叠 / 820px 抽屉（设计 §5.5）──
        ctx = await browser.new_context(viewport={"width": 1024, "height": 800})
        page = await ctx.new_page()
        page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: console_errors.append(str(e)))
        await open_graph_page(page)
        await focus_address(page, FOCUS_ADDR)
        collapsible = page.locator(".flow-inspector-collapsible")
        REPORT["1024px: 可折叠容器存在"] = await collapsible.count() > 0
        if await collapsible.count() > 0:
            w = await page.locator(".flow-inspector-collapsible .flow-inspector").evaluate(
                "(el) => el.offsetWidth"
            )
            REPORT["1024px: Inspector 默认 340px"] = w == 340
        await page.locator(".flow-inspector-collapse-toggle").click()
        await page.wait_for_timeout(400)
        REPORT["1024px: 折叠条出现"] = await page.locator(".flow-inspector-collapse-bar").is_visible()
        await ctx.close()

        ctx = await browser.new_context(viewport={"width": 820, "height": 800})
        page = await ctx.new_page()
        page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: console_errors.append(str(e)))
        await open_graph_page(page)
        await focus_address(page, FOCUS_ADDR)
        REPORT["820px: 无常驻 Inspector"] = await page.locator(".flow-workspace-body > .flow-inspector").count() == 0
        REPORT["820px: Drawer 打开"] = await page.locator(".flow-inspector-drawer").is_visible()
        dbox = await page.locator(".flow-inspector-drawer").bounding_box()
        REPORT["820px: Drawer 约 340px（非全屏）"] = dbox is not None and 300 <= dbox["width"] <= 380
        await ctx.close()

        await browser.close()

    REPORT["控制台无错误"] = len(console_errors) == 0
    if console_errors:
        REPORT["控制台错误明细"] = console_errors[:5]

    print("=" * 70)
    print("视觉验收报告")
    print("=" * 70)
    failed = []
    for key, value in REPORT.items():
        ok = bool(value)
        print(f"  [{'PASS' if ok else 'FAIL'}] {key}")
        if not ok and key != "控制台错误明细":
            failed.append(key)
    print("=" * 70)
    print(f"截图目录: {OUT_DIR}")
    print(f"结果: {'全部通过' if not failed else '失败项: ' + str(failed)}")
    with open(os.path.join(OUT_DIR, "visual-report.json"), "w", encoding="utf-8") as fh:
        json.dump(REPORT, fh, ensure_ascii=False, indent=2)
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    asyncio.run(run())
