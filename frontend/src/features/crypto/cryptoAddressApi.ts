export type CryptoAddressCandidate = {
  readonly chain: string;
  readonly name: string;
  readonly family: string;
  readonly confidence: number;
  readonly source: string;
  readonly status?: string;
  readonly detail?: string;
};

export type CryptoAddressItem = {
  readonly input: string;
  readonly address: string;
  readonly valid: boolean;
  readonly family: string;
  readonly kind: string;
  readonly status: string;
  readonly retry_count: number;
  readonly error: string;
  readonly network: string;
  readonly confidence: number;
  readonly reason: string;
  readonly candidates: readonly CryptoAddressCandidate[];
  readonly warnings?: readonly string[];
};

export type CryptoAddressSummary = {
  readonly total: number;
  readonly valid: number;
  readonly invalid: number;
  readonly duplicates: number;
  readonly family_counts: Record<string, number>;
  readonly chain_counts: Record<string, number>;
};

export type CryptoAddressClassifyValues = {
  readonly addresses: string;
  readonly chains?: readonly string[];
  readonly rpcNodes?: readonly string[];
  readonly includeDuplicates?: boolean;
};

export type CryptoAddressClassifyResponse = {
  readonly items: readonly CryptoAddressItem[];
  readonly summary: CryptoAddressSummary;
  readonly settings: {
    readonly provider: string;
    readonly verify_online: boolean;
    readonly base_url: string;
    readonly used_api: boolean;
  };
};

export async function classifyCryptoAddresses(values: CryptoAddressClassifyValues): Promise<CryptoAddressClassifyResponse> {
  const response = await fetch('/api/crypto/address-classify', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      addresses: values.addresses,
      chains: values.chains ?? [],
      rpc_nodes: values.rpcNodes ?? [],
      verify_online: false,
      include_duplicates: values.includeDuplicates ?? false,
    }),
  });
  const payload = await parseJSON(response);
  if (!response.ok) {
    throw new Error(readDetail(payload) || `地址区分失败（HTTP ${response.status}）`);
  }
  return parseCryptoAddressResponse(payload);
}

async function parseJSON(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return {};
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new Error('后端返回内容无法解析');
  }
}

function parseCryptoAddressResponse(value: unknown): CryptoAddressClassifyResponse {
  if (!isRecord(value) || !Array.isArray(value.items) || !isRecord(value.summary)) {
    throw new Error('地址区分响应格式不正确');
  }
  return {
    items: value.items.filter(isRecord).map(parseCryptoAddressItem),
    summary: {
      total: numberField(value.summary, 'total'),
      valid: numberField(value.summary, 'valid'),
      invalid: numberField(value.summary, 'invalid'),
      duplicates: numberField(value.summary, 'duplicates'),
      family_counts: recordNumberField(value.summary, 'family_counts'),
      chain_counts: recordNumberField(value.summary, 'chain_counts'),
    },
    settings: isRecord(value.settings)
      ? {
          provider: stringField(value.settings, 'provider'),
          verify_online: value.settings.verify_online === true,
          base_url: stringField(value.settings, 'base_url'),
          used_api: value.settings.used_api === true,
        }
      : { provider: '', verify_online: false, base_url: '', used_api: false },
  };
}

function parseCryptoAddressItem(value: Record<string, unknown>): CryptoAddressItem {
  return {
    input: stringField(value, 'input'),
    address: stringField(value, 'address'),
    valid: value.valid === true,
    family: stringField(value, 'family'),
    kind: stringField(value, 'kind'),
    status: stringField(value, 'status'),
    retry_count: numberField(value, 'retry_count'),
    error: stringField(value, 'error'),
    network: stringField(value, 'network'),
    confidence: numberField(value, 'confidence'),
    reason: stringField(value, 'reason'),
    candidates: Array.isArray(value.candidates) ? value.candidates.filter(isRecord).map(parseCandidate) : [],
    warnings: Array.isArray(value.warnings) ? value.warnings.filter((item): item is string => typeof item === 'string') : [],
  };
}

function parseCandidate(value: Record<string, unknown>): CryptoAddressCandidate {
  return {
    chain: stringField(value, 'chain'),
    name: stringField(value, 'name'),
    family: stringField(value, 'family'),
    confidence: numberField(value, 'confidence'),
    source: stringField(value, 'source'),
    status: stringField(value, 'status') || undefined,
    detail: stringField(value, 'detail') || undefined,
  };
}

function readDetail(value: unknown): string {
  return isRecord(value) && typeof value.detail === 'string' ? value.detail : '';
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function stringField(value: Record<string, unknown>, key: string): string {
  return typeof value[key] === 'string' ? value[key] : '';
}

function numberField(value: Record<string, unknown>, key: string): number {
  return typeof value[key] === 'number' ? value[key] : 0;
}

function recordNumberField(value: Record<string, unknown>, key: string): Record<string, number> {
  if (!isRecord(value[key])) return {};
  const out: Record<string, number> = {};
  for (const [name, count] of Object.entries(value[key])) {
    if (typeof count === 'number') out[name] = count;
  }
  return out;
}
