import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Archive,
  ArrowLeft,
  AtSign,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Cloud,
  Database,
  FilePenLine,
  Inbox,
  KeyRound,
  Languages,
  LockKeyhole,
  LogOut,
  Mail as MailIcon,
  Menu,
  Moon,
  MoreHorizontal,
  Paperclip,
  Plus,
  RefreshCw,
  Search,
  Send,
  Settings,
  ShieldCheck,
  Sparkles,
  Star,
  Sun,
  Trash2,
  UserRound,
  Users,
  X,
} from "lucide-react";
import {
  api,
  type Account,
  ApiError,
  formatDate,
  initials,
  type MailFolder,
  type MessageDetail,
  type MessageSummary,
} from "./api";
import { ComposeDialog } from "./components/ComposeDialog";
import { ImportDialog } from "./components/ImportDialog";
import { OAuthDialog } from "./components/OAuthDialog";
import { useI18n } from "./i18n";

type Page = "inbox" | "accounts" | "settings";
type Toast = { id: number; message: string; type: "success" | "error" };

const folderDefinitions = [
  { specialUse: "\\Inbox", fallback: "INBOX", label: "收件箱", icon: Inbox },
  { specialUse: "\\Sent", fallback: "Sent", label: "已发送", icon: Send },
  { specialUse: "\\Drafts", fallback: "Drafts", label: "草稿", icon: FilePenLine },
  { specialUse: "\\Archive", fallback: "Archive", label: "归档", icon: Archive },
  { specialUse: "\\Trash", fallback: "Deleted", label: "已删除", icon: Trash2 },
];

