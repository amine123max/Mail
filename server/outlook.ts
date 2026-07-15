import { ImapFlow, type FetchMessageObject } from "imapflow";
import { simpleParser } from "mailparser";
import nodemailer from "nodemailer";
import sanitizeHtml from "sanitize-html";
import {
  markAccountSynced,
  updateRefreshToken,
} from "./database";
import type { AccountCredentials } from "./types";

const imapHosts = Array.from(
  new Set([
    process.env.OUTLOOK_IMAP_HOST || "outlook.office365.com",
    "outlook.live.com",
  ]),
);
const smtpHosts = Array.from(
  new Set([
    process.env.OUTLOOK_SMTP_HOST || "smtp-mail.outlook.com",
    "smtp.office365.com",
  ]),
);

export class MailServiceError extends Error {
  constructor(
    message: string,
    public readonly code = "MAIL_SERVICE_ERROR",
    public readonly status = 502,
  ) {
    super(message);
  }
}

interface TokenResponse {
  access_token?: string;
  refresh_token?: string;
  scope?: string;
  error?: string;
  error_description?: string;
}

export async function refreshAccessToken(
  account: AccountCredentials,
  requestedScope?: string,
): Promise<{ accessToken: string; scope: string }> {
  const tokenRequest: Record<string, string> = {
    client_id: account.clientId,
    grant_type: "refresh_token",
    refresh_token: account.refreshToken,
  };
  if (requestedScope) tokenRequest.scope = requestedScope;
  const response = await fetch(
    "https://login.microsoftonline.com/consumers/oauth2/v2.0/token",
    {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(tokenRequest),
      signal: AbortSignal.timeout(20_000),
    },
  );
  const data = (await response.json()) as TokenResponse;

  if (!response.ok || !data.access_token) {
    const detail = data.error_description || data.error || `HTTP ${response.status}`;
    throw new MailServiceError(
      `微软令牌刷新失败：${detail}`,
      "TOKEN_REFRESH_FAILED",
      401,
    );
  }

  if (data.refresh_token && data.refresh_token !== account.refreshToken) {
    updateRefreshToken(account.ownerKey, account.id, data.refresh_token);
    account.refreshToken = data.refresh_token;
  }

  return { accessToken: data.access_token, scope: data.scope || "" };
}

const deviceScopes = [
  "https://outlook.office.com/IMAP.AccessAsUser.All",
  "https://outlook.office.com/SMTP.Send",
  "https://graph.microsoft.com/Mail.Send",
  "offline_access",
].join(" ");
const imapScope = "https://outlook.office.com/IMAP.AccessAsUser.All offline_access";
const smtpScope = "https://outlook.office.com/SMTP.Send offline_access";

export async function requestDeviceCode(clientId: string) {
  const response = await fetch(
    "https://login.microsoftonline.com/consumers/oauth2/v2.0/devicecode",
    {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ client_id: clientId, scope: deviceScopes }),
      signal: AbortSignal.timeout(20_000),
    },
  );
  const data = (await response.json()) as Record<string, unknown>;
  if (!response.ok || !data.device_code) {
    throw new MailServiceError(
      `获取微软设备码失败：${String(data.error_description || data.error || response.status)}`,
      "DEVICE_CODE_FAILED",
      400,
    );
  }
  return data;
}

export async function pollDeviceCode(clientId: string, deviceCode: string) {
  const response = await fetch(
    "https://login.microsoftonline.com/consumers/oauth2/v2.0/token",
    {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        client_id: clientId,
        grant_type: "urn:ietf:params:oauth:grant-type:device_code",
        device_code: deviceCode,
      }),
      signal: AbortSignal.timeout(20_000),
    },
  );
  const data = (await response.json()) as TokenResponse;
  if (data.error === "authorization_pending" || data.error === "slow_down") {
    return { pending: true as const, slowDown: data.error === "slow_down" };
  }
  if (!response.ok || !data.refresh_token) {
    throw new MailServiceError(
      `微软授权失败：${data.error_description || data.error || response.status}`,
      "DEVICE_AUTH_FAILED",
      400,
    );
  }
  return {
    pending: false as const,
    refreshToken: data.refresh_token,
    scope: data.scope || "",
  };
}

async function connectImap(
  account: AccountCredentials,
  accessToken: string,
): Promise<ImapFlow> {
  let lastError: unknown;
  for (const host of imapHosts) {
    const client = new ImapFlow({
      host,
      port: 993,
      secure: true,
      auth: { user: account.email, accessToken },
      logger: false,
      socketTimeout: 30_000,
      greetingTimeout: 15_000,
    });
    try {
      await client.connect();
      return client;
    } catch (error) {
      lastError = error;
      try {
        await client.logout();
      } catch {
        // Connection may not have reached an authenticated state.
      }
    }
  }

  throw new MailServiceError(
    `无法连接 Outlook IMAP：${errorMessage(lastError)}`,
    "IMAP_CONNECTION_FAILED",
  );
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error || "未知错误");
}

