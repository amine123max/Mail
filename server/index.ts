import "dotenv/config";
import express, {
  type NextFunction,
  type Request,
  type Response,
} from "express";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { z } from "zod";
import {
  AuthError,
  authenticate,
  clearGuestCookie,
  clearSessionCookies,
  createGuestIdentity,
  createSessionCookie,
  getGuestId,
  getIdentity,
  getUserSessionId,
  initializeAdministrator,
  type Identity,
  registerUser,
  requestRegistrationCode,
  renewGuestIdentity,
  requireIdentity,
  requireUser,
} from "./auth";
import {
  databasePath,
  deleteGuestSession,
  deleteUserSession,
  deleteAccount,
  getAccountCredentials,
  importAccounts,
  isSetupRequired,
  listAccounts,
  transferGuestAccounts,
  updateAccount,
} from "./database";
import { parseAccountImport } from "./importer";
import {
  getMessage,
  listFolders,
  listMessages,
  MailServiceError,
  pollDeviceCode,
  requestDeviceCode,
  sendMessage,
  testAccount,
} from "./outlook";

const app = express();
if (process.env.MAIL_TRUST_PROXY === "1") app.set("trust proxy", 1);
const isProduction = process.env.NODE_ENV === "production";
const port = Number(process.env.PORT || (isProduction ? 3000 : 3001));
const host = process.env.HOST || "127.0.0.1";

if (isProduction) {
  if (!process.env.MAIL_SESSION_SECRET || process.env.MAIL_SESSION_SECRET.length < 32) {
    throw new Error("生产环境必须设置至少 32 位的 MAIL_SESSION_SECRET");
  }
  if (!process.env.MAIL_VERIFICATION_SMTP_HOST || !(process.env.MAIL_VERIFICATION_FROM || process.env.MAIL_VERIFICATION_SMTP_USER)) {
    throw new Error("生产环境必须配置注册验证码邮件服务");
  }
}

app.disable("x-powered-by");
app.use(express.json({ limit: "2mb" }));
app.use((request, response, next) => {
  response.setHeader("X-Content-Type-Options", "nosniff");
  response.setHeader("X-Frame-Options", "DENY");
  response.setHeader("Referrer-Policy", "no-referrer");
  response.setHeader("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()");
  response.setHeader(
    "Content-Security-Policy",
    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
  );
  if (request.secure) {
    response.setHeader("Strict-Transport-Security", "max-age=31536000; includeSubDomains");
  }
  next();
});
app.use("/api", (_request, response, next) => {
  response.setHeader("Cache-Control", "no-store, max-age=0");
  response.setHeader("Pragma", "no-cache");
  next();
});

type AsyncHandler = (
  request: Request,
  response: Response,
  next: NextFunction,
) => Promise<unknown>;

function route(handler: AsyncHandler) {
  return (request: Request, response: Response, next: NextFunction) => {
    Promise.resolve(handler(request, response, next)).catch(next);
  };
}

const authAttempts = new Map<string, { count: number; resetAt: number }>();
const sendAttempts = new Map<string, { count: number; resetAt: number }>();
function limitAuth(request: Request, response: Response, next: NextFunction) {
  const key = request.ip || request.socket.remoteAddress || "unknown";
  const now = Date.now();
  const current = authAttempts.get(key);
  const record = !current || current.resetAt <= now
    ? { count: 0, resetAt: now + 15 * 60_000 }
    : current;
  record.count += 1;
  authAttempts.set(key, record);
  if (authAttempts.size > 5000) {
    for (const [entryKey, entry] of authAttempts) {
      if (entry.resetAt <= now) authAttempts.delete(entryKey);
    }
    while (authAttempts.size > 5000) {
      const oldest = authAttempts.keys().next().value as string | undefined;
      if (!oldest) break;
      authAttempts.delete(oldest);
    }
  }
  if (record.count > 20) {
    response.setHeader("Retry-After", String(Math.ceil((record.resetAt - now) / 1000)));
    response.status(429).json({ error: "登录尝试过于频繁，请稍后再试", code: "AUTH_RATE_LIMIT" });
    return;
  }
  next();
}

