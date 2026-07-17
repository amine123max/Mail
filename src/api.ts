export interface Account {
  id: number;
  email: string;
  remark: string;
  group: string;
  createdAt: string;
  updatedAt: string;
  lastSyncAt: string | null;
}

export interface MailFolder {
  path: string;
  name: string;
  specialUse: string | null;
  delimiter: string;
}

export interface MessageSummary {
  uid: number | string;
  subject: string;
  from: string;
  fromEmail: string;
  to: string;
  date: string;
  unread: boolean;
  flagged: boolean;
  preview: string;
}

export interface Announcement {
  id: number;
  title: string;
  content: string;
  author: string;
  createdAt: string;
  read: boolean;
}

export interface MessageDetail {
  uid: number | string;
  subject: string;
  from: string;
  to: string;
  cc: string;
  date: string;
  html: string;
  text: string;
  attachments: Array<{
    index: number;
    filename: string;
    contentType: string;
    size: number;
  }>;
}

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
    public readonly details?: unknown,
  ) {
    super(message);
  }
}

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
    code?: string;
    details?: unknown;
  };
  if (!response.ok) {
    throw new ApiError(
      data.error || `请求失败（${response.status}）`,
      response.status,
      data.code,
      data.details,
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
