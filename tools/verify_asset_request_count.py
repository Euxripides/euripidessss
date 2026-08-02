# 验证：聚焦后 /api/flow/address-assets 请求只发生一次（无请求风暴）
import asyncio

from playwright.async_api import async_playwright

BASE = "http://127.0.0.1:8000"
ADDR = "0x000000000000000000000000000000000000dead"


async def main():
    async with async_playwright() as p:
        browser = await p.chromium.launch(channel="msedge", headless=True)
        ctx = await browser.new_context(viewport={"width": 1440, "height": 900})
        page = await ctx.new_page()
        assets_calls = []
        page.on(
            "request",
            lambda req: assets_calls.append(req.url)
            if "/api/flow/address-assets" in req.url
            else None,
        )
        await page.goto(BASE, wait_until="domcontentloaded")
        await page.locator(".ant-menu-item").filter(has_text="地址关系图").first.click()
        await page.wait_for_selector(".react-flow__node", timeout=40000)
        await page.get_by_label("地址搜索").fill(ADDR)
        await page.get_by_label("地址搜索").press("Enter")
        await page.wait_for_selector(".focus-address-node.relation-selected", timeout=15000)
        # 观察 6 秒内的资产请求次数
        await page.wait_for_timeout(6000)
        print("assets API calls in 6s:", len(assets_calls))
        await browser.close()


if __name__ == "__main__":
    asyncio.run(main())
