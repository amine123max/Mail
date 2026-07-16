import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

export type Language = "zh" | "en";

const english: Record<string, string> = {
  "写邮件": "Compose",
  "邮件": "MAIL",
  "工作台": "WORKSPACE",
  "收件箱": "Inbox",
  "已发送": "Sent",
  "草稿": "Drafts",
  "归档": "Archive",
  "已删除": "Trash",
  "管理": "MANAGE",
  "账号管理": "Accounts",
  "导入账号": "Import accounts",
  "微软授权": "Microsoft OAuth",
  "系统": "SYSTEM",
  "系统设置": "Settings",
  "界面语言": "Language",
  "邮箱账号": "Mailbox accounts",
  "导入账号后即可开始收件": "Import an account to start receiving mail",
  "SQLite 本地存储": "Local SQLite storage",
  "搜索邮件主题、发件人或正文…": "Search subject, sender, or message…",
  "集中查看 {email} 的最新邮件。": "View the latest messages from {email} in one place.",
  "同步": "Sync",
  "当前文件夹": "Current folder",
  "封邮件": "messages",
  "未读邮件": "Unread",
  "当前页": "this page",
  "已安全连接": "securely connected",
  "文件夹": "Folders",
  "实时连接": "Live connection",
  "邮件正文按需读取，不会整库缓存。": "Message bodies are loaded on demand and not fully cached.",
  "邮件列表": "Messages",
  "未知发件人": "Unknown sender",
  "正在同步邮件…": "Syncing messages…",
  "这里还没有邮件": "No messages here",
  "尝试同步或切换其他文件夹": "Sync or choose another folder",
  "点击查看邮件正文与详细信息": "Open the message and details",
  "第 {page} 页": "Page {page}",
  "正在打开邮件…": "Opening message…",
  "选择一封邮件": "Select a message",
  "邮件正文将在这里安全显示": "The message will be displayed safely here",
  "发送给 {to}": "To {to}",
  "把所有 Outlook 邮箱放进一个安静的工作台": "Bring every Outlook mailbox into one quiet workspace",
  "导入 Outlook、Hotmail 或 Live 账号，在一个界面中收件、阅读和发送邮件。账号凭据会加密保存在本机 SQLite。": "Import Outlook, Hotmail, or Live accounts to receive, read, and send mail in one place. Credentials are encrypted in local SQLite.",
  "导入第一个邮箱": "Import your first mailbox",
  "Outlook 邮箱管理": "Outlook Mail",
  "导入": "Import",
  "AES-256 加密": "AES-256 encryption",
  "SQLite 存储": "SQLite storage",
  "OAuth2 连接": "OAuth2 connection",
  "本地加密": "Encrypted locally",
  "管理 Outlook、Hotmail 与 Live 邮箱连接。": "Manage Outlook, Hotmail, and Live mailbox connections.",
  "{count} 个邮箱账号": "{count} mailbox accounts",
  "敏感字段均以 AES-256-GCM 加密写入 SQLite。": "Sensitive fields are encrypted with AES-256-GCM before being stored in SQLite.",
  "备注": "Note",
  "最后同步": "Last sync",
  "操作": "Actions",
  "未添加备注": "No note",
  "测试": "Test",
  "授权": "Authorize",
  "还没有导入邮箱账号": "No mailbox accounts yet",
  "立即导入": "Import now",
  "查看存储、安全和微软授权配置。": "Review storage, security, and Microsoft authorization.",
  "微软授权工具": "Microsoft authorization",
  "SQLite 数据库": "SQLite database",
  "账号、备注和同步时间存储在单个本地数据库文件中。": "Accounts, notes, and sync timestamps are stored in one local database file.",
  "已启用": "Enabled",
  "凭据加密": "Credential encryption",
  "密码、Client ID 与 Refresh Token 在写入前使用 AES-256-GCM 加密。": "Passwords, Client IDs, and refresh tokens are encrypted with AES-256-GCM before storage.",
  "微软邮件连接": "Microsoft mail connection",
  "收件使用 IMAP XOAUTH2，发件使用 SMTP OAuth2，不启用过时的基本认证。": "Receiving uses IMAP XOAUTH2 and sending uses SMTP OAuth2 without legacy basic authentication.",
  "发件权限说明": "Sending permissions",
  "从部分旧工具取得的令牌仅包含 IMAP 权限，能够收件但不能发件。使用内置授权工具重新申请令牌时，会同时请求 IMAP、SMTP、Graph 和离线刷新权限。": "Tokens from some older tools only include IMAP access. The built-in authorization flow requests IMAP, SMTP, Graph, and offline refresh permissions.",
  "重新授权": "Authorize again",
  "欢迎回来": "Welcome back",
  "登录并继续管理你的 Outlook 与 Hotmail 邮箱": "Sign in to continue managing your Outlook and Hotmail mailboxes",
  "用户名": "Username",
  "用户名或邮箱": "Username or email",
  "输入用户名或邮箱": "Enter username or email",
  "密码": "Password",
  "输入管理员密码": "Enter administrator password",
  "登录 Mail": "Sign in to Mail",
  "正在登录…": "Signing in…",
  "HttpOnly 会话 · SQLite 本地存储": "HttpOnly session · Local SQLite storage",
  "退出登录": "Sign out",
  "切换主题": "Toggle theme",
  "通知": "Notifications",
  "游客模式": "Guest mode",
  "游客模式仅支持收件": "Guest mode is receive-only",
  "创建个人空间": "Create your personal space",
  "每个用户的邮箱数据都会独立隔离并加密保存": "Each user's mailbox data is isolated and encrypted",
  "登录": "Sign in",
  "注册": "Register",
  "配置管理员": "Set up administrator",
  "3-32 位字母、数字或下划线": "3-32 letters, numbers, or underscores",
  "邮箱": "Email",
  "至少 8 位密码": "At least 8 characters",
  "管理员密码至少 12 位": "Administrator password: 12+ characters",
  "输入密码": "Enter password",
  "验证码": "Verification code",
  "6 位验证码": "6-digit code",
  "发送验证码": "Send code",
  "验证码发送失败": "Could not send the verification code",
  "验证码已发送，5 分钟内有效": "Code sent. It is valid for 5 minutes.",
  "{seconds} 秒后重发": "Resend in {seconds}s",
  "处理中…": "Processing…",
  "完成管理员配置": "Complete administrator setup",
  "创建账号": "Create account",
  "或者": "OR",
  "以游客模式继续": "Continue as guest",
  "进入游客模式失败": "Could not enter guest mode",
  "游客凭据仅保存在服务端，Cookie 不包含邮箱密码或令牌": "Guest credentials stay on the server; cookies never contain mailbox passwords or tokens",
  "导入邮箱账号": "Import mailbox accounts",
  "一次导入一个或多个 Outlook / Hotmail 账号": "Import one or more Outlook / Hotmail accounts at once",
  "文本导入": "Paste text",
  "文件上传": "Upload file",
  "支持的格式（每行一个账号）": "Supported formats (one account per line)",
  "四个字段必须完整；字段之间可使用 Tab 键或四个横线分隔。": "All four fields are required. Separate them with a Tab or four hyphens.",
  "选择 TXT / CSV 文件": "Choose a TXT / CSV file",
  "文件已读取，可继续更换": "File loaded; choose another if needed",
  "文件内容不会上传到第三方服务": "File contents are never uploaded to a third party",
  "遇到相同邮箱：": "When an email already exists:",
  "跳过已有账号": "Skip existing account",
  "覆盖凭据": "Replace credentials",
  "取消": "Cancel",
  "正在导入…": "Importing…",
  "覆盖导入": "Import and replace",
  "添加导入": "Import accounts",
  "使用已导入的 Outlook / Hotmail 账号发送": "Send with an imported Outlook / Hotmail account",
  "发件人": "From",
  "选择邮箱账号": "Choose a mailbox",
  "收件人": "To",
  "抄送": "Cc",
  "可选，多个地址使用逗号分隔": "Optional; separate addresses with commas",
  "主题": "Subject",
  "邮件主题": "Message subject",
  "输入邮件正文…": "Write your message…",
  "凭据仅由本机服务读取": "Credentials are only read by the local service",
  "保存草稿": "Save draft",
  "发送邮件": "Send message",
  "发送中…": "Sending…",
  "申请 IMAP 收件、SMTP / Graph 发件与离线刷新权限": "Request IMAP receiving, SMTP / Graph sending, and offline refresh permissions",
  "开始设备授权": "Start device authorization",
  "正在获取…": "Requesting…",
  "在微软页面输入验证码": "Enter this code on the Microsoft page",
  "复制验证码": "Copy code",
  "打开微软授权": "Open Microsoft authorization",
  "等待你在微软页面完成授权…": "Waiting for authorization in Microsoft…",
  "授权成功": "Authorization complete",
  "Refresh Token 已安全接收，不会显示在页面上。": "The refresh token was received securely and will not be displayed.",
  "关闭": "Close",
  "复制 Refresh Token": "Copy refresh token",
};

interface I18nValue {
  language: Language;
  setLanguage: (language: Language) => void;
  t: (text: string, values?: Record<string, string | number>) => string;
}

const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState<Language>(() =>
    localStorage.getItem("mail-language") === "en" ? "en" : "zh",
  );
  useEffect(() => {
    document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
  }, [language]);
  const value = useMemo<I18nValue>(() => ({
    language,
    setLanguage(next) {
      localStorage.setItem("mail-language", next);
      document.documentElement.lang = next === "zh" ? "zh-CN" : "en";
      setLanguageState(next);
    },
    t(text, values = {}) {
      let result = language === "en" ? english[text] || text : text;
      for (const [key, replacement] of Object.entries(values)) {
        result = result.replaceAll(`{${key}}`, String(replacement));
      }
      return result;
    },
  }), [language]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const context = useContext(I18nContext);
  if (!context) throw new Error("useI18n must be used inside I18nProvider");
  return context;
}