function limitSend(_request: Request, response: Response, next: NextFunction) {
  const ownerKey = identity(response).ownerKey;
  const now = Date.now();
  const current = sendAttempts.get(ownerKey);
  const record = !current || current.resetAt <= now
    ? { count: 0, resetAt: now + 60 * 60_000 }
    : current;
  record.count += 1;
  sendAttempts.set(ownerKey, record);
  if (record.count > 60) {
    response.setHeader("Retry-After", String(Math.ceil((record.resetAt - now) / 1000)));
    response.status(429).json({ error: "发件频率过高，请稍后再试", code: "SEND_RATE_LIMIT" });
    return;
  }
  next();
}

function paramValue(value: string | string[]): string {
  return Array.isArray(value) ? value[0] : value;
}

function parseId(value: string | string[]): number {
  const id = Number(paramValue(value));
  if (!Number.isInteger(id) || id < 1) {
    throw new MailServiceError("账号 ID 无效", "INVALID_ACCOUNT_ID", 400);
  }
  return id;
}

function identity(response: Response): Identity {
  return response.locals.identity as Identity;
}

function accountOrThrow(value: string | string[], response: Response) {
  const id = parseId(value);
  const account = getAccountCredentials(identity(response).ownerKey, id);
  if (!account) {
    throw new MailServiceError("邮箱账号不存在", "ACCOUNT_NOT_FOUND", 404);
  }
  return account;
}

app.get("/api/health", (_, response) => {
  response.json({ status: "ok", storage: "sqlite" });
});

app.get("/api/auth/status", (request, response) => {
  const current = getIdentity(request);
  if (current?.kind === "guest" && current.guestId) {
    response.setHeader("Set-Cookie", renewGuestIdentity(request, current.guestId));
  }
  response.json({
    authenticated: current?.kind === "user",
    guest: current?.kind === "guest",
    username: current?.username || null,
    setupRequired: isSetupRequired(),
  });
});

app.post(
  "/api/auth/verification/request",
  limitAuth,
  route(async (request, response) => {
    const body = z
      .object({
        email: z.string().trim().email("邮箱地址格式不正确").max(254),
        purpose: z.enum(["setup", "register"]),
      })
      .parse(request.body);
    response.json(await requestRegistrationCode(body.email, body.purpose));
  }),
);

app.post("/api/auth/setup", limitAuth, (request, response) => {
  const body = z
    .object({
      username: z.string().trim().regex(/^[A-Za-z0-9_]{3,32}$/, "用户名需为 3-32 位字母、数字或下划线"),
      email: z.string().trim().email("邮箱地址格式不正确").max(254),
      password: z.string().min(12, "管理员密码至少需要 12 位").max(128),
      verificationCode: z.string().regex(/^\d{6}$/, "请输入 6 位验证码"),
    })
    .parse(request.body);
  const user = initializeAdministrator(
    body.username,
    body.email,
    body.password,
    body.verificationCode,
  );
  response.setHeader("Set-Cookie", createSessionCookie(request, user));
  response.status(201).json({ authenticated: true, username: user.username, administrator: true });
});

app.post("/api/auth/login", limitAuth, (request, response) => {
  if (isSetupRequired()) {
    throw new AuthError("请先完成管理员初始化", "SETUP_REQUIRED", 409);
  }
  const body = z
    .object({ username: z.string().min(1), password: z.string().min(1) })
    .parse(request.body);
  const user = authenticate(body.username, body.password);
  if (!user) {
    response.status(401).json({ error: "用户名、邮箱或密码错误", code: "LOGIN_FAILED" });
    return;
  }
  const guestId = getGuestId(request);
  const transferred = guestId ? transferGuestAccounts(guestId, user.id) : 0;
  response.setHeader("Set-Cookie", [
    createSessionCookie(request, user),
    clearGuestCookie(),
  ]);
  response.json({ authenticated: true, username: user.username, transferred });
});

