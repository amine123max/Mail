import { ImapFlow, type FetchMessageObject } from "imapflow";
import { lookup } from "node:dns/promises";
import { isIP } from "node:net";
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
  "https://outlook.office.com/Mail.ReadWrite",
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
    `无法连接 Outlook IMAP：${mailErrorMessage(lastError)}`,
    "IMAP_CONNECTION_FAILED",
  );
}

export function mailErrorMessage(error: unknown): string {
  if (error && typeof error === "object") {
    const value = error as { responseText?: unknown; response?: unknown; message?: unknown };
    return String(value.responseText || value.response || value.message || "未知错误");
  }
  return String(error || "未知错误");
}

function formatAddress(address: unknown): string {
  if (!address || typeof address !== "object") return "";
  const value = address as { name?: string; address?: string };
  if (value.name && value.address) return `${value.name} <${value.address}>`;
  return value.address || value.name || "";
}

function addressEmail(address: unknown): string {
  if (!address || typeof address !== "object") return "";
  return String((address as { address?: string }).address || "");
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

async function sourcePreview(source?: Buffer): Promise<string> {
  if (!source?.length) return "";
  try {
    const parsed = await simpleParser(source);
    const html = typeof parsed.html === "string"
      ? sanitizeHtml(parsed.html, { allowedTags: [], allowedAttributes: {} })
      : "";
    return (parsed.text || html)
      .replace(/\s+/g, " ")
      .trim()
      .slice(0, 220);
  } catch {
    return "";
  }
}

async function messageSummary(message: FetchMessageObject) {
  const envelope = message.envelope;
  return {
    uid: message.uid,
    subject: envelope?.subject || "（无主题）",
    from: formatAddress(envelope?.from?.[0]),
    fromEmail: addressEmail(envelope?.from?.[0]),
    to: envelope?.to?.map(formatAddress).filter(Boolean).join(", ") || "",
    date: isoDate(message.internalDate || envelope?.date),
    unread: !message.flags?.has("\\Seen"),
    flagged: Boolean(message.flags?.has("\\Flagged")),
    preview: await sourcePreview(message.source),
  };
}

interface OutlookRestAddress {
  Name?: string;
  Address?: string;
}

interface OutlookRestRecipient {
  EmailAddress?: OutlookRestAddress;
}

interface OutlookRestMessage {
  Id: string;
  Subject?: string;
  Sender?: OutlookRestRecipient;
  ToRecipients?: OutlookRestRecipient[];
  CcRecipients?: OutlookRestRecipient[];
  ReceivedDateTime?: string;
  IsRead?: boolean;
  HasAttachments?: boolean;
  BodyPreview?: string;
  Body?: { ContentType?: string; Content?: string };
  Flag?: { FlagStatus?: string };
}

interface OutlookRestCollection<T> {
  value?: T[];
  "@odata.count"?: number;
  "@odata.nextLink"?: string;
}

interface OutlookRestAttachment {
  Name?: string;
  ContentType?: string;
  Size?: number;
  IsInline?: boolean;
  ContentId?: string;
  ContentBytes?: string;
}

const outlookRestFolders = [
  { path: "rest:inbox", name: "Inbox", specialUse: "\\Inbox", delimiter: "/" },
  { path: "rest:sentitems", name: "Sent", specialUse: "\\Sent", delimiter: "/" },
  { path: "rest:drafts", name: "Drafts", specialUse: "\\Drafts", delimiter: "/" },
  { path: "rest:archive", name: "Archive", specialUse: "\\Archive", delimiter: "/" },
  { path: "rest:deleteditems", name: "Deleted", specialUse: "\\Trash", delimiter: "/" },
];

function canUseOutlookRestFallback(error: unknown): boolean {
  return error instanceof MailServiceError && error.code === "IMAP_CONNECTION_FAILED";
}

function outlookRestFolder(folder: string): string {
  if (folder.startsWith("rest:")) return folder.slice(5);
  const normalized = folder.toLowerCase();
  if (normalized === "inbox") return "inbox";
  if (normalized.includes("sent")) return "sentitems";
  if (normalized.includes("draft")) return "drafts";
  if (normalized.includes("archive")) return "archive";
  if (normalized.includes("deleted") || normalized.includes("trash")) return "deleteditems";
  return folder;
}

function outlookRestAddress(value?: OutlookRestAddress): string {
  if (!value) return "";
  if (value.Name && value.Address) return `${value.Name} <${value.Address}>`;
  return value.Address || value.Name || "";
}

async function outlookRestRequest<T>(
  accessToken: string,
  resource: string,
  init?: { method?: string; body?: unknown },
): Promise<T> {
  const response = await fetch(`https://outlook.office.com/api/v2.0/me/${resource}`, {
    method: init?.method || "GET",
    headers: {
      Authorization: `Bearer ${accessToken}`,
      Accept: "application/json",
      Prefer: 'outlook.body-content-type="html"',
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
    },
    body: init?.body ? JSON.stringify(init.body) : undefined,
    signal: AbortSignal.timeout(30_000),
  });
  const data = (await response.json().catch(() => ({}))) as T & {
    error?: { code?: string; message?: string };
  };
  if (!response.ok) {
    throw new MailServiceError(
      `Outlook 收件回退失败：${data.error?.message || data.error?.code || `HTTP ${response.status}`}`,
      "OUTLOOK_REST_FAILED",
      response.status === 401 || response.status === 403 ? 401 : 502,
    );
  }
  return data;
}

function isPrivateNetworkAddress(addressInput: string): boolean {
  const address = addressInput.toLowerCase().split("%")[0];
  if (address.startsWith("::ffff:")) return isPrivateNetworkAddress(address.slice(7));
  if (isIP(address) === 4) {
    const parts = address.split(".").map(Number);
    const [a, b] = parts;
    return a === 0 || a === 10 || a === 127 ||
      (a === 100 && b >= 64 && b <= 127) ||
      (a === 169 && b === 254) ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 0) ||
      (a === 192 && b === 168) ||
      (a === 198 && (b === 18 || b === 19)) ||
      a >= 224;
  }
  if (isIP(address) === 6) {
    return address === "::" || address === "::1" ||
      address.startsWith("fc") || address.startsWith("fd") ||
      /^fe[89ab]/.test(address) || address.startsWith("ff");
  }
  return true;
}

