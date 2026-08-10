# -*- coding: utf-8 -*-
"""Request an OKLink CSV export through Crawl4AI + Patchright.

The Go caller sends one JSON object on stdin and expects one JSON object on
stdout.  Browser/profile diagnostics stay on stderr and request bodies are
never logged.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import re
import sys
import time
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, urlparse


OKLINK_HOSTS = {"oklink.com", "www.oklink.com"}
CSV_ENDPOINT = re.compile(
    r"^/download/explorer/v1/(?P<chain>[a-z0-9_-]+)/"
    r"(?P<kind>normalTransaction|tokenTransfer)/download/async$",
    re.IGNORECASE,
)
ADDRESS_PAGE = re.compile(
    r"^/zh-hans/(?P<chain>[a-z0-9_-]+)/address/"
    r"(?P<address>[^/?#]+?)(?:/(?P<kind>token-transfer))?/?$",
    re.IGNORECASE,
)
DEFAULT_PROFILE_NAME = "crawl4ai-patchright-profile"
MAX_INPUT_BYTES = 1 << 20
BRIDGE_TIMEOUT_SECONDS = 100
PROFILE_LOCK_TIMEOUT_SECONDS = 15


def _boolean_env(name: str, default: bool) -> bool:
    raw = os.environ.get(name)
    if raw is None:
        return default
    return raw.strip().lower() not in {"0", "false", "no", "off"}


def _profile_directory() -> Path:
    local_cache = os.environ.get("LOCALAPPDATA", "").strip()
    if not local_cache:
        local_cache = str(Path.home() / "AppData" / "Local")
    root = (Path(local_cache) / "wallet-exporter" / "browser").resolve()
    configured = os.environ.get("OKLINK_CRAWL4AI_PROFILE_DIR", "").strip()
    path = Path(configured).expanduser().resolve() if configured else root / DEFAULT_PROFILE_NAME

    if root == Path(root.anchor).resolve() or not path.is_relative_to(root):
        raise ValueError("browser profile must stay inside the configured profile root")
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    return path


class _ProfileLock:
    def __init__(self, profile: Path):
        self.path = profile.parent / f"{profile.name}.bridge.lock"
        self.fd: int | None = None

    def acquire(self, timeout: float) -> None:
        self.fd = os.open(self.path, os.O_RDWR | os.O_CREAT, 0o600)
        if os.path.getsize(self.path) == 0:
            os.write(self.fd, b"0")
        deadline = time.monotonic() + timeout
        while True:
            try:
                os.lseek(self.fd, 0, os.SEEK_SET)
                if os.name == "nt":
                    import msvcrt

                    msvcrt.locking(self.fd, msvcrt.LK_NBLCK, 1)
                else:
                    import fcntl

                    fcntl.flock(self.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                return
            except OSError:
                if time.monotonic() >= deadline:
                    self.release()
                    raise TimeoutError("another Crawl4AI browser session is using the profile")
                time.sleep(0.25)

    def release(self) -> None:
        if self.fd is None:
            return
        try:
            os.lseek(self.fd, 0, os.SEEK_SET)
            if os.name == "nt":
                import msvcrt

                msvcrt.locking(self.fd, msvcrt.LK_UNLCK, 1)
            else:
                import fcntl

                fcntl.flock(self.fd, fcntl.LOCK_UN)
        except OSError:
            pass
        finally:
            os.close(self.fd)
            self.fd = None


def _validated_request(raw: str) -> tuple[str, str, dict[str, Any]]:
    try:
        request = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError("invalid bridge request JSON") from exc
    if not isinstance(request, dict):
        raise ValueError("bridge request must be a JSON object")

    api_url = str(request.get("url", "")).strip()
    page_url = str(request.get("pageUrl", "")).strip()
    body_text = str(request.get("body", ""))
    api = urlparse(api_url)
    page = urlparse(page_url)

    if api.scheme != "https" or page.scheme != "https":
        raise ValueError("OKLink bridge requires HTTPS URLs")
    if api.username or api.password or page.username or page.password:
        raise ValueError("credentials in OKLink URLs are not allowed")
    if (api.hostname or "").lower() not in OKLINK_HOSTS:
        raise ValueError("unsupported CSV API host")
    if (page.hostname or "").lower() != (api.hostname or "").lower():
        raise ValueError("page and CSV API origins must match")
    if api.port not in (None, 443) or page.port not in (None, 443):
        raise ValueError("non-default OKLink ports are not allowed")
    endpoint_match = CSV_ENDPOINT.fullmatch(api.path)
    page_match = ADDRESS_PAGE.fullmatch(page.path)
    if not endpoint_match:
        raise ValueError("unsupported CSV API endpoint")
    if not page_match or page.query or page.fragment:
        raise ValueError("unsupported OKLink address page")
    if endpoint_match.group("chain").lower() != page_match.group("chain").lower():
        raise ValueError("page and CSV API chains must match")
    expected_page_kind = "tokenTransfer" if page_match.group("kind") else "normalTransaction"
    if endpoint_match.group("kind").lower() != expected_page_kind.lower():
        raise ValueError("page and CSV API transfer kinds must match")
    query = parse_qs(api.query, keep_blank_values=True)
    if set(query) != {"t"} or len(query["t"]) != 1 or not query["t"][0].isdigit():
        raise ValueError("CSV API query must contain one numeric timestamp")
    if api.fragment:
        raise ValueError("CSV API fragments are not allowed")

    try:
        payload = json.loads(body_text)
    except json.JSONDecodeError as exc:
        raise ValueError("invalid CSV request body JSON") from exc
    if not isinstance(payload, dict):
        raise ValueError("CSV request body must be a JSON object")
    if str(payload.get("url", "")) != page_url:
        raise ValueError("CSV request body URL must match the address page")
    if str(payload.get("address", "")) != page_match.group("address"):
        raise ValueError("CSV request address must match the address page")

    # The existing, live-validated browser request signs only the pathname.
    return page_url, api.path, payload


def _browser_expression() -> str:
    return r"""