app.post("/api/auth/register", limitAuth, (request, response) => {
  const body = z
    .object({
      username: z.string().trim().regex(/^[A-Za-z0-9_]{3,32}$/, "用户名需为 3-32 位字母、数字或下划线"),
      email: z.string().trim().email("邮箱地址格式不正确").max(254),
      password: z.string().min(8, "密码至少需要 8 位").max(128),
      verificationCode: z.string().regex(/^\d{6}$/, "请输入 6 位验证码"),
    })
    .parse(request.body);
  const user = registerUser(body.username, body.email, body.password, body.verificationCode);
  const guestId = getGuestId(request);
  const transferred = guestId ? transferGuestAccounts(guestId, user.id) : 0;
  response.setHeader("Set-Cookie", [
    createSessionCookie(request, user),
    clearGuestCookie(),
  ]);
  response.status(201).json({ authenticated: true, username: user.username, transferred });
});

app.post("/api/auth/guest", limitAuth, (request, response) => {
  if (isSetupRequired()) {
    throw new AuthError("请先完成管理员初始化", "SETUP_REQUIRED", 409);
  }
  const existing = getIdentity(request);
  if (existing?.kind === "guest") {
    if (existing.guestId) response.setHeader("Set-Cookie", renewGuestIdentity(request, existing.guestId));
    response.json({ guest: true });
    return;
  }
  const guest = createGuestIdentity(request);
  response.setHeader("Set-Cookie", guest.cookie);
  response.status(201).json({ guest: true, expiresIn: 86_400 });
});

app.post("/api/auth/logout", (request, response) => {
  const guestId = getGuestId(request);
  if (guestId) deleteGuestSession(guestId);
  const sessionId = getUserSessionId(request);
  if (sessionId) deleteUserSession(sessionId);
  response.setHeader("Set-Cookie", clearSessionCookies());
  response.status(204).end();
});

app.use("/api", requireIdentity);

app.get("/api/accounts", (_request, response) => {
  response.json({ accounts: listAccounts(identity(response).ownerKey) });
});

app.post("/api/accounts/import", (request, response) => {
  const body = z
    .object({
      raw: z.string().min(1, "请粘贴或上传账号内容"),
      mode: z.enum(["skip", "overwrite"]).default("skip"),
    })
    .parse(request.body);
  const parsed = parseAccountImport(body.raw);
  if (parsed.errors.length) {
    response.status(400).json({
      error: "部分导入行格式不正确",
      code: "IMPORT_FORMAT_ERROR",
      details: parsed.errors,
    });
    return;
  }
  if (!parsed.accounts.length) {
    response.status(400).json({ error: "没有可导入的账号" });
    return;
  }

  const ownerKey = identity(response).ownerKey;
  const accountLimit = identity(response).kind === "guest" ? 3 : 100;
  const existingAccounts = listAccounts(ownerKey);
  const existingEmails = new Set(existingAccounts.map((account) => account.email.toLowerCase()));
  const newEmails = new Set(
    parsed.accounts
      .map((account) => account.email.toLowerCase())
      .filter((email) => !existingEmails.has(email)),
  );
  if (existingAccounts.length + newEmails.size > accountLimit) {
    response.status(403).json({
      error: `当前身份最多可保存 ${accountLimit} 个邮箱账号`,
      code: "ACCOUNT_LIMIT_REACHED",
    });
    return;
  }
  const result = importAccounts(ownerKey, parsed.accounts, body.mode);
  response.status(201).json({ ...result, accounts: listAccounts(ownerKey) });
});

app.patch("/api/accounts/:id", (request, response) => {
  const id = parseId(request.params.id);
  const body = z.object({ remark: z.string().max(200) }).parse(request.body);
  const account = updateAccount(identity(response).ownerKey, id, { remark: body.remark });
  if (!account) {
    response.status(404).json({ error: "邮箱账号不存在" });
    return;
  }
  response.json({ account });
});

app.put("/api/accounts/:id/token", (request, response) => {
  const id = parseId(request.params.id);
  const body = z
    .object({ refreshToken: z.string().min(20) })
    .parse(request.body);
  const account = updateAccount(identity(response).ownerKey, id, { refreshToken: body.refreshToken });
  if (!account) {
    response.status(404).json({ error: "邮箱账号不存在" });
    return;
  }
  response.json({ account });
});