export async function validateRemoteImageUrl(value: string): Promise<URL> {
  const url = new URL(value);
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) {
    throw new Error("Unsupported remote image URL");
  }
  if (url.port && !["80", "443"].includes(url.port)) {
    throw new Error("Unsupported remote image port");
  }
  const hostname = url.hostname.toLowerCase();
  if (hostname === "localhost" || hostname.endsWith(".local") || hostname.endsWith(".internal")) {
    throw new Error("Private remote image host");
  }
  const addresses = isIP(hostname)
    ? [{ address: hostname }]
    : await lookup(hostname, { all: true, verbatim: true });
  if (!addresses.length || addresses.some((item) => isPrivateNetworkAddress(item.address))) {
    throw new Error("Private remote image address");
  }
  return url;
}

async function fetchRemoteImageData(source: string): Promise<string | null> {
  let current = source;
  for (let redirects = 0; redirects <= 3; redirects += 1) {
    const url = await validateRemoteImageUrl(current);
    const response = await fetch(url, {
      redirect: "manual",
      headers: {
        Accept: "image/avif,image/webp,image/png,image/jpeg,image/gif,image/svg+xml",
        "User-Agent": "MailImageFetcher/1.0",
      },
      signal: AbortSignal.timeout(10_000),
    });
    if (response.status >= 300 && response.status < 400) {
      const location = response.headers.get("location");
      if (!location || redirects === 3) return null;
      current = new URL(location, url).toString();
      continue;
    }
    if (!response.ok) return null;
    const contentType = (response.headers.get("content-type") || "").split(";")[0].trim().toLowerCase();
    if (!contentType.startsWith("image/")) return null;
    const declaredSize = Number(response.headers.get("content-length") || 0);
    if (declaredSize > 2_000_000) return null;
    if (!response.body) return null;
    const reader = response.body.getReader();
    const chunks: Buffer[] = [];
    let totalSize = 0;
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      totalSize += value.byteLength;
      if (totalSize > 2_000_000) {
        await reader.cancel();
        return null;
      }
      chunks.push(Buffer.from(value));
    }
    const bytes = Buffer.concat(chunks, totalSize);
    if (!bytes.length) return null;
    return `data:${contentType};base64,${bytes.toString("base64")}`;
  }
  return null;
}

