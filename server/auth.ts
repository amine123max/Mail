import {
  createHmac,
  randomBytes,
  randomInt,
  scryptSync,
  timingSafeEqual,
} from "node:crypto";
import type { NextFunction, Request, Response } from "express";
import nodemailer from "nodemailer";
import {
  canSendEmailVerification,
  cleanupExpiredEmailVerifications,
  cleanupExpiredGuests,
  cleanupExpiredUserSessions,
  consumeEmailVerification,
  createAdministrator,
  createGuestSession,
  createUserSession,
  createUser,
  deleteEmailVerification,
  findUserByEmail,
  findUserById,
  findUserByUsername,
  guestSessionExists,
  isSetupRequired,
  saveEmailVerification,
  userSessionExists,
} from "./database";
import type { UserRow } from "./types";

export interface Identity {
  kind: "user" | "guest";
  ownerKey: string;
  userId?: number;
  username?: string;
  guestId?: string;
}

const signingKey = process.env.MAIL_SESSION_SECRET || randomBytes(32).toString("base64url");
const userLifetimeSeconds = 60 * 60 * 24 * 30;
const guestLifetimeSeconds = 60 * 60 * 24 * 400;
export const verificationLifetimeSeconds = 5 * 60;
export const verificationCooldownSeconds = 60;

cleanupExpiredGuests();
cleanupExpiredUserSessions();
cleanupExpiredEmailVerifications();
const cleanupTimer = setInterval(() => {
  cleanupExpiredGuests();
  cleanupExpiredUserSessions();
  cleanupExpiredEmailVerifications();
}, 60 * 60_000);
cleanupTimer.unref();

export class AuthError extends Error {
  constructor(
    message: string,
    public readonly code: string,
    public readonly status: number,
  ) {
    super(message);
    this.name = "AuthError";
  }
}

function sign(value: string): string {
  return createHmac("sha256", signingKey).update(value).digest("base64url");
}

function safeEqual(left: string, right: string): boolean {
  const a = Buffer.from(left);
  const b = Buffer.from(right);
  return a.length === b.length && timingSafeEqual(a, b);
}

function parseCookies(request: Request): Record<string, string> {
  return Object.fromEntries(
    (request.headers.cookie || "")
      .split(";")
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => {
        const index = part.indexOf("=");
        return index < 0
          ? [part, ""]
          : [part.slice(0, index), decodeURIComponent(part.slice(index + 1))];
      }),
  );
}

function hashPassword(password: string): string {
  const salt = randomBytes(16).toString("base64url");
  const hash = scryptSync(password, salt, 32).toString("base64url");
  return `scrypt:${salt}:${hash}`;
}

function verifyPassword(password: string, stored: string): boolean {
  const [algorithm, salt, expected] = stored.split(":");
  if (algorithm !== "scrypt" || !salt || !expected) return false;
  const actual = scryptSync(password, salt, 32).toString("base64url");
  return safeEqual(actual, expected);
}

function normalizeEmail(email: string): string {
  return email.trim().toLowerCase();
}

function hashVerificationCode(email: string, code: string): string {
  return createHmac("sha256", signingKey)
    .update(`registration:${normalizeEmail(email)}:${code}`)
    .digest("hex");
}

function verificationTransport() {
  const host = process.env.MAIL_VERIFICATION_SMTP_HOST?.trim();
  const user = process.env.MAIL_VERIFICATION_SMTP_USER?.trim();
  const password = process.env.MAIL_VERIFICATION_SMTP_PASSWORD;
  const from = process.env.MAIL_VERIFICATION_FROM?.trim() || user;
  const port = Number(process.env.MAIL_VERIFICATION_SMTP_PORT || 587);
  const secure = process.env.MAIL_VERIFICATION_SMTP_SECURE === "1";

  if (!host || !from || !Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new AuthError(
      "邮件验证码服务尚未配置，请联系管理员",
      "VERIFICATION_EMAIL_NOT_CONFIGURED",
      503,
    );
  }
  if (Boolean(user) !== Boolean(password)) {
    throw new AuthError(
      "邮件验证码服务配置不完整，请联系管理员",
      "VERIFICATION_EMAIL_NOT_CONFIGURED",
      503,
    );
  }

  return {
    from,
    transporter: nodemailer.createTransport({
      host,
      port,
      secure,
      auth: user && password ? { user, pass: password } : undefined,
    }),
  };
}