function formatAddress(address: unknown): string {
  if (!address || typeof address !== "object") return "";
  const value = address as { name?: string; address?: string };
  if (value.name && value.address) return `${value.name} <${value.address}>`;
  return value.address || value.name || "";
}

function isoDate(value: string | Date | undefined): string {
  const date = value instanceof Date ? value : value ? new Date(value) : new Date();
  return date.toISOString();
}

function parsedAddressText(value: unknown): string {
  if (Array.isArray(value)) {
    return value.map(parsedAddressText).filter(Boolean).join(", ");
  }
  if (value && typeof value === "object" && "text" in value) {
    return String((value as { text?: string }).text || "");
  }
  return "";
}

function messageSummary(message: FetchMessageObject) {
  const envelope = message.envelope;
  return {
    uid: message.uid,
    subject: envelope?.subject || "（无主题）",
    from: formatAddress(envelope?.from?.[0]),
    to: envelope?.to?.map(formatAddress).filter(Boolean).join(", ") || "",
    date: isoDate(message.internalDate || envelope?.date),
    unread: !message.flags?.has("\\Seen"),
    flagged: Boolean(message.flags?.has("\\Flagged")),
  };
}

export async function testAccount(account: AccountCredentials) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  const client = await connectImap(account, accessToken);
  try {
    const folders = await client.list();
    let canSend = false;
    let scope = "";
    try {
      const smtpToken = await refreshAccessToken(account, smtpScope);
      scope = smtpToken.scope;
      canSend = smtpToken.scope.includes("SMTP.Send");
    } catch {
      canSend = false;
    }
    markAccountSynced(account.ownerKey, account.id);
    return {
      folders: folders.length,
      canSend,
      scope,
    };
  } finally {
    await client.logout();
  }
}

export async function listFolders(account: AccountCredentials) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  const client = await connectImap(account, accessToken);
  try {
    const folders = await client.list();
    markAccountSynced(account.ownerKey, account.id);
    return folders.map((folder) => ({
      path: folder.path,
      name: folder.name,
      specialUse: folder.specialUse || null,
      delimiter: folder.delimiter,
    }));
  } finally {
    await client.logout();
  }
}

export async function listMessages(
  account: AccountCredentials,
  options: { folder: string; page: number; pageSize: number; query?: string },
) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  const client = await connectImap(account, accessToken);
  let lock: Awaited<ReturnType<ImapFlow["getMailboxLock"]>> | undefined;
  try {
    lock = await client.getMailboxLock(options.folder);
    const total = client.mailbox ? client.mailbox.exists : 0;
    if (!total) return { messages: [], total: 0, page: options.page };

    let range = "";
    let totalMatches = total;
    let useUid = false;

    if (options.query?.trim()) {
      const query = options.query.trim();
      const foundResult = await client.search(
        { or: [{ subject: query }, { from: query }, { body: query }] },
        { uid: true },
      );
      const found = foundResult || [];
      found.sort((a, b) => b - a);
      totalMatches = found.length;
      const selected = found.slice(
        (options.page - 1) * options.pageSize,
        options.page * options.pageSize,
      );
      if (!selected.length) {
        return { messages: [], total: totalMatches, page: options.page };
      }
      range = selected.join(",");
      useUid = true;
    } else {
      const rawEnd = total - (options.page - 1) * options.pageSize;
      if (rawEnd < 1) {
        return { messages: [], total, page: options.page };
      }
      const end = rawEnd;
      const start = Math.max(1, end - options.pageSize + 1);
      range = `${start}:${end}`;
    }

    const messages = [];
    for await (const message of client.fetch(
      range,
      { uid: true, envelope: true, flags: true, internalDate: true },
      { uid: useUid },
    )) {
      messages.push(messageSummary(message));
    }
    messages.sort((a, b) => b.uid - a.uid);
    markAccountSynced(account.ownerKey, account.id);
    return { messages, total: totalMatches, page: options.page };
  } finally {
    lock?.release();
    await client.logout();
  }
}