function App() {
  const { language, setLanguage, t } = useI18n();
  const [authState, setAuthState] = useState<"checking" | "setup" | "signedOut" | "guest" | "authenticated">("checking");
  const [page, setPage] = useState<Page>("inbox");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedAccountId, setSelectedAccountId] = useState<number | null>(null);
  const [folders, setFolders] = useState<MailFolder[]>([]);
  const [selectedFolder, setSelectedFolder] = useState("INBOX");
  const [messages, setMessages] = useState<MessageSummary[]>([]);
  const [messageTotal, setMessageTotal] = useState(0);
  const [selectedMessage, setSelectedMessage] = useState<MessageDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [messageLoading, setMessageLoading] = useState(false);
  const [search, setSearch] = useState("");
  const [activeSearch, setActiveSearch] = useState("");
  const [mailPage, setMailPage] = useState(1);
  const [importOpen, setImportOpen] = useState(false);
  const [composeOpen, setComposeOpen] = useState(false);
  const [oauthOpen, setOauthOpen] = useState(false);
  const [oauthAccount, setOauthAccount] = useState<Account | null>(null);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [dark, setDark] = useState(() => localStorage.getItem("mail-theme") === "dark");

  const selectedAccount = accounts.find((item) => item.id === selectedAccountId) || null;

  const notify = useCallback((message: string, type: "success" | "error" = "success") => {
    const id = Date.now() + Math.random();
    setToasts((current) => [...current, { id, message, type }]);
    window.setTimeout(() => {
      setToasts((current) => current.filter((toast) => toast.id !== id));
    }, 4500);
  }, []);

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("mail-theme", dark ? "dark" : "light");
  }, [dark]);

  useEffect(() => {
    api<{ authenticated: boolean; guest: boolean; setupRequired: boolean }>("/api/auth/status")
      .then((result) => setAuthState(result.setupRequired ? "setup" : result.authenticated ? "authenticated" : result.guest ? "guest" : "signedOut"))
      .catch(() => setAuthState("signedOut"));
  }, []);

  const loadAccounts = useCallback(async () => {
    try {
      const result = await api<{ accounts: Account[] }>("/api/accounts");
      setAccounts(result.accounts);
      setSelectedAccountId((current) => {
        if (current && result.accounts.some((item) => item.id === current)) return current;
        return result.accounts[0]?.id || null;
      });
    } catch (error) {
      notify(error instanceof Error ? error.message : "无法读取账号", "error");
    }
  }, [notify]);

  useEffect(() => {
    if (authState === "authenticated" || authState === "guest") void loadAccounts();
  }, [authState, loadAccounts]);

  useEffect(() => {
    if (!selectedAccountId) {
      setFolders([]);
      return;
    }
    let cancelled = false;
    api<{ folders: MailFolder[] }>(`/api/accounts/${selectedAccountId}/folders`)
      .then((result) => {
        if (cancelled) return;
        setFolders(result.folders);
        const inbox = result.folders.find((folder) => folder.specialUse === "\\Inbox");
        if (inbox) setSelectedFolder(inbox.path);
      })
      .catch((error) => {
        if (!cancelled) notify(error instanceof Error ? error.message : "无法读取文件夹", "error");
      });
    return () => {
      cancelled = true;
    };
  }, [selectedAccountId, notify]);

  const loadMessages = useCallback(async () => {
    if (!selectedAccountId || page !== "inbox") return;
    setLoading(true);
    setSelectedMessage(null);
    try {
      const params = new URLSearchParams({
        folder: selectedFolder,
        page: String(mailPage),
        pageSize: "30",
      });
      if (activeSearch) params.set("query", activeSearch);
      const result = await api<{ messages: MessageSummary[]; total: number }>(
        `/api/accounts/${selectedAccountId}/messages?${params}`,
      );
      setMessages(result.messages);
      setMessageTotal(result.total);
    } catch (error) {
      setMessages([]);
      setMessageTotal(0);
      notify(error instanceof Error ? error.message : "无法读取邮件", "error");
    } finally {
      setLoading(false);
    }
  }, [activeSearch, mailPage, notify, page, selectedAccountId, selectedFolder]);

  useEffect(() => void loadMessages(), [loadMessages]);

  const openMessage = async (message: MessageSummary) => {
    if (!selectedAccountId) return;
    setMessageLoading(true);
    try {
      const params = new URLSearchParams({ folder: selectedFolder });
      const result = await api<{ message: MessageDetail }>(
        `/api/accounts/${selectedAccountId}/messages/${message.uid}?${params}`,
      );
      setSelectedMessage(result.message);
      setMessages((current) => current.map((item) => item.uid === message.uid ? { ...item, unread: false } : item));
    } catch (error) {
      notify(error instanceof Error ? error.message : "无法打开邮件", "error");
    } finally {
      setMessageLoading(false);
    }
  };

  const visibleFolders = useMemo(() => folderDefinitions.map((definition) => {
    const actual = folders.find((folder) => folder.specialUse === definition.specialUse)
      || folders.find((folder) => folder.path.toLowerCase() === definition.fallback.toLowerCase());
    return { ...definition, path: actual?.path || definition.fallback, available: Boolean(actual) || definition.specialUse === "\\Inbox" };
  }), [folders]);

  const navigate = (next: Page) => {
    setPage(next);
    setMobileNavOpen(false);
  };

  const openFolder = (folder: (typeof visibleFolders)[number]) => {
    if (!folder.available) return;
    setSelectedFolder(folder.path);
    setMailPage(1);
    navigate("inbox");
  };

  if (authState === "checking") {
    return <div className="boot-screen"><RefreshCw className="spin" size={17} /></div>;
  }

  if (authState === "signedOut" || authState === "setup") {
    return <LoginPage setupRequired={authState === "setup"} dark={dark} setDark={setDark} onLogin={() => setAuthState("authenticated")} onGuest={() => setAuthState("guest")} />;
  }

  const sidebar = (
    <>
      <div className="brand-row">
        <div className="brand-mark"><img src="/paper-plane-logo.png" alt="" /></div>
        <span>Mail</span>
        <button className="mobile-close" onClick={() => setMobileNavOpen(false)}><X size={18} /></button>
      </div>
      <button className="compose-button" onClick={() => setComposeOpen(true)} disabled={!accounts.length || authState === "guest"} title={authState === "guest" ? t("游客模式仅支持收件") : ""}>
        <Plus size={18} /> {t("写邮件")}
      </button>
      <nav className="side-nav">
        <span className="nav-label">{t("邮件")}</span>
        {visibleFolders.map((folder) => {
          const Icon = folder.icon;
          const isActive = page === "inbox" && selectedFolder === folder.path;
          return (
            <button key={folder.label} className={isActive ? "active" : ""} disabled={!folder.available} onClick={() => openFolder(folder)}>
              <Icon size={18} /> {t(folder.label)}
              {folder.specialUse === "\\Inbox" && messages.filter((message) => message.unread).length > 0 && <em>{messages.filter((message) => message.unread).length}</em>}
            </button>
          );
        })}
        <span className="nav-label nav-label-spaced">{t("管理")}</span>
        <button className={page === "accounts" ? "active" : ""} onClick={() => navigate("accounts")}>
          <Users size={18} /> {t("账号管理")}
        </button>
        <button onClick={() => setImportOpen(true)}><Plus size={18} /> {t("导入账号")}</button>
        <button onClick={() => { setOauthAccount(selectedAccount); setOauthOpen(true); }}><KeyRound size={18} /> {t("微软授权")}</button>
        <span className="nav-label nav-label-spaced">{t("系统")}</span>
        <button className={page === "settings" ? "active" : ""} onClick={() => navigate("settings")}>
          <Settings size={18} /> {t("系统设置")}
        </button>
        <div className="side-language">
          <Languages size={18} />
          <span>{t("界面语言")}</span>
          <button className="language-toggle compact" onClick={() => setLanguage(language === "zh" ? "en" : "zh")} aria-label={t("界面语言")}>
            {language === "zh" ? "EN" : "中文"}
          </button>
        </div>
      </nav>
      <div className="side-accounts">
        <div className="side-section-title"><span>{t("邮箱账号")}</span><button onClick={() => setImportOpen(true)}><Plus size={14} /></button></div>
        {accounts.slice(0, 4).map((account) => (
          <button
            key={account.id}
            className={account.id === selectedAccountId ? "account-mini active" : "account-mini"}
            onClick={() => { setSelectedAccountId(account.id); navigate("inbox"); }}
          >
            <span className="mini-avatar">{account.email.slice(0, 1).toUpperCase()}</span>
            <span><strong>{account.remark || account.email.split("@")[0]}</strong><small>{account.email}</small></span>
            <i className="status-dot" />
          </button>
        ))}
      </div>
      <div className="sidebar-foot">
        <div className="storage-line"><Database size={15} /><span>{t("SQLite 本地存储")}</span><ShieldCheck size={15} /></div>
      </div>
    </>
  );

  return (
    <div className="app-shell">
      <aside className="sidebar">{sidebar}</aside>
      {mobileNavOpen && <div className="mobile-nav-overlay" onClick={() => setMobileNavOpen(false)}><aside onClick={(event) => event.stopPropagation()}>{sidebar}</aside></div>}

      <div className="workspace">
        <header className="topbar">
          <button className="mobile-menu" onClick={() => setMobileNavOpen(true)}><Menu size={19} /></button>
          <form className="search-box" onSubmit={(event) => { event.preventDefault(); setMailPage(1); setActiveSearch(search.trim()); }}>
            <Search size={16} />
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("搜索邮件主题、发件人或正文…")} />
            <kbd>↵</kbd>
          </form>
          <div className="top-actions">
            {authState === "guest" && <span className="guest-badge"><UserRound size={14} /> {t("游客模式")}</span>}
            <button className="language-toggle" onClick={() => setLanguage(language === "zh" ? "en" : "zh")} aria-label={t("界面语言")}>
              <Languages size={15} /> {language === "zh" ? "EN" : "中文"}
            </button>
            <button className="icon-button" onClick={() => setDark((value) => !value)} aria-label={t("切换主题")}>
              {dark ? <Sun size={18} /> : <Moon size={18} />}
            </button>
            <button className="icon-button notification-button" aria-label={t("通知")}><CircleAlert size={18} /><i /></button>
            <button
              className="profile-avatar profile-button"
              title={t("退出登录")}
              onClick={async () => {
                await api("/api/auth/logout", { method: "POST" });
                setAccounts([]);
                setSelectedAccountId(null);
                setAuthState("signedOut");
              }}
            ><LogOut size={14} /></button>
          </div>
        </header>

        <main className="main-content">
          {page === "inbox" && (
            <InboxPage
              accounts={accounts}
              selectedAccount={selectedAccount}
              selectedAccountId={selectedAccountId}
              setSelectedAccountId={setSelectedAccountId}
              visibleFolders={visibleFolders}
              selectedFolder={selectedFolder}
              setSelectedFolder={(folder) => { setMailPage(1); setSelectedFolder(folder); }}
              messages={messages}
              total={messageTotal}
              loading={loading}
              detailLoading={messageLoading}
              selectedMessage={selectedMessage}
              closeMessage={() => setSelectedMessage(null)}
              openMessage={openMessage}
              reload={loadMessages}
              openImport={() => setImportOpen(true)}
              openCompose={() => setComposeOpen(true)}
              canSend={authState === "authenticated"}
              page={mailPage}
              setPage={setMailPage}
            />
          )}
          {page === "accounts" && (
            <AccountsPage
              accounts={accounts}
              openImport={() => setImportOpen(true)}
              notify={notify}
              reload={loadAccounts}
              authorize={(account) => { setOauthAccount(account); setOauthOpen(true); }}
            />
          )}
          {page === "settings" && (
            <SettingsPage authorize={() => { setOauthAccount(selectedAccount); setOauthOpen(true); }} />
          )}
        </main>
      </div>

      <ImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
        notify={notify}
        onImported={(next) => { setAccounts(next); setSelectedAccountId((current) => current || next[0]?.id || null); }}
      />
      <ComposeDialog open={composeOpen} onClose={() => setComposeOpen(false)} accounts={accounts} initialAccountId={selectedAccountId} notify={notify} />
      <OAuthDialog open={oauthOpen} onClose={() => setOauthOpen(false)} account={oauthAccount} notify={notify} />

      <div className="toast-stack">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast ${toast.type}`}>
            {toast.type === "success" ? <CheckCircle2 size={17} /> : <CircleAlert size={17} />}
            {toast.message}
          </div>
        ))}
      </div>
    </div>
  );
}

function LoginPage({ setupRequired, dark, setDark, onLogin, onGuest }: { setupRequired: boolean; dark: boolean; setDark: (value: boolean) => void; onLogin: () => void; onGuest: () => void }) {
  const { language, setLanguage, t } = useI18n();
  const [mode, setMode] = useState<"login" | "register" | "setup">(setupRequired ? "setup" : "login");
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [verificationCode, setVerificationCode] = useState("");
  const [resendSeconds, setResendSeconds] = useState(0);
  const [sendingCode, setSendingCode] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (resendSeconds <= 0) return;
    const timer = window.setTimeout(() => setResendSeconds((value) => Math.max(0, value - 1)), 1000);
    return () => window.clearTimeout(timer);
  }, [resendSeconds]);

  const changeMode = (next: "login" | "register") => {
    setMode(next);
    setUsername("");
    setEmail("");
    setPassword("");
    setVerificationCode("");
    setResendSeconds(0);
    setError("");
    setNotice("");
  };

  const sendVerificationCode = async () => {
    setSendingCode(true);
    setError("");
    setNotice("");
    try {
      const result = await api<{ retryAfter: number }>("/api/auth/verification/request", {
        method: "POST",
        body: JSON.stringify({ email, purpose: mode === "setup" ? "setup" : "register" }),
      });
      setResendSeconds(result.retryAfter || 60);
      setNotice(t("验证码已发送，5 分钟内有效"));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("验证码发送失败"));
    } finally {
      setSendingCode(false);
    }
  };

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setLoading(true);
    setError("");
    try {
      const endpoint = mode === "login" ? "/api/auth/login" : mode === "setup" ? "/api/auth/setup" : "/api/auth/register";
      const body = mode === "login"
        ? { username, password }
        : { username, email, password, verificationCode };
      await api(endpoint, { method: "POST", body: JSON.stringify(body) });
      onLogin();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "登录失败");
    } finally {
      setLoading(false);
    }
  };
  const enterGuest = async () => {
    setLoading(true);
    setError("");
    try {
      await api("/api/auth/guest", { method: "POST" });
      onGuest();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("进入游客模式失败"));
    } finally {
      setLoading(false);
    }
  };
  return (
    <div className="login-page">
      <div className="login-top-brand">
        <div className="brand-mark"><img src="/paper-plane-logo.png" alt="" /></div>
        <span>Mail</span>
      </div>
      <div className="login-actions">
        <button className="language-toggle" onClick={() => setLanguage(language === "zh" ? "en" : "zh")} aria-label={t("界面语言")}><Languages size={15} /> {language === "zh" ? "EN" : "中文"}</button>
        <button className="login-theme icon-button" onClick={() => setDark(!dark)}>{dark ? <Sun size={18} /> : <Moon size={18} />}</button>
      </div>
      <section className="login-card">
        <h1>{mode === "setup" ? t("配置管理员") : mode === "login" ? t("欢迎回来") : t("创建个人空间")}</h1>
        {!setupRequired && <div className="auth-tabs"><button className={mode === "login" ? "active" : ""} onClick={() => changeMode("login")}>{t("登录")}</button><button className={mode === "register" ? "active" : ""} onClick={() => changeMode("register")}>{t("注册")}</button></div>}
        <form onSubmit={submit}>
          <label className="stack-field"><span>{t("用户名")}</span><input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" placeholder={mode !== "login" ? t("3-32 位字母、数字或下划线") : ""} /></label>
          {mode !== "login" && <label className="stack-field"><span>{t("邮箱")}</span><input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="email" placeholder="name@example.com" /></label>}
          <label className="stack-field"><span>{t("密码")}</span><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder={mode === "setup" ? t("管理员密码至少 12 位") : mode === "register" ? t("至少 8 位密码") : t("输入密码")} autoComplete={mode === "login" ? "current-password" : "new-password"} /></label>
          {mode !== "login" && <label className="stack-field"><span>{t("验证码")}</span><div className="verification-control"><input value={verificationCode} onChange={(event) => setVerificationCode(event.target.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" placeholder={t("6 位验证码")} /><button type="button" disabled={!email || sendingCode || resendSeconds > 0} onClick={sendVerificationCode}>{sendingCode ? t("发送中…") : resendSeconds > 0 ? t("{seconds} 秒后重发", { seconds: resendSeconds }) : t("发送验证码")}</button></div></label>}
          {notice && <div className="login-notice"><CheckCircle2 size={15} />{notice}</div>}
          {error && <div className="login-error"><CircleAlert size={15} />{error}</div>}
          <button className="button primary full login-submit" disabled={!username || !password || loading || (mode !== "login" && (!email || verificationCode.length !== 6))}><LockKeyhole size={16} />{loading ? t("处理中…") : mode === "setup" ? t("完成管理员配置") : mode === "login" ? t("登录 Mail") : t("创建账号")}</button>
        </form>
        {!setupRequired && <><div className="guest-divider"><span>{t("或者")}</span></div><button className="button secondary full guest-button" disabled={loading} onClick={enterGuest}><UserRound size={16} /> {t("以游客模式继续")}</button></>}
      </section>
    </div>
  );
}

function PageHeader({ eyebrow, title, description, actions }: { eyebrow: string; title: string; description: string; actions: React.ReactNode }) {
  return (
    <div className="page-header">
      <div><span className="eyebrow">{eyebrow}</span><h1>{title}</h1><p>{description}</p></div>
      <div className="page-actions">{actions}</div>
    </div>
  );
}

function InboxPage(props: {
  accounts: Account[];
  selectedAccount: Account | null;
  selectedAccountId: number | null;
  setSelectedAccountId: (id: number) => void;
  visibleFolders: Array<(typeof folderDefinitions)[number] & { path: string; available: boolean }>;
  selectedFolder: string;
  setSelectedFolder: (folder: string) => void;
  messages: MessageSummary[];
  total: number;
  loading: boolean;
  detailLoading: boolean;
  selectedMessage: MessageDetail | null;
  closeMessage: () => void;
  openMessage: (message: MessageSummary) => void;
  reload: () => void;
  openImport: () => void;
  openCompose: () => void;
  canSend: boolean;
  page: number;
  setPage: (value: number | ((current: number) => number)) => void;
}) {
  const { language, t } = useI18n();
  const unread = props.messages.filter((message) => message.unread).length;
  if (!props.accounts.length) return <EmptyMailbox openImport={props.openImport} />;

  return (
    <>
      <PageHeader
        eyebrow="MAILBOX"
        title={t("收件箱")}
        description={t("集中查看 {email} 的最新邮件。", { email: props.selectedAccount?.email || t("邮箱账号") })}
        actions={
          <>
            <select className="account-picker" value={props.selectedAccountId || ""} onChange={(event) => props.setSelectedAccountId(Number(event.target.value))}>
              {props.accounts.map((account) => <option key={account.id} value={account.id}>{account.email}</option>)}
            </select>
            <button className="button secondary" onClick={props.reload}><RefreshCw size={16} /> {t("同步")}</button>
            <button className="button primary" onClick={props.openCompose} disabled={!props.canSend} title={!props.canSend ? t("游客模式仅支持收件") : ""}><Plus size={16} /> {t("写邮件")}</button>
          </>
        }
      />

      <section className="stats-grid">
        <div className="stat-card"><div className="stat-icon blue"><Inbox size={18} /></div><span>{t("当前文件夹")}</span><strong>{props.total}</strong><small>{t("封邮件")}</small></div>
        <div className="stat-card"><div className="stat-icon violet"><Sparkles size={18} /></div><span>{t("未读邮件")}</span><strong>{unread}</strong><small>{t("当前页")}</small></div>
        <div className="stat-card"><div className="stat-icon green"><AtSign size={18} /></div><span>{t("邮箱账号")}</span><strong>{props.accounts.length}</strong><small>{t("已安全连接")}</small></div>
      </section>

      <section className="mail-panel">
        <aside className="folder-column">
          <span className="column-label">{t("文件夹")}</span>
          {props.visibleFolders.map((folder) => {
            const Icon = folder.icon;
            return (
              <button
                key={folder.label}
                className={props.selectedFolder === folder.path ? "active" : ""}
                disabled={!folder.available}
                onClick={() => props.setSelectedFolder(folder.path)}
              >
                <Icon size={17} /> {t(folder.label)}
                {folder.specialUse === "\\Inbox" && <em>{unread || ""}</em>}
              </button>
            );
          })}
          <div className="folder-tip"><Cloud size={17} /><div><strong>{t("实时连接")}</strong><span>{t("邮件正文按需读取，不会整库缓存。")}</span></div></div>
        </aside>

        <div className="message-column">
          <div className="column-head"><div><strong>{t("邮件列表")}</strong><span>{props.total} {t("封邮件")}</span></div><button><MoreHorizontal size={17} /></button></div>
          <div className="message-list">
            {props.loading && <div className="loading-state"><RefreshCw className="spin" size={20} /> {t("正在同步邮件…")}</div>}
            {!props.loading && !props.messages.length && <div className="empty-list"><MailIcon size={24} /><strong>{t("这里还没有邮件")}</strong><span>{t("尝试同步或切换其他文件夹")}</span></div>}
            {!props.loading && props.messages.map((message) => (
              <button
                key={message.uid}
                className={`message-row ${message.unread ? "unread" : ""} ${props.selectedMessage?.uid === message.uid ? "active" : ""}`}
                onClick={() => props.openMessage(message)}
              >
                <span className="sender-avatar">{initials(message.from)}</span>
                <span className="message-copy">
                  <span className="message-meta"><strong>{message.from || t("未知发件人")}</strong><time>{formatDate(message.date, false, language === "en" ? "en-US" : "zh-CN")}</time></span>
                  <span className="message-subject">{message.subject}</span>
                  <small>{t("点击查看邮件正文与详细信息")}</small>
                </span>
                {message.flagged && <Star className="row-star" size={14} />}
              </button>
            ))}
          </div>
          <div className="pagination">
            <button disabled={props.page <= 1} onClick={() => props.setPage((page) => Math.max(1, page - 1))}><ChevronLeft size={15} /></button>
            <span>{t("第 {page} 页", { page: props.page })}</span>
            <button disabled={props.page * 30 >= props.total} onClick={() => props.setPage((page) => page + 1)}><ChevronRight size={15} /></button>
          </div>
        </div>

        <article className="reader-column">
          {props.detailLoading && <div className="reader-empty"><RefreshCw className="spin" size={22} /><span>{t("正在打开邮件…")}</span></div>}
          {!props.detailLoading && !props.selectedMessage && <div className="reader-empty"><div className="reader-illustration"><MailIcon size={30} /></div><strong>{t("选择一封邮件")}</strong><span>{t("邮件正文将在这里安全显示")}</span></div>}
          {!props.detailLoading && props.selectedMessage && <MessageReader message={props.selectedMessage} onClose={props.closeMessage} />}
        </article>
      </section>
    </>
  );
}

function MessageReader({ message, onClose }: { message: MessageDetail; onClose: () => void }) {
  const { language, t } = useI18n();
  const srcDoc = `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; font-src 'none'; connect-src 'none'; media-src 'none'; frame-src 'none'"><meta name="viewport" content="width=device-width"><style>body{font:14px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;color:#222;margin:0;padding:4px 2px;overflow-wrap:anywhere}img{max-width:100%;height:auto}table{max-width:100%!important}a{color:#2563eb}.mail-remote-image-blocked{display:inline-block;padding:6px 8px;border:1px solid #ddd;border-radius:6px;color:#777;font-size:12px}</style></head><body>${message.html || `<pre style="white-space:pre-wrap;font:inherit">${escapeHtml(message.text)}</pre>`}</body></html>`;
  return (
    <>
      <div className="reader-head">
        <div className="reader-tools"><button className="reader-back" onClick={onClose} aria-label="返回邮件列表"><ArrowLeft size={16} /></button><span className="reader-tool-spacer" /><button><Archive size={16} /></button><button><Trash2 size={16} /></button><button><Star size={16} /></button></div>
        <h2>{message.subject}</h2>
        <div className="reader-sender"><span className="sender-avatar large">{initials(message.from)}</span><div><strong>{message.from}</strong><span>{t("发送给 {to}", { to: message.to || "me" })}</span></div><time>{formatDate(message.date, true, language === "en" ? "en-US" : "zh-CN")}</time></div>
      </div>
      {message.attachments.length > 0 && <div className="attachment-strip"><Paperclip size={15} /> {message.attachments.map((item) => <span key={item.index}>{item.filename}</span>)}</div>}
      <iframe title={message.subject} className="message-frame" sandbox="allow-popups allow-popups-to-escape-sandbox" srcDoc={srcDoc} />
    </>
  );
}