app.delete("/api/accounts/:id", (request, response) => {
  const deleted = deleteAccount(identity(response).ownerKey, parseId(request.params.id));
  response.status(deleted ? 204 : 404).end();
});

app.post(
  "/api/accounts/:id/test",
  route(async (request, response) => {
    const result = await testAccount(accountOrThrow(request.params.id, response));
    response.json({ status: "ok", ...result });
  }),
);

app.get(
  "/api/accounts/:id/folders",
  route(async (request, response) => {
    const folders = await listFolders(accountOrThrow(request.params.id, response));
    response.json({ folders });
  }),
);

app.get(
  "/api/accounts/:id/messages",
  route(async (request, response) => {
    const query = z
      .object({
        folder: z.string().min(1).default("INBOX"),
        page: z.coerce.number().int().min(1).default(1),
        pageSize: z.coerce.number().int().min(5).max(100).default(30),
        query: z.string().max(200).optional(),
      })
      .parse(request.query);
    const result = await listMessages(accountOrThrow(request.params.id, response), query);
    response.json(result);
  }),
);

app.get(
  "/api/accounts/:id/messages/:uid",
  route(async (request, response) => {
    const uid = z.string().min(1).max(1000).parse(request.params.uid);
    const folder = z.string().min(1).parse(request.query.folder || "INBOX");
    const message = await getMessage(
      accountOrThrow(request.params.id, response),
      folder,
      uid,
    );
    response.json({ message });
  }),
);

app.post(
  "/api/accounts/:id/send",
  requireUser,
  limitSend,
  route(async (request, response) => {
    const body = z
      .object({
        to: z.string().min(3).max(2000),
        cc: z.string().max(2000).optional(),
        bcc: z.string().max(2000).optional(),
        subject: z.string().max(500).default(""),
        text: z.string().max(2_000_000).default(""),
        html: z.string().max(2_000_000).optional(),
      })
      .refine((value) => value.text.trim() || value.html?.trim(), {
        message: "邮件正文不能为空",
      })
      .parse(request.body);
    const result = await sendMessage(accountOrThrow(request.params.id, response), body);
    response.status(201).json({ status: "sent", ...result });
  }),
);

app.post(
  "/api/oauth/device-code",
  route(async (request, response) => {
    const { clientId } = z
      .object({ clientId: z.string().min(8).max(200) })
      .parse(request.body);
    response.json(await requestDeviceCode(clientId));
  }),
);

app.post(
  "/api/oauth/poll",
  route(async (request, response) => {
    const body = z
      .object({
        clientId: z.string().min(8).max(200),
        deviceCode: z.string().min(8),
      })
      .parse(request.body);
    const result = await pollDeviceCode(body.clientId, body.deviceCode);
    response.status(result.pending ? 202 : 200).json(result);
  }),
);

const webRoot = resolve("./dist");
if (existsSync(webRoot)) {
  app.use(express.static(webRoot, { index: false }));
  app.use((request, response, next) => {
    if (request.method !== "GET" || request.path.startsWith("/api/")) {
      next();
      return;
    }
    response.sendFile(resolve(webRoot, "index.html"));
  });
}

app.use((request, response) => {
  response.status(404).json({ error: `未找到接口 ${request.method} ${request.path}` });
});

app.use(
  (
    error: unknown,
    _request: Request,
    response: Response,
    _next: NextFunction,
  ) => {
    if (error instanceof z.ZodError) {
      response.status(400).json({
        error: "请求参数不正确",
        code: "VALIDATION_ERROR",
        details: error.issues,
      });
      return;
    }
    if (error instanceof MailServiceError) {
      response.status(error.status).json({ error: error.message, code: error.code });
      return;
    }
    if (error instanceof AuthError) {
      response.status(error.status).json({ error: error.message, code: error.code });
      return;
    }
    console.error(error);
    response.status(500).json({
      error: "服务器内部错误",
      code: "INTERNAL_ERROR",
    });
  },
);

app.listen(port, host, () => {
  console.log(`Mail API 已启动：http://${host}:${port}`);
  console.log("SQLite 存储已就绪");
  if (isSetupRequired()) console.log("等待首次部署管理员初始化");
});