async ({ endpoint, payload }) => {
  const resource = performance.getEntriesByType('resource')
    .map((entry) => entry.name)
    .find((name) => /\/17203-[^/]+\.js(?:\?|$)/.test(name))
    || 'https://static.oklink.com/cdn/assets/okfe/all-block-chain/assets/17203-CWFspQgO.js';
  const { generateSecToken } = await import(resource);
  const xSecToken = await generateSecToken({ method: 'POST', url: endpoint });
  const apiKey = 'a2c903cc-b31e-4547-9299-b6d07b7631ab';
  const rotated = apiKey.slice(8) + apiKey.slice(0, 8);
  const suffix = String(Math.floor(Math.random() * 1000)).padStart(3, '0');
  const xApiKey = btoa(rotated + '|' + String(Date.now() + 1111111111111) + suffix);
  try {
    const response = await window.utils.ont.post(endpoint, payload, {
      needSign: true,
      headers: {
        'Content-Type': 'application/json',
        'x-apiKey': xApiKey,
        'x-sec-token': xSecToken,
      },
    });
    return { ok: true, response };
  } catch (error) {
    return {
      ok: false,
      msg: error?.msg ?? error?.data?.msg ?? String(error),
    };
  }
}
"""


def _normalise_result(result: Any) -> dict[str, Any]:
    if not isinstance(result, dict):
        return {"code": -1, "msg": "browser request returned an invalid result"}
    if not result.get("ok"):
        return {"code": -1, "msg": str(result.get("msg") or "browser request failed")}

    response = result.get("response")
    if isinstance(response, dict) and isinstance(response.get("data"), dict):
        data = response["data"]
    elif isinstance(response, dict):
        data = response
    else:
        data = {}
    try:
        code = int(data.get("code", -1))
    except (TypeError, ValueError):
        code = -1
    message = str(data.get("detailMsg") or data.get("msg") or "")
    if code != 0 and not message:
        message = "browser response did not include a successful result code"
    return {"code": code, "msg": message}


def _harden_crawl4ai_browser_flags() -> None:
    """Remove Crawl4AI server defaults that weaken browser isolation/TLS."""
    from crawl4ai.browser_manager import ManagedBrowser
    from patchright.async_api import BrowserType

    original = ManagedBrowser.build_browser_flags
    blocked = {
        "--no-sandbox",
        "--ignore-certificate-errors",
        "--ignore-certificate-errors-spki-list",
    }

    def safe_flags(config):
        return [flag for flag in original(config) if flag not in blocked]

    ManagedBrowser.build_browser_flags = staticmethod(safe_flags)
    if not getattr(BrowserType.launch_persistent_context, "_oklink_hardened", False):
        original_launch = BrowserType.launch_persistent_context

        async def sandboxed_launch(self, user_data_dir, **kwargs):
            kwargs["chromium_sandbox"] = True
            return await original_launch(self, user_data_dir, **kwargs)

        sandboxed_launch._oklink_hardened = True
        BrowserType.launch_persistent_context = sandboxed_launch


async def _run(raw_request: str) -> dict[str, Any]:
    page_url, endpoint, payload = _validated_request(raw_request)

    profile = _profile_directory()
    profile_lock = _ProfileLock(profile)
    await asyncio.to_thread(profile_lock.acquire, PROFILE_LOCK_TIMEOUT_SECONDS)

    try:
        # Imports are intentionally delayed until after input validation.  This
        # keeps malformed requests from starting browser/runtime subprocesses.
        from crawl4ai import AsyncWebCrawler, BrowserConfig, CacheMode, CrawlerRunConfig, UndetectedAdapter
        from crawl4ai.async_crawler_strategy import AsyncPlaywrightCrawlerStrategy

        _harden_crawl4ai_browser_flags()

        browser_config = BrowserConfig(
            browser_type="chromium",
            headless=_boolean_env("OKLINK_CRAWL4AI_HEADLESS", True),
            use_persistent_context=True,
            user_data_dir=str(profile),
            viewport_width=1280,
            viewport_height=900,
            accept_downloads=False,
            ignore_https_errors=False,
            enable_stealth=False,
            verbose=False,
        )
        adapter = UndetectedAdapter()
        strategy = AsyncPlaywrightCrawlerStrategy(
            browser_config=browser_config,
            browser_adapter=adapter,
        )
        captured: dict[str, Any] = {}

        async def after_goto(page, **_: Any) -> None:
            await page.wait_for_function(
                "() => document.readyState !== 'loading' && !!window.utils?.ont",
                timeout=60_000,
            )
            captured["result"] = await page.evaluate(
                _browser_expression(),
                {"endpoint": endpoint, "payload": payload},
                isolated_context=False,
            )

        strategy.set_hook("after_goto", after_goto)
        run_config = CrawlerRunConfig(
            cache_mode=CacheMode.BYPASS,
            wait_until="domcontentloaded",
            page_timeout=60_000,
            delay_before_return_html=0,
            scan_full_page=False,
            capture_network_requests=False,
            capture_console_messages=False,
            verbose=False,
        )
        async with AsyncWebCrawler(
            crawler_strategy=strategy,
            config=browser_config,
        ) as crawler:
            result = await crawler.arun(url=page_url, config=run_config)
            if not getattr(result, "success", False) and "result" not in captured:
                error = getattr(result, "error_message", None) or "page navigation failed"
                raise RuntimeError(str(error))

        if "result" not in captured:
            raise RuntimeError("OKLink page completed without a CSV request result")
        return _normalise_result(captured["result"])
    finally:
        profile_lock.release()


def _sanitised_error(exc: Exception) -> str:
    message = str(exc).replace("\r", " ").replace("\n", " ")
    message = re.sub(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}", "<email>", message)
    message = re.sub(r"0x[a-fA-F0-9]{40}", "<evm-address>", message)
    return message[:500]


def main() -> int:
    try:
        raw_bytes = sys.stdin.buffer.read(MAX_INPUT_BYTES + 1)
        if len(raw_bytes) > MAX_INPUT_BYTES:
            raise ValueError("bridge request exceeds 1 MiB")
        raw_request = raw_bytes.decode("utf-8")
        # Third-party loggers must not corrupt or grow the one-object stdout protocol.
        with open(os.devnull, "w", encoding="utf-8") as sink, contextlib.redirect_stdout(sink):
            response = asyncio.run(asyncio.wait_for(_run(raw_request), timeout=BRIDGE_TIMEOUT_SECONDS))
    except Exception as exc:  # Keep the stdout protocol valid for every failure.
        response = {"code": -1, "msg": f"Crawl4AI bridge failed: {_sanitised_error(exc)}"}
    sys.stdout.write(json.dumps(response, ensure_ascii=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