function normalizeContentId(value: string): string {
  return value.trim().replace(/^cid:/i, "").replace(/^<|>$/g, "").toLowerCase();
}

function normalizeRemoteImageSource(value: string): string | null {
  const decoded = value
    .trim()
    .replaceAll("&amp;", "&")
    .replaceAll("&#38;", "&")
    .replaceAll("&#x26;", "&");
  if (decoded.startsWith("//")) return `https:${decoded}`;
  return /^https?:\/\//i.test(decoded) ? decoded : null;
}

function remoteImageSources(htmlSource: string): string[] {
  const sources: string[] = [];
  const imagePattern = /<img\b[^>]*\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s"'=<>`]+))/gi;
  for (const match of htmlSource.matchAll(imagePattern)) {
    const source = normalizeRemoteImageSource(match[1] || match[2] || match[3] || "");
    if (source && source.length <= 2_048 && !sources.includes(source)) sources.push(source);
    if (sources.length === 12) break;
  }
  return sources;
}

export async function renderMessageHtml(
  htmlSource: string,
  inlineImages: Map<string, string> = new Map(),
): Promise<string> {
  if (!htmlSource) return "";
  const remoteSources = remoteImageSources(htmlSource);
  const remoteImages = new Map<string, string>();
  await Promise.all(remoteSources.map(async (source) => {
    try {
      const data = await fetchRemoteImageData(source);
      if (data) remoteImages.set(source, data);
    } catch {
      // Invalid, private-network, oversized, or unreachable images stay blocked.
    }
  }));

  return sanitizeHtml(htmlSource, {
    // Message HTML is rendered inside a sandboxed, script-free iframe with a restrictive CSP.
    allowVulnerableTags: true,
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
    allowedSchemes: ["mailto", "data"],
    transformTags: {
      a: sanitizeHtml.simpleTransform("a", {
        target: "_blank",
        rel: "noopener noreferrer",
      }),
      img: (tagName, attributes) => {
        const source = (attributes.src || "").replaceAll("&amp;", "&");
        const remoteSource = normalizeRemoteImageSource(source);
        const embedded = source.toLowerCase().startsWith("cid:")
          ? inlineImages.get(normalizeContentId(source))
          : remoteSource ? remoteImages.get(remoteSource) : undefined;
        if (embedded) return { tagName, attribs: { ...attributes, src: embedded } };
        if (/^data:image\/(?:avif|gif|jpeg|jpg|png|svg\+xml|webp);base64,/i.test(source)) {
          return { tagName, attribs: attributes };
        }
        return {
          tagName: "span",
          attribs: { class: "mail-image-unavailable" },
          text: attributes.alt ? `[${attributes.alt}]` : "[图片无法安全加载]",
        };
      },
    },
  });
}

