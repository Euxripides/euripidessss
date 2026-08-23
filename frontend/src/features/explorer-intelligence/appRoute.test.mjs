// appRoute 纯函数表驱动测试（UAT-FIX-001/002）
// 运行：node --test frontend/src/features/explorer-intelligence/appRoute.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { parseAppRoute, buildAppURL, buildTransactionURL, chainIdToAppChain, isTransactionHash, normalizeChain } from "./appRoute.ts";

const loc = (pathname, search = "") => ({ pathname, search });

test("parseAppRoute: explorer 链首页", () => {
  assert.deepEqual(parseAppRoute(loc("/explorer/bsc")), { active: "explorer", chain: "bsc", address: undefined, query: {} });
});

test("parseAppRoute: explorer 地址详情（含大小写归一）", () => {
  const r = parseAppRoute(loc("/explorer/eth/address/0x8894E0A0C962CB723C1976A4421C95949BE2D4E3"));
  assert.equal(r.active, "explorer");
  assert.equal(r.chain, "eth");
  assert.equal(r.address, "0x8894e0a0c962cb723c1976a4421c95949be2d4e3");
});

test("parseAppRoute: 非法链/非法地址不匹配", () => {
  assert.equal(parseAppRoute(loc("/explorer/bitcoin")), null);
  assert.equal(parseAppRoute(loc("/explorer/bsc/address/not-an-address")), null);
});

test("parseAppRoute: 真实交易路由并归一 hash", () => {
  const hash = `0x${"AB".repeat(32)}`;
  assert.deepEqual(parseAppRoute(loc(`/bsc/tx/${hash}`)), {
    active: "transaction",
    chain: "bsc",
    txHash: hash.toLowerCase(),
    query: {},
  });
  assert.deepEqual(parseAppRoute(loc(`/bsc/tx/0x${"1".repeat(63)}`)), {
    active: "transaction",
    chain: "bsc",
    txHash: `0x${"1".repeat(63)}`,
    query: {},
  });
  assert.equal(parseAppRoute(loc("/bsc/tx/too/many/segments")), null);
});

test("parseAppRoute: 页面路径", () => {
  assert.deepEqual(parseAppRoute(loc("/smart-download")), { active: "smart-download", query: {} });
  assert.deepEqual(parseAppRoute(loc("/fund-flow")), { active: "analytics-graph", query: {} });
  assert.deepEqual(parseAppRoute(loc("/risk")), { active: "risk", query: {} });
  assert.deepEqual(parseAppRoute(loc("/analytics-address")), { active: "analytics-address", query: {} });
});

test("parseAppRoute: 根路径与未知路径返回 null", () => {
  assert.equal(parseAppRoute(loc("/")), null);
  assert.equal(parseAppRoute(loc("/unknown-page")), null);
});

test("buildAppURL: explorer 地址详情", () => {
  assert.equal(buildAppURL({ active: "explorer", chain: "bsc", address: "0xf43ba0b50028b8873fd4d6daac4bb7c4d5523906" }), "/explorer/bsc/address/0xf43ba0b50028b8873fd4d6daac4bb7c4d5523906");
});

test("buildAppURL: 页面路径与 query", () => {
  assert.equal(buildAppURL({ active: "smart-download" }), "/smart-download");
  assert.equal(buildAppURL({ active: "explorer", chain: "bsc", query: { tab: "overview" } }), "/explorer/bsc?tab=overview");
});

test("buildAppURL: 交易路由可直接分享", () => {
  const hash = `0x${"ab".repeat(32)}`;
  assert.equal(buildAppURL({ active: "transaction", chain: "eth", txHash: hash }), `/eth/tx/${hash}`);
  assert.equal(buildTransactionURL(56, hash), `/bsc/tx/${hash}`);
  assert.equal(buildTransactionURL("42161", hash), `/arbitrum/tx/${hash}`);
});

test("交易 hash 与链映射纯函数", () => {
  assert.equal(isTransactionHash(`0x${"f".repeat(64)}`), true);
  assert.equal(isTransactionHash(`0x${"f".repeat(63)}`), false);
  assert.equal(chainIdToAppChain(1), "eth");
  assert.equal(chainIdToAppChain(8453), "base");
  assert.equal(chainIdToAppChain(undefined), "bsc");
});

test("parse/build 互为可逆（explorer 地址）", () => {
  const url = "/explorer/bsc/address/0xf43ba0b50028b8873fd4d6daac4bb7c4d5523906?range=7d&tab=overview";
  const route = parseAppRoute(loc(url.split("?")[0], "?" + url.split("?")[1]));
  assert.equal(buildAppURL(route), url);
});

test("parse/build 互为可逆（smart-download）", () => {
  const route = parseAppRoute(loc("/smart-download"));
  assert.equal(buildAppURL(route), "/smart-download");
});

test("normalizeChain: 合法/非法回退", () => {
  assert.equal(normalizeChain("eth"), "eth");
  assert.equal(normalizeChain("bitcoin"), "bsc");
  assert.equal(normalizeChain(undefined), "bsc");
  assert.equal(normalizeChain("base", "eth"), "base");
});