function EmptyMailbox({ openImport }: { openImport: () => void }) {
  const { t } = useI18n();
  return (
    <section className="empty-mailbox">
      <div className="empty-brand"><img src="/paper-plane-logo.png" alt="" /><span>Mail</span></div>
      <button className="button primary large-button" onClick={openImport}><Plus size={17} /> {t("导入")}</button>
      <div className="empty-features"><span><ShieldCheck size={16} /> {t("AES-256 加密")}</span><span><Database size={16} /> {t("SQLite 存储")}</span><span><Cloud size={16} /> {t("OAuth2 连接")}</span></div>
    </section>
  );
}

function AccountsPage({ accounts, openImport, notify, reload, authorize }: { accounts: Account[]; openImport: () => void; notify: (message: string, type?: "success" | "error") => void; reload: () => void; authorize: (account: Account) => void }) {
  const { language, t } = useI18n();
  const test = async (account: Account) => {
    try {
      const result = await api<{ canSend: boolean | null }>(`/api/accounts/${account.id}/test`, { method: "POST" });
      notify(result.canSend === false ? "收件连接正常，但当前令牌缺少 SMTP.Send 发件权限" : "收件连接与授权状态正常", result.canSend === false ? "error" : "success");
      reload();
    } catch (error) {
      notify(error instanceof Error ? error.message : "连接测试失败", "error");
    }
  };
  const remove = async (account: Account) => {
    if (!window.confirm(`确定删除 ${account.email}？此操作只删除本地记录。`)) return;
    try { await api(`/api/accounts/${account.id}`, { method: "DELETE" }); notify("账号已删除"); reload(); } catch (error) { notify(error instanceof Error ? error.message : "删除失败", "error"); }
  };
  return (
    <>
      <PageHeader eyebrow="ACCOUNTS" title={t("账号管理")} description={t("管理 Outlook、Hotmail 与 Live 邮箱连接。")} actions={<button className="button primary" onClick={openImport}><Plus size={16} /> {t("导入账号")}</button>} />
      <section className="account-summary-card"><div><span className="summary-icon"><Users size={22} /></span><div><strong>{t("{count} 个邮箱账号", { count: accounts.length })}</strong><p>{t("敏感字段均以 AES-256-GCM 加密写入 SQLite。")}</p></div></div><span className="secure-badge"><ShieldCheck size={15} /> {t("本地加密")}</span></section>
      <section className="accounts-card">
        <div className="table-head"><span>{t("邮箱账号")}</span><span>{t("备注")}</span><span>{t("最后同步")}</span><span>{t("操作")}</span></div>
        {accounts.map((account) => (
          <div className="account-row" key={account.id}>
            <div className="account-identity"><span className="account-avatar">{account.email.slice(0, 1).toUpperCase()}</span><div><strong>{account.email}</strong><small><i className="status-dot" /> Outlook OAuth2</small></div></div>
            <span>{account.remark || t("未添加备注")}</span>
            <span>{formatDate(account.lastSyncAt, true, language === "en" ? "en-US" : "zh-CN")}</span>
            <div className="row-actions"><button onClick={() => test(account)}><RefreshCw size={15} /> {t("测试")}</button><button onClick={() => authorize(account)}><KeyRound size={15} /> {t("授权")}</button><button className="danger" onClick={() => remove(account)}><Trash2 size={15} /></button></div>
          </div>
        ))}
        {!accounts.length && <div className="empty-table"><Users size={24} /><span>{t("还没有导入邮箱账号")}</span><button className="button secondary" onClick={openImport}>{t("立即导入")}</button></div>}
      </section>
    </>
  );
}

