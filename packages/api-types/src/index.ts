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

export interface MessageAttachment {
  index: number;
  filename: string;
  contentType: string;
  size: number;
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
  attachments: MessageAttachment[];
}

export * from "./desktop-contract.generated";
export type { DesktopApiError as DesktopApiErrorBody } from "./desktop-contract.generated";

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
    public readonly details?: unknown,
    public readonly requestId?: string,
    public readonly retryable = false,
    public readonly retryAfter?: number,
  ) {
    super(message);
  }
}