export async function requestRegistrationCode(
  emailInput: string,
  purpose: "setup" | "register",
): Promise<{ expiresIn: number; retryAfter: number }> {
  const email = normalizeEmail(emailInput);
  const setupRequired = isSetupRequired();
  if (purpose === "setup" && !setupRequired) {
    throw new AuthError("管理员初始化已完成", "SETUP_ALREADY_COMPLETED", 409);
  }
  if (purpose === "register" && setupRequired) {
    throw new AuthError("请先完成管理员初始化", "SETUP_REQUIRED", 409);
  }
  if (findUserByEmail(email)) {
    throw new AuthError("该邮箱已被注册", "EMAIL_EXISTS", 409);
  }
  if (!canSendEmailVerification(email, verificationCooldownSeconds)) {
    throw new AuthError(
      "验证码发送过于频繁，请在 60 秒后重试",
      "VERIFICATION_COOLDOWN",
      429,
    );
  }

  const code = randomInt(0, 1_000_000).toString().padStart(6, "0");
  const expiresAt = new Date(Date.now() + verificationLifetimeSeconds * 1000);
  const { from, transporter } = verificationTransport();
  saveEmailVerification(email, hashVerificationCode(email, code), expiresAt);

  try {
    await transporter.sendMail({
      from,
      to: email,
      subject: "Mail 注册验证码 / Registration code",
      text: [
        `你的 Mail 注册验证码是：${code}`,
        "验证码将在 5 分钟后失效，请勿向任何人透露。",
        "",
        `Your Mail registration code is: ${code}`,
        "This code expires in 5 minutes. Never share it with anyone.",
      ].join("\n"),
      html: `<div style="font-family:Arial,sans-serif;line-height:1.7;color:#18181b"><h2>Mail</h2><p>你的注册验证码是：</p><p style="font-size:30px;font-weight:700;letter-spacing:8px">${code}</p><p>验证码将在 <strong>5 分钟</strong>后失效，请勿向任何人透露。</p><hr style="border:0;border-top:1px solid #e4e4e7"><p>Your registration code is:</p><p style="font-size:30px;font-weight:700;letter-spacing:8px">${code}</p><p>This code expires in <strong>5 minutes</strong>. Never share it with anyone.</p></div>`,
    });
  } catch {
    deleteEmailVerification(email);
    throw new AuthError(
      "验证码邮件发送失败，请检查邮件服务配置后重试",
      "VERIFICATION_DELIVERY_FAILED",
      503,
    );
  }

  return {
    expiresIn: verificationLifetimeSeconds,
    retryAfter: verificationCooldownSeconds,
  };
}

function verifyRegistrationCode(email: string, code: string): void {
  const result = consumeEmailVerification(email, hashVerificationCode(email, code));
  if (result === "verified") return;
  if (result === "invalid") {
    throw new AuthError("验证码错误", "VERIFICATION_CODE_INVALID", 400);
  }
  if (result === "attempts_exceeded") {
    throw new AuthError(
      "验证码错误次数过多，请重新获取",
      "VERIFICATION_ATTEMPTS_EXCEEDED",
      429,
    );
  }
  throw new AuthError("验证码已失效，请重新获取", "VERIFICATION_CODE_EXPIRED", 400);
}

function secureCookie(request: Request): string[] {
  return request.secure || request.headers["x-forwarded-proto"] === "https"
    ? ["Secure"]
    : [];
}

function signedCookie(
  request: Request,
  name: string,
  payload: Record<string, unknown>,
  maxAge: number,
): string {
  const encoded = Buffer.from(JSON.stringify(payload)).toString("base64url");
  return [
    `${name}=${encodeURIComponent(`${encoded}.${sign(encoded)}`)}`,
    "Path=/",
    "HttpOnly",
    "SameSite=Strict",
    `Max-Age=${maxAge}`,
    ...secureCookie(request),
  ].join("; ");
}

function readSignedCookie<T>(request: Request, name: string): T | null {
  const token = parseCookies(request)[name];
  if (!token) return null;
  const separator = token.lastIndexOf(".");
  if (separator < 1) return null;
  const payload = token.slice(0, separator);
  if (!safeEqual(token.slice(separator + 1), sign(payload))) return null;
  try {
    return JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as T;
  } catch {
    return null;
  }
}

export function authenticate(inputUser: string, inputPassword: string): UserRow | null {
  const user = findUserByUsername(inputUser);
  return user && verifyPassword(inputPassword, user.password_hash) ? user : null;
}