function SettingsPage({ authorize }: { authorize: () => void }) {
  const { t } = useI18n();
  return (
    <>
      <PageHeader eyebrow="SETTINGS" title={t("系统设置")} description={t("查看存储、安全和微软授权配置。")} actions={<button className="button primary" onClick={authorize}><KeyRound size={16} /> {t("微软授权工具")}</button>} />
      <section className="settings-grid">
        <div className="settings-card"><span className="settings-icon"><Database size={20} /></span><div><h3>{t("SQLite 数据库")}</h3><p>{t("账号、备注和同步时间存储在单个本地数据库文件中。")}</p><code>data/mail.sqlite</code></div><span className="config-status">{t("已启用")}</span></div>
        <div className="settings-card"><span className="settings-icon purple"><ShieldCheck size={20} /></span><div><h3>{t("凭据加密")}</h3><p>{t("密码、Client ID 与 Refresh Token 在写入前使用 AES-256-GCM 加密。")}</p><code>data/.master-key</code></div><span className="config-status">{t("已启用")}</span></div>
        <div className="settings-card"><span className="settings-icon green"><Cloud size={20} /></span><div><h3>{t("微软邮件连接")}</h3><p>{t("收件使用 IMAP XOAUTH2，发件使用 SMTP OAuth2，不启用过时的基本认证。")}</p><code>IMAP 993 · SMTP 587</code></div><span className="config-status">OAuth2</span></div>
      </section>
      <section className="permission-card"><div><span className="permission-mark"><KeyRound size={20} /></span><div><h3>{t("发件权限说明")}</h3><p>{t("从部分旧工具取得的令牌仅包含 IMAP 权限，能够收件但不能发件。使用内置授权工具重新申请令牌时，会同时请求 IMAP、SMTP、Graph 和离线刷新权限。")}</p></div></div><button className="button secondary" onClick={authorize}>{t("重新授权")}</button></section>
    </>
  );
}

function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[character] || character);
}

export default App;
