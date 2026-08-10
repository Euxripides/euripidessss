import { CheckCircleFilled, WarningFilled } from "@ant-design/icons";
import { useEffect, useMemo, useState, type CSSProperties } from "react";

const TRUST_WALLET_SMARTCHAIN = "https://raw.githubusercontent.com/trustwallet/assets/master/blockchains/smartchain";

type KnownToken = { name: string; symbol: string; logo: string };

const BSC_NATIVE: KnownToken = {
  name: "BNB",
  symbol: "BNB",
  logo: `${TRUST_WALLET_SMARTCHAIN}/info/logo.png`,
};

const KNOWN_BSC_TOKENS: Record<string, KnownToken> = {
  "0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c": {
    name: "Wrapped BNB",
    symbol: "WBNB",
    logo: BSC_NATIVE.logo,
  },
  "0x55d398326f99059ff775485246999027b3197955": {
    name: "Tether USD",
    symbol: "USDT",
    logo: `${TRUST_WALLET_SMARTCHAIN}/assets/0x55d398326f99059fF775485246999027B3197955/logo.png`,
  },
  "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d": {
    name: "USD Coin",
    symbol: "USDC",
    logo: `${TRUST_WALLET_SMARTCHAIN}/assets/0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d/logo.png`,
  },
};

export interface TokenIdentityProps {
  chainId?: number;
  address?: unknown;
  symbol?: unknown;
  name?: unknown;
  logoURI?: unknown;
  verified?: boolean;
  spam?: boolean;
}

function safeLogoURL(value: unknown): string {
  const raw = String(value || "").trim();
  if (raw.startsWith("/assets/tokens/") && !raw.includes("..")) return raw;
  try {
    const parsed = new URL(raw);
    if (parsed.protocol !== "https:") return "";
    if (parsed.hostname === "raw.githubusercontent.com" && parsed.pathname.startsWith("/trustwallet/assets/")) return parsed.href;
    if (parsed.hostname === "assets-cdn.trustwallet.com") return parsed.href;
    return "";
  } catch {
    return "";
  }
}

function stableHue(value: string): number {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return Math.abs(hash) % 360;
}

function shortContract(address: string): string {
  return /^0x[0-9a-f]{40}$/.test(address) ? `${address.slice(0, 8)}…${address.slice(-6)}` : "Native Token";
}

export function TokenIdentity({ chainId = 56, address, symbol, name, logoURI, verified, spam }: TokenIdentityProps) {
  const contract = String(address || "").trim().toLowerCase();
  const known = chainId === 56 ? (contract ? KNOWN_BSC_TOKENS[contract] : BSC_NATIVE) : undefined;
  const displaySymbol = known?.symbol || String(symbol || "Unknown").trim() || "Unknown";
  const displayName = known?.name || String(name || "Unknown Token").trim() || "Unknown Token";
  const logo = known?.logo || safeLogoURL(logoURI);
  const [failed, setFailed] = useState(false);
  const hue = useMemo(() => stableHue(`${chainId}:${contract || displaySymbol}`), [chainId, contract, displaySymbol]);

  useEffect(() => setFailed(false), [logo]);

  const fallback = (
    <span className="xi-token-fallback" style={{ "--token-hue": hue } as CSSProperties} aria-hidden="true">
      {displaySymbol.slice(0, 2).toUpperCase()}
    </span>
  );

  return (
    <span className="xi-token" title={[displayName, displaySymbol, contract].filter(Boolean).join(" · ")}>
      {logo && !failed ? (
        <img
          className="xi-token-logo"
          src={logo}
          alt={`${displaySymbol} Logo`}
          loading="lazy"
          decoding="async"
          referrerPolicy="no-referrer"
          onError={() => setFailed(true)}
        />
      ) : fallback}
      <span className="xi-token-copy">
        <b>{displaySymbol}{verified ? <CheckCircleFilled className="xi-token-verified" title="已验证 Token" /> : null}{spam ? <WarningFilled className="xi-token-spam" title="疑似垃圾 Token" /> : null}</b>
        <small>{displayName} · {shortContract(contract)}</small>
      </span>
    </span>
  );
}