export function registerUser(
  username: string,
  emailInput: string,
  password: string,
  verificationCode: string,
): UserRow {
  if (isSetupRequired()) {
    throw new AuthError("请先完成管理员初始化", "SETUP_REQUIRED", 409);
  }
  if (findUserByUsername(username)) {
    throw new AuthError("用户名已存在", "USERNAME_EXISTS", 409);
  }
  const email = normalizeEmail(emailInput);
  if (findUserByEmail(email)) {
    throw new AuthError("该邮箱已被注册", "EMAIL_EXISTS", 409);
  }
  verifyRegistrationCode(email, verificationCode);
  return createUser(username, hashPassword(password), email);
}

export function initializeAdministrator(
  username: string,
  emailInput: string,
  password: string,
  verificationCode: string,
): UserRow {
  if (!isSetupRequired()) {
    throw new AuthError("管理员初始化已完成", "SETUP_ALREADY_COMPLETED", 409);
  }
  const email = normalizeEmail(emailInput);
  verifyRegistrationCode(email, verificationCode);
  try {
    return createAdministrator(username, hashPassword(password), email);
  } catch (error) {
    if (error instanceof Error && error.message === "SETUP_ALREADY_COMPLETED") {
      throw new AuthError("管理员初始化已完成", "SETUP_ALREADY_COMPLETED", 409);
    }
    throw error;
  }
}

export function createSessionCookie(request: Request, user: UserRow): string {
  const sessionId = randomBytes(24).toString("base64url");
  const expiresAt = new Date(Date.now() + userLifetimeSeconds * 1000);
  createUserSession(sessionId, user.id, expiresAt);
  return signedCookie(
    request,
    "mail_session",
    { sessionId, userId: user.id, exp: expiresAt.getTime() },
    userLifetimeSeconds,
  );
}

export function createGuestIdentity(request: Request): { identity: Identity; cookie: string } {
  const guestId = randomBytes(24).toString("base64url");
  const expiresAt = new Date(Date.now() + guestLifetimeSeconds * 1000);
  createGuestSession(guestId, expiresAt);
  return {
    identity: { kind: "guest", guestId, ownerKey: `guest:${guestId}` },
    cookie: signedCookie(
      request,
      "mail_guest",
      { guestId, exp: expiresAt.getTime() },
      guestLifetimeSeconds,
    ),
  };
}

export function renewGuestIdentity(request: Request, guestId: string): string {
  const expiresAt = new Date(Date.now() + guestLifetimeSeconds * 1000);
  createGuestSession(guestId, expiresAt);
  return signedCookie(
    request,
    "mail_guest",
    { guestId, exp: expiresAt.getTime() },
    guestLifetimeSeconds,
  );
}

export function clearSessionCookies(): string[] {
  return [
    "mail_session=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0",
    "mail_guest=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0",
  ];
}

export function getGuestId(request: Request): string | null {
  const data = readSignedCookie<{ guestId?: string; exp?: number }>(request, "mail_guest");
  if (!data?.guestId || !data.exp || data.exp <= Date.now()) return null;
  return guestSessionExists(data.guestId) ? data.guestId : null;
}

export function getUserSessionId(request: Request): string | null {
  const data = readSignedCookie<{ sessionId?: string; userId?: number; exp?: number }>(request, "mail_session");
  if (!data?.sessionId || !data.userId || !data.exp || data.exp <= Date.now()) return null;
  return userSessionExists(data.sessionId, data.userId) ? data.sessionId : null;
}

export function getIdentity(request: Request): Identity | null {
  const session = readSignedCookie<{ sessionId?: string; userId?: number; exp?: number }>(request, "mail_session");
  if (session?.sessionId && session.userId && session.exp && session.exp > Date.now() && userSessionExists(session.sessionId, session.userId)) {
    const user = findUserById(session.userId);
    if (user) {
      return {
        kind: "user",
        userId: user.id,
        username: user.username,
        ownerKey: `user:${user.id}`,
      };
    }
  }

  const guestId = getGuestId(request);
  return guestId ? { kind: "guest", guestId, ownerKey: `guest:${guestId}` } : null;
}

export function requireIdentity(request: Request, response: Response, next: NextFunction) {
  const identity = getIdentity(request);
  if (!identity) {
    response.status(401).json({ error: "请先登录或使用游客模式", code: "IDENTITY_REQUIRED" });
    return;
  }
  response.locals.identity = identity;
  next();
}

export function requireUser(_request: Request, response: Response, next: NextFunction) {
  const identity = response.locals.identity as Identity | undefined;
  if (identity?.kind !== "user") {
    response.status(403).json({ error: "游客模式仅支持收件，登录后才能发送邮件", code: "GUEST_SEND_DISABLED" });
    return;
  }
  next();
}
