export type ApiResult<T = any> = {
  response: Response;
  payload: T;
};

export async function parseJsonResponse<T = any>(response: Response, fallbackMessage: string): Promise<T> {
  const text = await response.text();
  if (!text) return {} as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new Error(`${fallbackMessage}：后端返回内容无法解析`);
  }
}

export async function getJson<T = any>(url: string, fallbackMessage: string): Promise<ApiResult<T>> {
  const response = await fetch(url);
  const payload = await parseJsonResponse<T>(response, fallbackMessage);
  return { response, payload };
}

export async function postJson<T = any>(url: string, body: unknown, fallbackMessage: string): Promise<ApiResult<T>> {
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const payload = await parseJsonResponse<T>(response, fallbackMessage);
  return { response, payload };
}

export async function postForm<T = any>(url: string, body: FormData, fallbackMessage: string): Promise<ApiResult<T>> {
  const response = await fetch(url, { method: 'POST', body });
  const payload = await parseJsonResponse<T>(response, fallbackMessage);
  return { response, payload };
}
