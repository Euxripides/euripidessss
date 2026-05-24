import { getJson, postForm, postJson } from '../../api/client';

export type CurrentFilesPayload = {
  uploads?: Array<{ name: string; path: string; size: number }>;
  rule_samples?: Array<{ name: string; path: string; size: number }>;
};

export function loadCurrentFiles() {
  return getJson<CurrentFilesPayload>('/api/files/current', '读取当前文件失败');
}

export function analyzeRules(data: FormData) {
  return postForm('/api/rules/analyze', data, '规则分析失败');
}

export function confirmRuleExpansion(provider: string, rule: unknown) {
  return postJson('/api/rules/confirm', { provider, rule }, '保存规则失败');
}
