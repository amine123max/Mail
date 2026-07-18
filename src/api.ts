import { ApiError } from "@aillive/api-types";

export { ApiError } from "@aillive/api-types";
export type {
  Account,
  Announcement,
  DesktopApiErrorBody,
  DesktopCapabilities,
  MailFolder,
  MessageDetail,
  MessageSummary,
} from "@aillive/api-types";

const appBasePath = import.meta.env.BASE_URL.replace(/\/$/, "");

function resolveApiPath(path: string): string {
  return path.startsWith("/api/") ? `${appBasePath}${path}` : path;
}

export async function api<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const response = await fetch(resolveApiPath(path), {
    ...init,
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });

  if (response.status === 204) return undefined as T;
  const data = (await response.json().catch(() => ({}))) as {
    error?: string;
    message?: string;
    code?: string;
    details?: unknown;
    requestId?: string;
    retryable?: boolean;
    retryAfter?: number | null;
  };
  if (!response.ok) {
    throw new ApiError(
      data.message || data.error || `请求失败（${response.status}）`,
      response.status,
      data.code,
      data.details,
      data.requestId,
      Boolean(data.retryable),
      data.retryAfter ?? undefined,
    );
  }
  return data as T;
}

export function formatDate(value: string | null, long = false, locale = "zh-CN"): string {
  if (!value) return "尚未同步";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(locale, {
    month: "short",
    day: "numeric",
    ...(long ? { year: "numeric", hour: "2-digit", minute: "2-digit" } : {}),
  }).format(date);
}

export function initials(value: string): string {
  const name = value.split("<")[0].trim() || value;
  return name.slice(0, 2).toUpperCase();
}