export async function getMessage(
  account: AccountCredentials,
  folder: string,
  uid: number,
) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  const client = await connectImap(account, accessToken);
  let lock: Awaited<ReturnType<ImapFlow["getMailboxLock"]>> | undefined;
  try {
    lock = await client.getMailboxLock(folder);
    const message = await client.fetchOne(
      uid,
      { source: true, flags: true, envelope: true, internalDate: true },
      { uid: true },
    );
    if (!message || !message.source) {
      throw new MailServiceError("邮件不存在或已被删除", "MESSAGE_NOT_FOUND", 404);
    }

    const parsed = await simpleParser(Buffer.from(message.source));
    await client.messageFlagsAdd(uid, ["\\Seen"], { uid: true });

    const htmlSource = typeof parsed.html === "string" ? parsed.html : "";
    const safeHtml = htmlSource
      ? sanitizeHtml(htmlSource, {
          allowedTags: sanitizeHtml.defaults.allowedTags.concat([
            "img",
            "style",
            "table",
            "tbody",
            "thead",
            "tfoot",
            "tr",
            "td",
            "th",
          ]),
          allowedAttributes: {
            "*": ["style", "class", "title", "dir", "align"],
            a: ["href", "name", "target", "rel"],
            img: ["src", "alt", "width", "height"],
            td: ["colspan", "rowspan", "width", "height"],
            th: ["colspan", "rowspan", "width", "height"],
          },
          allowedSchemes: ["http", "https", "mailto", "cid", "data"],
          transformTags: {
            a: sanitizeHtml.simpleTransform("a", {
              target: "_blank",
              rel: "noopener noreferrer",
            }),
            img: (tagName, attributes) => {
              const source = attributes.src || "";
              if (/^https?:/i.test(source) || source.startsWith("//")) {
                return {
                  tagName: "span",
                  attribs: { class: "mail-remote-image-blocked" },
                  text: attributes.alt ? `[${attributes.alt}]` : "[远程图片已阻止]",
                };
              }
              return { tagName, attribs: attributes };
            },
          },
        })
      : "";

    return {
      uid,
      subject: parsed.subject || "（无主题）",
      from: parsed.from?.text || formatAddress(message.envelope?.from?.[0]),
      to: parsedAddressText(parsed.to),
      cc: parsedAddressText(parsed.cc),
      date: isoDate(parsed.date || message.internalDate),
      html: safeHtml,
      text: parsed.text || "",
      attachments: parsed.attachments.map((attachment, index) => ({
        index,
        filename: attachment.filename || `附件-${index + 1}`,
        contentType: attachment.contentType,
        size: attachment.size,
      })),
    };
  } finally {
    lock?.release();
    await client.logout();
  }
}

export async function sendMessage(
  account: AccountCredentials,
  message: {
    to: string;
    cc?: string;
    bcc?: string;
    subject: string;
    text: string;
    html?: string;
  },
) {
  let accessToken = "";
  let scope = "";
  let lastError: unknown;
  try {
    const smtpToken = await refreshAccessToken(account, smtpScope);
    accessToken = smtpToken.accessToken;
    scope = smtpToken.scope;
  } catch (error) {
    lastError = error;
  }

  for (const host of !accessToken || (scope && !scope.includes("SMTP.Send")) ? [] : smtpHosts) {
    const transport = nodemailer.createTransport({
      host,
      port: 587,
      secure: false,
      requireTLS: true,
      auth: {
        type: "OAuth2",
        user: account.email,
        accessToken,
      },
      connectionTimeout: 20_000,
      greetingTimeout: 15_000,
      socketTimeout: 30_000,
    });
    try {
      const result = await transport.sendMail({
        from: account.email,
        to: message.to,
        cc: message.cc || undefined,
        bcc: message.bcc || undefined,
        subject: message.subject,
        text: message.text,
        html: message.html || undefined,
      });
      return { messageId: result.messageId, accepted: result.accepted };
    } catch (error) {
      lastError = error;
      transport.close();
    }
  }

  const smtpError = errorMessage(lastError);
  try {
    const graphToken = await refreshAccessToken(
      account,
      "https://graph.microsoft.com/Mail.Send offline_access",
    );
    const graphResponse = await fetch("https://graph.microsoft.com/v1.0/me/sendMail", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${graphToken.accessToken}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        message: {
          subject: message.subject,
          body: {
            contentType: message.html ? "HTML" : "Text",
            content: message.html || message.text,
          },
          toRecipients: parseRecipients(message.to),
          ccRecipients: parseRecipients(message.cc || ""),
          bccRecipients: parseRecipients(message.bcc || ""),
        },
        saveToSentItems: true,
      }),
      signal: AbortSignal.timeout(30_000),
    });
    if (graphResponse.ok) {
      return { messageId: "graph-accepted", accepted: parseRecipients(message.to).map((item) => item.emailAddress.address), transport: "graph" };
    }
    const graphError = await graphResponse.text();
    throw new Error(`Graph HTTP ${graphResponse.status}: ${graphError}`);
  } catch (graphFailure) {
    throw new MailServiceError(
      `邮件发送失败。SMTP：${smtpError}；Graph 回退：${errorMessage(graphFailure)}。请使用授权工具重新授予 Mail.Send，或在微软邮箱设置中启用 SMTP AUTH。`,
      "MAIL_SEND_FAILED",
    );
  }
}

function parseRecipients(value: string) {
  return value
    .split(/[;,]/)
    .map((address) => address.trim())
    .filter(Boolean)
    .map((address) => ({ emailAddress: { address } }));
}