async function listOutlookRestMessages(
  account: AccountCredentials,
  accessToken: string,
  options: { folder: string; page: number; pageSize: number; query?: string },
) {
  const skip = (options.page - 1) * options.pageSize;
  const parameters = new URLSearchParams({
    $top: String(options.pageSize),
    $skip: String(skip),
    $count: "true",
    $orderby: "ReceivedDateTime desc",
    $select: "Id,Subject,Sender,ToRecipients,ReceivedDateTime,IsRead,Flag,BodyPreview",
  });
  if (options.query?.trim()) {
    const query = options.query.trim().replaceAll("'", "''");
    parameters.set("$filter", `contains(Subject,'${query}') or contains(BodyPreview,'${query}')`);
  }
  const folder = encodeURIComponent(outlookRestFolder(options.folder));
  const data = await outlookRestRequest<OutlookRestCollection<OutlookRestMessage>>(
    accessToken,
    `MailFolders/${folder}/messages?${parameters}`,
  );
  const messages = (data.value || []).map((message) => ({
    uid: `rest:${message.Id}`,
    subject: message.Subject || "（无主题）",
    from: outlookRestAddress(message.Sender?.EmailAddress),
    fromEmail: message.Sender?.EmailAddress?.Address || "",
    to: (message.ToRecipients || []).map((item) => outlookRestAddress(item.EmailAddress)).filter(Boolean).join(", "),
    date: isoDate(message.ReceivedDateTime),
    unread: !message.IsRead,
    flagged: message.Flag?.FlagStatus === "Flagged",
    preview: (message.BodyPreview || "").replace(/\s+/g, " ").trim().slice(0, 220),
  }));
  markAccountSynced(account.ownerKey, account.id);
  return {
    messages,
    total: data["@odata.count"] ?? skip + messages.length + (data["@odata.nextLink"] ? 1 : 0),
    page: options.page,
    transport: "outlook-rest" as const,
  };
}

async function getOutlookRestMessage(
  account: AccountCredentials,
  accessToken: string,
  uid: string,
) {
  const messageId = uid.startsWith("rest:") ? uid.slice(5) : uid;
  const fields = "Id,Subject,Sender,ToRecipients,CcRecipients,ReceivedDateTime,Body,IsRead,HasAttachments";
  const message = await outlookRestRequest<OutlookRestMessage>(
    accessToken,
    `messages/${encodeURIComponent(messageId)}?$select=${fields}`,
  );
  const body = message.Body?.Content || "";
  let attachments: Array<{ index: number; filename: string; contentType: string; size: number }> = [];
  const inlineImages = new Map<string, string>();
  if (message.HasAttachments || /\bcid:/i.test(body)) {
    const result = await outlookRestRequest<OutlookRestCollection<OutlookRestAttachment>>(
      accessToken,
      `messages/${encodeURIComponent(messageId)}/Attachments?$select=Name,ContentType,Size,IsInline,ContentId,ContentBytes`,
    );
    const values = result.value || [];
    for (const attachment of values) {
      if (
        attachment.IsInline &&
        attachment.ContentId &&
        attachment.ContentBytes &&
        attachment.ContentType?.startsWith("image/") &&
        Number(attachment.Size || 0) <= 2_000_000 &&
        Buffer.byteLength(attachment.ContentBytes, "base64") <= 2_000_000
      ) {
        inlineImages.set(
          normalizeContentId(attachment.ContentId),
          `data:${attachment.ContentType};base64,${attachment.ContentBytes}`,
        );
      }
    }
    attachments = values.filter((attachment) => !attachment.IsInline).map((attachment, index) => ({
      index,
      filename: attachment.Name || `附件-${index + 1}`,
      contentType: attachment.ContentType || "application/octet-stream",
      size: Number(attachment.Size || 0),
    }));
  }
  if (!message.IsRead) {
    await outlookRestRequest(accessToken, `messages/${encodeURIComponent(messageId)}`, {
      method: "PATCH",
      body: { IsRead: true },
    }).catch(() => undefined);
  }
  const isHtml = message.Body?.ContentType?.toLowerCase() === "html";
  markAccountSynced(account.ownerKey, account.id);
  return {
    uid: `rest:${message.Id}`,
    subject: message.Subject || "（无主题）",
    from: outlookRestAddress(message.Sender?.EmailAddress),
    to: (message.ToRecipients || []).map((item) => outlookRestAddress(item.EmailAddress)).filter(Boolean).join(", "),
    cc: (message.CcRecipients || []).map((item) => outlookRestAddress(item.EmailAddress)).filter(Boolean).join(", "),
    date: isoDate(message.ReceivedDateTime),
    html: isHtml ? await renderMessageHtml(body, inlineImages) : "",
    text: isHtml ? sanitizeHtml(body, { allowedTags: [], allowedAttributes: {} }) : body,
    attachments,
  };
}

export async function testAccount(account: AccountCredentials) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  let folders = 0;
  let receiveTransport: "imap" | "outlook-rest" = "imap";
  try {
    const client = await connectImap(account, accessToken);
    try {
      folders = (await client.list()).length;
    } finally {
      await client.logout();
    }
  } catch (error) {
    if (!canUseOutlookRestFallback(error)) throw error;
    await outlookRestRequest(accessToken, "MailFolders/inbox?$select=Id");
    folders = outlookRestFolders.length;
    receiveTransport = "outlook-rest";
  }
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
  return { folders, canSend, scope, receiveTransport };
}

export async function listFolders(account: AccountCredentials) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  try {
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
  } catch (error) {
    if (!canUseOutlookRestFallback(error)) throw error;
    await outlookRestRequest(accessToken, "MailFolders/inbox?$select=Id");
    markAccountSynced(account.ownerKey, account.id);
    return outlookRestFolders;
  }
}

export async function listMessages(
  account: AccountCredentials,
  options: { folder: string; page: number; pageSize: number; query?: string },
) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  let client: ImapFlow;
  try {
    client = await connectImap(account, accessToken);
  } catch (error) {
    if (!canUseOutlookRestFallback(error)) throw error;
    return listOutlookRestMessages(account, accessToken, options);
  }
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
      { uid: true, envelope: true, flags: true, internalDate: true, source: { start: 0, maxLength: 20_000 } },
      { uid: useUid },
    )) {
      messages.push(await messageSummary(message));
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
  uid: string,
) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  const numericUid = Number(uid);
  if (folder.startsWith("rest:") || uid.startsWith("rest:") || !Number.isInteger(numericUid) || numericUid < 1) {
    return getOutlookRestMessage(account, accessToken, uid);
  }
  let client: ImapFlow;
  try {
    client = await connectImap(account, accessToken);
  } catch (error) {
    if (canUseOutlookRestFallback(error)) {
      throw new MailServiceError(
        "此邮箱的 IMAP 当前不可用，请返回邮件列表后重新同步以启用 Outlook 收件回退",
        "IMAP_RELOAD_REQUIRED",
        409,
      );
    }
    throw error;
  }
  let lock: Awaited<ReturnType<ImapFlow["getMailboxLock"]>> | undefined;
  try {
    lock = await client.getMailboxLock(folder);
    const message = await client.fetchOne(
      numericUid,
      { source: true, flags: true, envelope: true, internalDate: true },
      { uid: true },
    );
    if (!message || !message.source) {
      throw new MailServiceError("邮件不存在或已被删除", "MESSAGE_NOT_FOUND", 404);
    }

    const parsed = await simpleParser(Buffer.from(message.source));
    await client.messageFlagsAdd(numericUid, ["\\Seen"], { uid: true });

    const htmlSource = typeof parsed.html === "string" ? parsed.html : "";
    const inlineImages = new Map<string, string>();
    for (const attachment of parsed.attachments) {
      if (
        attachment.contentId &&
        attachment.contentType.startsWith("image/") &&
        attachment.content.length <= 2_000_000
      ) {
        inlineImages.set(
          normalizeContentId(attachment.contentId),
          `data:${attachment.contentType};base64,${attachment.content.toString("base64")}`,
        );
      }
    }
    const safeHtml = await renderMessageHtml(htmlSource, inlineImages);

    return {
      uid: numericUid,
      subject: parsed.subject || "（无主题）",
      from: parsed.from?.text || formatAddress(message.envelope?.from?.[0]),
      to: parsedAddressText(parsed.to),
      cc: parsedAddressText(parsed.cc),
      date: isoDate(parsed.date || message.internalDate),
      html: safeHtml,
      text: parsed.text || "",
      attachments: parsed.attachments.filter((attachment) => !attachment.contentId).map((attachment, index) => ({
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

export async function moveMessage(
  account: AccountCredentials,
  folder: string,
  uid: string,
  targetFolder: string,
) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  const numericUid = Number(uid);
  if (folder.startsWith("rest:") || uid.startsWith("rest:") || !Number.isInteger(numericUid) || numericUid < 1) {
    const messageId = uid.startsWith("rest:") ? uid.slice(5) : uid;
    if (outlookRestFolder(folder) === outlookRestFolder(targetFolder)) {
      await outlookRestRequest(accessToken, `messages/${encodeURIComponent(messageId)}`, { method: "DELETE" });
    } else {
      await outlookRestRequest(accessToken, `messages/${encodeURIComponent(messageId)}/move`, {
        method: "POST",
        body: { DestinationId: outlookRestFolder(targetFolder) },
      });
    }
    markAccountSynced(account.ownerKey, account.id);
    return;
  }

  const client = await connectImap(account, accessToken);
  let lock: Awaited<ReturnType<ImapFlow["getMailboxLock"]>> | undefined;
  try {
    lock = await client.getMailboxLock(folder);
    if (folder.toLowerCase() === targetFolder.toLowerCase()) {
      await client.messageDelete(numericUid, { uid: true });
    } else {
      await client.messageMove(numericUid, targetFolder, { uid: true });
    }
    markAccountSynced(account.ownerKey, account.id);
  } finally {
    lock?.release();
    await client.logout();
  }
}

export async function setMessageFlag(
  account: AccountCredentials,
  folder: string,
  uid: string,
  flagged: boolean,
) {
  const { accessToken } = await refreshAccessToken(account, imapScope);
  const numericUid = Number(uid);
  if (folder.startsWith("rest:") || uid.startsWith("rest:") || !Number.isInteger(numericUid) || numericUid < 1) {
    const messageId = uid.startsWith("rest:") ? uid.slice(5) : uid;
    await outlookRestRequest(accessToken, `messages/${encodeURIComponent(messageId)}`, {
      method: "PATCH",
      body: { Flag: { FlagStatus: flagged ? "Flagged" : "NotFlagged" } },
    });
    markAccountSynced(account.ownerKey, account.id);
    return;
  }

  const client = await connectImap(account, accessToken);
  let lock: Awaited<ReturnType<ImapFlow["getMailboxLock"]>> | undefined;
  try {
    lock = await client.getMailboxLock(folder);
    if (flagged) {
      await client.messageFlagsAdd(numericUid, ["\\Flagged"], { uid: true });
    } else {
      await client.messageFlagsRemove(numericUid, ["\\Flagged"], { uid: true });
    }
    markAccountSynced(account.ownerKey, account.id);
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
    attachments?: Array<{
      filename: string;
      contentType: string;
      contentBase64: string;
      size: number;
    }>;
  },
) {
  const attachments = (message.attachments || []).map((attachment) => ({
    // Prevent a supplied path from being interpreted as a local path or from
    // leaking directory information into the outgoing MIME message.
    filename: attachment.filename.replace(/[/\\]/g, "_").slice(0, 255),
    contentType: attachment.contentType,
    content: Buffer.from(attachment.contentBase64, "base64"),
  }));
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
        attachments,
      });
      return { messageId: result.messageId, accepted: result.accepted };
    } catch (error) {
      lastError = error;
      transport.close();
    }
  }

  const smtpError = mailErrorMessage(lastError);
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
          attachments: attachments.map((attachment) => ({
            "@odata.type": "#microsoft.graph.fileAttachment",
            name: attachment.filename,
            contentType: attachment.contentType,
            contentBytes: attachment.content.toString("base64"),
          })),
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
      `邮件发送失败。SMTP：${smtpError}；Graph 回退：${mailErrorMessage(graphFailure)}。请使用授权工具重新授予 Mail.Send，或在微软邮箱设置中启用 SMTP AUTH。`,
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
