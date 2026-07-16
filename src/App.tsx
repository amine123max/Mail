import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Archive,
  ArrowLeft,
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  CircleAlert,
  Cloud,
  Database,
  FilePenLine,
  Inbox,
  KeyRound,
  Languages,
  LockKeyhole,
  LogOut,
  Mail,
  MailOpen,
  Menu,
  Minus,
  Moon,
  Paperclip,
  Plus,
  RefreshCw,
  Search,
  Send,
  Settings,
  ShieldCheck,
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
  type Announcement,
  ApiError,
  formatDate,
  initials,
  type MailFolder,
  type MessageDetail,
  type MessageSummary,
} from "./api";
import { AnnouncementDialog } from "./components/AnnouncementDialog";
import { ComposeDialog } from "./components/ComposeDialog";
import { ImportDialog } from "./components/ImportDialog";
import { OAuthDialog } from "./components/OAuthDialog";
import { useI18n } from "./i18n";
import {
  mailPath,
  parseMailPath,
  routeForSegment,
  type FolderRoute,
  type MailRoute,
  type MailRouteSegment,
  type PageRoute,
} from "./routes";

type Toast = { id: number; message: string; type: "success" | "error" };
type CurrentUser = { username: string; administrator: boolean };
type PendingSend = {
  id: string;
  accountId: number;
  from: string;
  to: string;
  subject: string;
  createdAt: string;
  status: "sending" | "failed";
  error?: string;
};
type MessageMoveConfirmation = {
  accountId: number;
  uid: number | string;
  subject: string;
  sourceFolder: string;
  sourceRoute: FolderRoute;
  targetFolder: string;
  targetRoute: "archive" | "trash";
};
const brandLogoUrl = `${import.meta.env.BASE_URL}paper-plane-logo.png`;
const appBasePath = import.meta.env.BASE_URL.replace(/\/$/, "");
const avatarGradients = [
  "linear-gradient(145deg, #7c3aed, #3b0764)",
  "linear-gradient(145deg, #2563eb, #172554)",
  "linear-gradient(145deg, #db2777, #701a75)",
  "linear-gradient(145deg, #ea580c, #7c2d12)",
  "linear-gradient(145deg, #059669, #064e3b)",
  "linear-gradient(145deg, #0891b2, #164e63)",
  "linear-gradient(145deg, #ca8a04, #713f12)",
];

function avatarGradient(seed: string) {
  const hash = [...seed].reduce((value, character) => ((value * 31) + character.charCodeAt(0)) >>> 0, 0);
  return avatarGradients[hash % avatarGradients.length];
}

function mailboxAddresses(value: string): string {
  const bracketed = Array.from(value.matchAll(/<([^<>\s]+@[^<>\s]+)>/g), (match) => match[1]);
  if (bracketed.length) return Array.from(new Set(bracketed)).join(", ");
  const plain = value.match(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi);
  return plain?.length ? Array.from(new Set(plain)).join(", ") : value;
}

function messageListDate(value: string, locale: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const now = new Date();
  const sameDay = date.getFullYear() === now.getFullYear()
    && date.getMonth() === now.getMonth()
    && date.getDate() === now.getDate();
  if (sameDay) {
    return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }).format(date);
  }
  return new Intl.DateTimeFormat(locale, {
    ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }),
    month: "short",
    day: "numeric",
  }).format(date);
}

const folderDefinitions = [
  { route: "inbox" as FolderRoute, specialUse: "\\Inbox", fallback: "INBOX", label: "收件箱", icon: Inbox },
  { route: "sent" as FolderRoute, specialUse: "\\Sent", fallback: "Sent", label: "已发送", icon: Send },
  { route: "drafts" as FolderRoute, specialUse: "\\Drafts", fallback: "Drafts", label: "草稿", icon: FilePenLine },
  { route: "archive" as FolderRoute, specialUse: "\\Archive", fallback: "Archive", label: "归档", icon: Archive },
  { route: "trash" as FolderRoute, specialUse: "\\Trash", fallback: "Deleted", label: "已删除", icon: Trash2 },
];

function App() {
  const initialRoute = useMemo(() => parseMailPath(window.location.pathname, appBasePath), []);
  const { language, setLanguage, t } = useI18n();
  const [authState, setAuthState] = useState<"checking" | "setup" | "signedOut" | "guest" | "authenticated">("checking");
  const [currentUser, setCurrentUser] = useState<CurrentUser | null>(null);
  const [page, setPage] = useState<PageRoute>(initialRoute.page);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedAccountId, setSelectedAccountId] = useState<number | null>(null);
  const [folders, setFolders] = useState<MailFolder[]>([]);
  const [selectedFolderRoute, setSelectedFolderRoute] = useState<FolderRoute>(initialRoute.folder || "inbox");
  const [messages, setMessages] = useState<MessageSummary[]>([]);
  const [messageTotal, setMessageTotal] = useState(0);
  const [messageReloadVersion, setMessageReloadVersion] = useState(0);
  const [pendingSends, setPendingSends] = useState<PendingSend[]>([]);
  const [announcementOpen, setAnnouncementOpen] = useState(false);
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [announcementUnread, setAnnouncementUnread] = useState(0);
  const [announcementsLoading, setAnnouncementsLoading] = useState(false);
  const [announcementPublishing, setAnnouncementPublishing] = useState(false);
  const [selectedMessage, setSelectedMessage] = useState<MessageDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [messageLoading, setMessageLoading] = useState(false);
  const [messageActionLoading, setMessageActionLoading] = useState(false);
  const [messageMoveConfirmation, setMessageMoveConfirmation] = useState<MessageMoveConfirmation | null>(null);
  const [search, setSearch] = useState("");
  const [activeSearch, setActiveSearch] = useState("");
  const [accountSearchOpen, setAccountSearchOpen] = useState(false);
  const [accountSearch, setAccountSearch] = useState("");
  const [accountsCollapsed, setAccountsCollapsed] = useState(false);
  const [mailPage, setMailPage] = useState(1);
  const [importOpen, setImportOpen] = useState(initialRoute.dialog === "import");
  const [composeOpen, setComposeOpen] = useState(initialRoute.dialog === "compose");
  const [oauthOpen, setOauthOpen] = useState(initialRoute.dialog === "oauth");
  const [oauthAccount, setOauthAccount] = useState<Account | null>(null);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [mobileNavClosing, setMobileNavClosing] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [dark, setDark] = useState(() => localStorage.getItem("mail-theme") === "dark");
  const [mailFontScale, setMailFontScale] = useState(() => {
    const stored = Number(localStorage.getItem("mail-list-font-scale"));
    return Number.isFinite(stored) && stored >= 0.9 && stored <= 1.4 ? stored : 1.1;
  });

  const selectedAccount = accounts.find((item) => item.id === selectedAccountId) || null;
  const visibleFolders = useMemo(() => folderDefinitions.map((definition) => {
    const actual = folders.find((folder) => folder.specialUse === definition.specialUse)
      || folders.find((folder) => folder.path.toLowerCase() === definition.fallback.toLowerCase());
    return { ...definition, path: actual?.path || definition.fallback, available: Boolean(actual) || definition.specialUse === "\\Inbox" };
  }), [folders]);
  const selectedFolder = visibleFolders.find((folder) => folder.route === selectedFolderRoute)?.path || "INBOX";
  const filteredAccounts = useMemo(() => {
    const query = accountSearch.trim().toLocaleLowerCase();
    if (!query) return accounts;
    return accounts.filter((account) => `${account.remark || ""}\n${account.email}`.toLocaleLowerCase().includes(query));
  }, [accountSearch, accounts]);

  const closeMobileNav = useCallback(() => {
    if (mobileNavOpen) setMobileNavClosing(true);
  }, [mobileNavOpen]);

  const applyMailRoute = useCallback((route: MailRoute) => {
    setPage(route.page);
    if (route.folder) {
      setSelectedFolderRoute(route.folder);
      setMailPage(1);
    }
    setComposeOpen(route.dialog === "compose");
    setImportOpen(route.dialog === "import");
    setOauthOpen(route.dialog === "oauth");
    setSelectedMessage(null);
    closeMobileNav();
  }, [closeMobileNav]);

  const navigateTo = useCallback((segment: MailRouteSegment, options?: { replace?: boolean }) => {
    const route = routeForSegment(segment);
    const path = mailPath(route.segment, appBasePath);
    window.history[options?.replace ? "replaceState" : "pushState"]({ mailRoute: route.segment }, "", path);
    applyMailRoute(route);
  }, [applyMailRoute]);

  const notify = useCallback((message: string, type: "success" | "error" = "success") => {
    const id = Date.now() + Math.random();
    setToasts((current) => [...current, { id, message, type }]);
    window.setTimeout(() => {
      setToasts((current) => current.filter((toast) => toast.id !== id));
    }, 4500);
  }, []);

  const loadAnnouncements = useCallback(async (silent = false) => {
    if (!silent) setAnnouncementsLoading(true);
    try {
      const result = await api<{ announcements: Announcement[]; unreadCount: number }>("/api/announcements");
      setAnnouncements(result.announcements);
      setAnnouncementUnread(result.unreadCount);
      return result;
    } catch (error) {
      if (!silent) notify(error instanceof Error ? error.message : "无法读取公告", "error");
      return null;
    } finally {
      if (!silent) setAnnouncementsLoading(false);
    }
  }, [notify]);

  const openAnnouncements = useCallback(() => {
    setAnnouncementOpen(true);
    void (async () => {
      const result = await loadAnnouncements();
      if (!result) return;
      await api("/api/announcements/read", { method: "POST" }).catch(() => undefined);
      setAnnouncementUnread(0);
      setAnnouncements((current) => current.map((announcement) => ({ ...announcement, read: true })));
    })();
  }, [loadAnnouncements]);

  const publishAnnouncement = useCallback(async (title: string, content: string) => {
    setAnnouncementPublishing(true);
    try {
      await api("/api/announcements", {
        method: "POST",
        body: JSON.stringify({ title, content }),
      });
      const result = await loadAnnouncements(true);
      if (result) {
        await api("/api/announcements/read", { method: "POST" }).catch(() => undefined);
        setAnnouncementUnread(0);
        setAnnouncements((current) => current.map((announcement) => ({ ...announcement, read: true })));
      }
      notify("公告已发布");
    } catch (error) {
      notify(error instanceof Error ? error.message : "公告发布失败", "error");
      throw error;
    } finally {
      setAnnouncementPublishing(false);
    }
  }, [loadAnnouncements, notify]);

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("mail-theme", dark ? "dark" : "light");
  }, [dark]);

  useEffect(() => {
    localStorage.setItem("mail-list-font-scale", String(mailFontScale));
  }, [mailFontScale]);

  useEffect(() => {
    const route = parseMailPath(window.location.pathname, appBasePath);
    if (route.known) {
      window.history.replaceState({ mailRoute: route.segment }, "", window.location.href);
    } else {
      window.history.replaceState({ mailRoute: "" }, "", mailPath("", appBasePath));
      applyMailRoute(routeForSegment(""));
    }
    const handlePopState = () => applyMailRoute(parseMailPath(window.location.pathname, appBasePath));
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [applyMailRoute]);

  useEffect(() => {
    api<{ authenticated: boolean; guest: boolean; setupRequired: boolean; username: string | null; administrator: boolean }>("/api/auth/status")
      .then((result) => {
        setCurrentUser(result.authenticated && result.username ? { username: result.username, administrator: result.administrator } : null);
        setAuthState(result.setupRequired ? "setup" : result.authenticated ? "authenticated" : result.guest ? "guest" : "signedOut");
      })
      .catch(() => { setCurrentUser(null); setAuthState("signedOut"); });
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
    if (authState !== "authenticated") {
      setAnnouncements([]);
      setAnnouncementUnread(0);
      return;
    }
    void loadAnnouncements(true);
    const timer = window.setInterval(() => void loadAnnouncements(true), 60_000);
    return () => window.clearInterval(timer);
  }, [authState, loadAnnouncements]);

  useEffect(() => {
    if (oauthOpen && !oauthAccount && selectedAccount) setOauthAccount(selectedAccount);
  }, [oauthAccount, oauthOpen, selectedAccount]);

  useEffect(() => {
    if (!selectedAccountId) {
      setFolders([]);
      return;
    }
    let cancelled = false;
    setFolders([]);
    api<{ folders: MailFolder[] }>(`/api/accounts/${selectedAccountId}/folders`)
      .then((result) => {
        if (cancelled) return;
        setFolders(result.folders);
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
  }, [activeSearch, mailPage, messageReloadVersion, notify, page, selectedAccountId, selectedFolder]);

  useEffect(() => void loadMessages(), [loadMessages]);

  const openMessage = async (message: MessageSummary) => {
    if (!selectedAccountId) return;
    setMessageLoading(true);
    try {
      const params = new URLSearchParams({ folder: selectedFolder });
      const result = await api<{ message: MessageDetail }>(
        `/api/accounts/${selectedAccountId}/messages/${encodeURIComponent(String(message.uid))}?${params}`,
      );
      setSelectedMessage(result.message);
      setMessages((current) => current.map((item) => item.uid === message.uid ? { ...item, unread: false } : item));
    } catch (error) {
      notify(error instanceof Error ? error.message : "无法打开邮件", "error");
    } finally {
      setMessageLoading(false);
    }
  };

  const requestMessageMove = (uid: number | string, subject: string, targetRoute: "archive" | "trash") => {
    if (!selectedAccountId) return;
    const target = visibleFolders.find((folder) => folder.route === targetRoute);
    if (!target?.available) {
      notify(targetRoute === "archive" ? "归档文件夹不可用" : "已删除文件夹不可用", "error");
      return;
    }
    setMessageMoveConfirmation({
      accountId: selectedAccountId,
      uid,
      subject,
      sourceFolder: selectedFolder,
      sourceRoute: selectedFolderRoute,
      targetFolder: target.path,
      targetRoute,
    });
  };

  const confirmMessageMove = async () => {
    const action = messageMoveConfirmation;
    if (!action) return;
    setMessageActionLoading(true);
    try {
      await api(`/api/accounts/${action.accountId}/messages/${encodeURIComponent(String(action.uid))}/move`, {
        method: "POST",
        body: JSON.stringify({ folder: action.sourceFolder, targetFolder: action.targetFolder }),
      });
      if (selectedAccountId === action.accountId && selectedFolder === action.sourceFolder) {
        setMessages((current) => current.filter((message) => message.uid !== action.uid));
        setMessageTotal((total) => Math.max(0, total - 1));
        setSelectedMessage((current) => current?.uid === action.uid ? null : current);
        setMessageReloadVersion((value) => value + 1);
      }
      setMessageMoveConfirmation(null);
      notify(action.targetRoute === "archive" ? "邮件已归档" : action.sourceRoute === "trash" ? "邮件已永久删除" : "邮件已移至已删除");
    } catch (error) {
      notify(error instanceof Error ? error.message : "邮件操作失败", "error");
    } finally {
      setMessageActionLoading(false);
    }
  };

  const toggleSelectedMessageFlag = async () => {
    if (!selectedAccountId || !selectedMessage) return;
    const summary = messages.find((message) => message.uid === selectedMessage.uid);
    const flagged = !summary?.flagged;
    setMessageActionLoading(true);
    try {
      await api(`/api/accounts/${selectedAccountId}/messages/${encodeURIComponent(String(selectedMessage.uid))}/flag`, {
        method: "PATCH",
        body: JSON.stringify({ folder: selectedFolder, flagged }),
      });
      setMessages((current) => current.map((message) => message.uid === selectedMessage.uid ? { ...message, flagged } : message));
      notify(flagged ? "邮件已收藏" : "已取消收藏");
    } catch (error) {
      notify(error instanceof Error ? error.message : "收藏操作失败", "error");
    } finally {
      setMessageActionLoading(false);
    }
  };

  const sendMailInBackground = async (draft: { accountId: number; to: string; cc: string; subject: string; text: string }) => {
    const account = accounts.find((item) => item.id === draft.accountId);
    if (!account) {
      notify("邮箱账号不存在", "error");
      return;
    }
    const pending: PendingSend = {
      id: `${Date.now()}-${Math.random()}`,
      accountId: draft.accountId,
      from: account.email,
      to: draft.to,
      subject: draft.subject,
      createdAt: new Date().toISOString(),
      status: "sending",
    };
    setPendingSends((current) => [pending, ...current]);
    setSelectedAccountId(draft.accountId);
    navigateTo("sent", { replace: true });
    try {
      await api(`/api/accounts/${draft.accountId}/send`, {
        method: "POST",
        body: JSON.stringify({ to: draft.to, cc: draft.cc, subject: draft.subject, text: draft.text }),
      });
      setPendingSends((current) => current.filter((item) => item.id !== pending.id));
      setMessageReloadVersion((value) => value + 1);
      notify("邮件已发送");
    } catch (error) {
      const message = error instanceof Error ? error.message : "发送失败";
      setPendingSends((current) => current.map((item) => item.id === pending.id ? { ...item, status: "failed", error: message } : item));
      notify(message, "error");
    }
  };

  const openFolder = (folder: (typeof visibleFolders)[number]) => {
    if (!folder.available) return;
    navigateTo(folder.route);
  };

  if (authState === "checking") {
    return <div className="boot-screen"><RefreshCw className="spin" size={17} /></div>;
  }

  if (authState === "signedOut" || authState === "setup") {
    return <LoginPage setupRequired={authState === "setup"} dark={dark} setDark={setDark} onLogin={(user) => { setCurrentUser(user); setAuthState("authenticated"); }} onGuest={() => { setCurrentUser(null); setAuthState("guest"); }} />;
  }

  const sidebar = (
    <>
      <div className="brand-row">
        <div className="brand-mark"><img src={brandLogoUrl} alt="" /></div>
        <span>Mail</span>
        <button className="mobile-close" onClick={closeMobileNav}><X size={18} /></button>
      </div>
      <button className="compose-button" onClick={() => navigateTo("sendmails")} disabled={!accounts.length || authState === "guest"} title={authState === "guest" ? t("游客模式仅支持收件") : ""}>
        <Plus size={18} /> {t("写邮件")}
      </button>
      <div className="sidebar-scroll">
        <nav className="side-nav">
          <span className="nav-label">{t("邮件")}</span>
          {visibleFolders.map((folder) => {
            const Icon = folder.icon;
            const isActive = page === "inbox" && selectedFolderRoute === folder.route;
            return (
              <button key={folder.label} className={isActive ? "active" : ""} disabled={!folder.available} onClick={() => openFolder(folder)}>
                <Icon size={18} /> {t(folder.label)}
                {folder.specialUse === "\\Inbox" && messages.filter((message) => message.unread).length > 0 && <em>{messages.filter((message) => message.unread).length}</em>}
              </button>
            );
          })}
          <span className="nav-label nav-label-spaced">{t("管理")}</span>
          <button className={page === "accounts" ? "active" : ""} onClick={() => navigateTo("accounts")}>
            <Users size={18} /> {t("账号管理")}
          </button>
          <button onClick={() => navigateTo("import")}><Plus size={18} /> {t("导入账号")}</button>
          <button onClick={() => { setOauthAccount(selectedAccount); navigateTo("oauth"); }}><KeyRound size={18} /> {t("微软授权")}</button>
          <span className="nav-label nav-label-spaced">{t("系统")}</span>
          {currentUser?.administrator && (
            <button onClick={openAnnouncements}>
              <CircleAlert size={18} /> {t("公告管理")}
              {announcementUnread > 0 && <em>{announcementUnread}</em>}
            </button>
          )}
          <button className={page === "settings" ? "active" : ""} onClick={() => navigateTo("settings")}>
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
          <div className="side-section-title">
            <span>{t("邮箱账号")}</span>
            <div className="side-section-actions">
              <button
                onClick={() => {
                  if (accountSearchOpen) {
                    setAccountSearchOpen(false);
                    setAccountSearch("");
                  } else {
                    setAccountsCollapsed(false);
                    setAccountSearchOpen(true);
                  }
                }}
                aria-label={t(accountSearchOpen ? "关闭搜索" : "搜索邮箱账号")}
                title={t(accountSearchOpen ? "关闭搜索" : "搜索邮箱账号")}
              >
                {accountSearchOpen ? <X size={14} /> : <Search size={14} />}
              </button>
              <button onClick={() => navigateTo("import")} aria-label={t("导入账号")} title={t("导入账号")}><Plus size={14} /></button>
              <button
                onClick={() => {
                  setAccountsCollapsed((collapsed) => {
                    if (!collapsed) {
                      setAccountSearchOpen(false);
                      setAccountSearch("");
                    }
                    return !collapsed;
                  });
                }}
                aria-expanded={!accountsCollapsed}
                aria-label={t(accountsCollapsed ? "展开邮箱账号" : "收起邮箱账号")}
                title={t(accountsCollapsed ? "展开邮箱账号" : "收起邮箱账号")}
              >
                {accountsCollapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
              </button>
            </div>
          </div>
          {accountSearchOpen && !accountsCollapsed && (
            <div className="account-search">
              <Search size={14} />
              <input
                autoFocus
                value={accountSearch}
                onChange={(event) => setAccountSearch(event.target.value)}
                placeholder={t("搜索邮箱账号")}
              />
              <button
                type="button"
                onClick={() => {
                  setAccountSearchOpen(false);
                  setAccountSearch("");
                }}
                aria-label={t("关闭搜索")}
                title={t("关闭搜索")}
              >
                <X size={13} />
              </button>
            </div>
          )}
          {!accountsCollapsed && filteredAccounts.map((account) => (
            <button
              key={account.id}
              className={account.id === selectedAccountId ? "account-mini active" : "account-mini"}
              onClick={() => { setSelectedAccountId(account.id); navigateTo("inbox"); }}
            >
              <span className="mini-avatar">{account.email.slice(0, 1).toUpperCase()}</span>
              <span><strong>{account.remark || account.email.split("@")[0]}</strong><small>{account.email}</small></span>
              <i className="status-dot" />
            </button>
          ))}
          {!accountsCollapsed && Boolean(accountSearch.trim()) && !filteredAccounts.length && <div className="side-empty">{t("没有匹配的邮箱账号")}</div>}
        </div>
      </div>
      <div className="sidebar-foot">
        {authState === "authenticated" && currentUser && <div className="sidebar-user"><span className="sidebar-user-avatar" style={{ background: avatarGradient(currentUser.username) }}>{currentUser.username.slice(0, 2).toUpperCase()}</span><span className="sidebar-user-copy"><strong>{currentUser.username}</strong><small>{currentUser.administrator ? t("管理员") : t("Mail 用户")}</small></span><ShieldCheck size={17} /></div>}
        {authState !== "authenticated" && <div className="storage-line"><Database size={15} /><span>{t("SQLite 本地存储")}</span><ShieldCheck size={15} /></div>}
      </div>
    </>
  );

  return (
    <div className="app-shell">
      <aside className="sidebar">{sidebar}</aside>
      {mobileNavOpen && (
        <div
          className={`mobile-nav-overlay ${mobileNavClosing ? "closing" : ""}`}
          onClick={closeMobileNav}
          onAnimationEnd={(event) => {
            if (!mobileNavClosing || event.target !== event.currentTarget) return;
            setMobileNavOpen(false);
            setMobileNavClosing(false);
          }}
        >
          <aside onClick={(event) => event.stopPropagation()}>{sidebar}</aside>
        </div>
      )}

      <div className="workspace">
        <header className="topbar">
          <button className="mobile-menu" onClick={() => { setMobileNavClosing(false); setMobileNavOpen(true); }}><Menu size={19} /></button>
          <form className="search-box" onSubmit={(event) => { event.preventDefault(); setMailPage(1); setActiveSearch(search.trim()); }}>
            <Search size={16} />
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("搜索邮件主题、发件人或正文…")} />
            <kbd>↵</kbd>
          </form>
          <div className="top-actions">
            {authState === "guest" && <span className="guest-badge" aria-label={t("游客模式")} title={t("游客模式")}><UserRound size={14} /><span>{t("游客模式")}</span></span>}
            <button className="language-toggle" onClick={() => setLanguage(language === "zh" ? "en" : "zh")} aria-label={t("界面语言")}>
              <Languages size={15} /> {language === "zh" ? "EN" : "中文"}
            </button>
            <button className="icon-button" onClick={() => setDark((value) => !value)} aria-label={t("切换主题")}>
              {dark ? <Sun size={18} /> : <Moon size={18} />}
            </button>
            {authState === "authenticated" && (
              <button className="icon-button notification-button" onClick={openAnnouncements} aria-label={t("公告")} title={t("公告")}>
                <CircleAlert size={18} />
                {announcementUnread > 0 && <i />}
              </button>
            )}
            <button
              className="profile-avatar profile-button"
              title={t("退出登录")}
              onClick={async () => {
                await api("/api/auth/logout", { method: "POST" });
                setAccounts([]);
                setSelectedAccountId(null);
                setCurrentUser(null);
                setAuthState("signedOut");
                navigateTo("", { replace: true });
              }}
            ><LogOut size={14} /></button>
          </div>
        </header>

        <main className={`main-content ${page === "inbox" ? "inbox-content" : ""}`}>
          {page === "inbox" && (
            <InboxPage
              accounts={accounts}
              messages={messages}
              total={messageTotal}
              loading={loading}
              detailLoading={messageLoading}
              selectedMessage={selectedMessage}
              folderRoute={selectedFolderRoute}
              actionLoading={messageActionLoading}
              pendingSends={selectedFolderRoute === "sent" && selectedAccountId ? pendingSends.filter((item) => item.accountId === selectedAccountId) : []}
              closeMessage={() => setSelectedMessage(null)}
              openMessage={openMessage}
              requestMoveMessage={(message, targetRoute) => requestMessageMove(message.uid, message.subject, targetRoute)}
              archiveMessage={() => { if (selectedMessage) requestMessageMove(selectedMessage.uid, selectedMessage.subject, "archive"); }}
              deleteMessage={() => { if (selectedMessage) requestMessageMove(selectedMessage.uid, selectedMessage.subject, "trash"); }}
              toggleFlag={() => void toggleSelectedMessageFlag()}
              reload={loadMessages}
              openImport={() => navigateTo("import")}
              fontScale={mailFontScale}
              setFontScale={setMailFontScale}
              page={mailPage}
              setPage={setMailPage}
            />
          )}
          {page === "accounts" && (
            <AccountsPage
              accounts={accounts}
              openImport={() => navigateTo("import")}
              notify={notify}
              reload={loadAccounts}
              authorize={(account) => { setOauthAccount(account); navigateTo("oauth"); }}
            />
          )}
          {page === "settings" && (
            <SettingsPage authorize={() => { setOauthAccount(selectedAccount); navigateTo("oauth"); }} />
          )}
        </main>
      </div>

      <ImportDialog
        open={importOpen}
        onClose={() => navigateTo("accounts", { replace: true })}
        notify={notify}
        onImported={(next) => { setAccounts(next); setSelectedAccountId((current) => current || next[0]?.id || null); }}
      />
      <ComposeDialog open={composeOpen} onClose={() => navigateTo("inbox", { replace: true })} onSend={sendMailInBackground} accounts={accounts} initialAccountId={selectedAccountId} />
      <OAuthDialog open={oauthOpen} onClose={() => navigateTo("settings", { replace: true })} account={oauthAccount} notify={notify} />
      <AnnouncementDialog
        open={announcementOpen}
        onClose={() => setAnnouncementOpen(false)}
        announcements={announcements}
        administrator={Boolean(currentUser?.administrator)}
        loading={announcementsLoading}
        publishing={announcementPublishing}
        onRefresh={() => void loadAnnouncements()}
        onPublish={publishAnnouncement}
      />
      <MessageMoveConfirmDialog
        action={messageMoveConfirmation}
        loading={messageActionLoading}
        onClose={() => { if (!messageActionLoading) setMessageMoveConfirmation(null); }}
        onConfirm={() => void confirmMessageMove()}
      />

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

function LoginPage({ setupRequired, dark, setDark, onLogin, onGuest }: { setupRequired: boolean; dark: boolean; setDark: (value: boolean) => void; onLogin: (user: CurrentUser) => void; onGuest: () => void }) {
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
        body: JSON.stringify({ email, purpose: mode === "setup" ? "setup" : "register", language }),
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
        ? { email, password }
        : { username, email, password, verificationCode };
      const result = await api<{ username: string; administrator?: boolean }>(endpoint, { method: "POST", body: JSON.stringify(body) });
      onLogin({ username: result.username, administrator: Boolean(result.administrator) });
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
        <div className="brand-mark"><img src={brandLogoUrl} alt="" /></div>
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
          {mode === "login"
            ? <label className="stack-field"><span>{t("邮箱")}</span><input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="email" placeholder={t("输入邮箱地址")} /></label>
            : <><label className="stack-field"><span>{t("用户名")}</span><input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" placeholder={t("3-32 位字母、数字或下划线")} /></label><label className="stack-field"><span>{t("邮箱")}</span><input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="email" placeholder="name@example.com" /></label></>}
          <label className="stack-field"><span>{t("密码")}</span><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder={mode === "setup" ? t("管理员密码至少 12 位") : mode === "register" ? t("至少 8 位密码") : t("输入密码")} autoComplete={mode === "login" ? "current-password" : "new-password"} /></label>
          {mode !== "login" && <label className="stack-field"><span>{t("验证码")}</span><div className="verification-control"><input value={verificationCode} onChange={(event) => setVerificationCode(event.target.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" placeholder={t("6 位验证码")} /><button type="button" disabled={!email || sendingCode || resendSeconds > 0} onClick={sendVerificationCode}>{sendingCode ? t("发送中…") : resendSeconds > 0 ? t("{seconds} 秒后重发", { seconds: resendSeconds }) : t("发送验证码")}</button></div></label>}
          {notice && <div className="login-notice"><CheckCircle2 size={15} />{notice}</div>}
          {error && <div className="login-error"><CircleAlert size={15} />{error}</div>}
          <button className="button primary full login-submit" disabled={(mode === "login" ? !email : !username || !email || verificationCode.length !== 6) || !password || loading}><LockKeyhole size={16} />{loading ? t("处理中…") : mode === "setup" ? t("完成管理员配置") : mode === "login" ? t("登录 Mail") : t("创建账号")}</button>
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
  messages: MessageSummary[];
  total: number;
  loading: boolean;
  detailLoading: boolean;
  selectedMessage: MessageDetail | null;
  folderRoute: FolderRoute;
  actionLoading: boolean;
  pendingSends: PendingSend[];
  closeMessage: () => void;
  openMessage: (message: MessageSummary) => void;
  requestMoveMessage: (message: MessageSummary, targetRoute: "archive" | "trash") => void;
  archiveMessage: () => void;
  deleteMessage: () => void;
  toggleFlag: () => void;
  reload: () => void;
  openImport: () => void;
  fontScale: number;
  setFontScale: (value: number | ((current: number) => number)) => void;
  page: number;
  setPage: (value: number | ((current: number) => number)) => void;
}) {
  const { language, t } = useI18n();
  const gestureRef = useRef<{
    uid: number | string;
    startX: number;
    startY: number;
    activated: boolean;
    offset: number;
  } | null>(null);
  const longPressTimerRef = useRef<number | null>(null);
  const suppressClickRef = useRef(false);
  const [swipeState, setSwipeState] = useState<{ uid: number | string; offset: number } | null>(null);

  const clearLongPressTimer = () => {
    if (longPressTimerRef.current !== null) window.clearTimeout(longPressTimerRef.current);
    longPressTimerRef.current = null;
  };

  const resetSwipe = () => {
    clearLongPressTimer();
    gestureRef.current = null;
    setSwipeState(null);
  };

  useEffect(() => () => clearLongPressTimer(), []);

  const startSwipeGesture = (message: MessageSummary, event: React.PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    clearLongPressTimer();
    suppressClickRef.current = false;
    gestureRef.current = {
      uid: message.uid,
      startX: event.clientX,
      startY: event.clientY,
      activated: false,
      offset: 0,
    };
    longPressTimerRef.current = window.setTimeout(() => {
      const gesture = gestureRef.current;
      if (!gesture || gesture.uid !== message.uid) return;
      gesture.activated = true;
      setSwipeState({ uid: message.uid, offset: 0 });
    }, 360);
  };

  const moveSwipeGesture = (event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = gestureRef.current;
    if (!gesture) return;
    const deltaX = event.clientX - gesture.startX;
    const deltaY = event.clientY - gesture.startY;
    if (!gesture.activated) {
      if (Math.abs(deltaX) > 12 || Math.abs(deltaY) > 12) resetSwipe();
      return;
    }
    event.preventDefault();
    gesture.offset = Math.max(-112, Math.min(112, deltaX));
    setSwipeState({ uid: gesture.uid, offset: gesture.offset });
  };

  const finishSwipeGesture = (message: MessageSummary) => {
    const gesture = gestureRef.current;
    clearLongPressTimer();
    if (!gesture || gesture.uid !== message.uid) {
      resetSwipe();
      return;
    }
    if (gesture.activated) {
      suppressClickRef.current = true;
      if (gesture.offset <= -68) props.requestMoveMessage(message, "trash");
      if (gesture.offset >= 68) props.requestMoveMessage(message, "archive");
    }
    resetSwipe();
  };

  if (!props.accounts.length) return <EmptyMailbox openImport={props.openImport} />;

  return (
    <section className="mail-panel" style={{ "--mail-primary-size": `${(12.6 * props.fontScale).toFixed(1)}px`, "--mail-time-size": `${(10.8 * props.fontScale).toFixed(1)}px`, "--mail-secondary-size": `${(11.4 * props.fontScale).toFixed(1)}px`, "--mail-row-height": `${Math.round(54 * props.fontScale)}px`, "--mail-row-padding": `${Math.round(7 * props.fontScale)}px` } as React.CSSProperties}>
        <div className="message-column">
          <div className="column-head"><div className="column-title"><strong>{t("邮件列表")}</strong><span>{props.total + props.pendingSends.length} {t("封邮件")}</span></div><div className="column-actions"><button disabled={props.fontScale <= 0.9} onClick={() => props.setFontScale((value) => Math.max(0.9, Number((value - 0.1).toFixed(1))))} aria-label={t("减小邮件列表字号")}><Minus size={15} /></button><span className="font-scale-label">{Math.round(props.fontScale * 100)}%</span><button disabled={props.fontScale >= 1.4} onClick={() => props.setFontScale((value) => Math.min(1.4, Number((value + 0.1).toFixed(1))))} aria-label={t("增大邮件列表字号")}><Plus size={15} /></button><button onClick={props.reload} aria-label={t("同步")}><RefreshCw size={16} /></button></div></div>
          <div className="message-list">
            {props.pendingSends.map((pending) => (
              <div className={`message-row pending-send-row ${pending.status}`} key={pending.id}>
                <span className="message-state sending" aria-label={t("发送中…")}><Send size={17} /></span>
                <strong className="message-sender">{mailboxAddresses(pending.to)}</strong>
                <span className="message-summary-line">
                  <strong>{pending.subject || t("邮件主题")}</strong>
                  <small>{pending.status === "sending" ? t("发送中…") : `${t("发送失败")}：${pending.error || t("发送失败")}`}</small>
                </span>
                <time className="message-time">{messageListDate(pending.createdAt, language === "en" ? "en-US" : "zh-CN")}</time>
                <Star className="row-star" size={16} />
                <i className="pending-send-progress" aria-hidden="true" />
              </div>
            ))}
            {props.loading && <div className="loading-state"><RefreshCw className="spin" size={20} /> {t("正在同步邮件…")}</div>}
            {!props.loading && !props.messages.length && !props.pendingSends.length && <div className="empty-list"><img className="state-plane-logo small" src={brandLogoUrl} alt="" /><strong>{t("这里还没有邮件")}</strong><span>{t("尝试同步或切换其他文件夹")}</span></div>}
            {!props.loading && props.messages.map((message) => {
              const activeSwipe = swipeState?.uid === message.uid ? swipeState.offset : 0;
              const swipeProgress = Math.min(1, Math.abs(activeSwipe) / 112);
              return (
                <div
                  className={`message-swipe-shell ${activeSwipe < 0 ? "dragging swiping-left" : activeSwipe > 0 ? "dragging swiping-right" : ""}`}
                  style={activeSwipe ? { "--swipe-progress": swipeProgress, "--swipe-scale": .72 + swipeProgress * .28 } as React.CSSProperties : undefined}
                  key={message.uid}
                >
                  <span className="message-swipe-action archive"><Archive size={18} /> {t("归档")}</span>
                  <span className="message-swipe-action delete"><Trash2 size={18} /> {t("删除")}</span>
                  <button
                    className={`message-row ${message.unread ? "unread" : ""} ${props.selectedMessage?.uid === message.uid ? "active" : ""}`}
                    style={activeSwipe ? { transform: `translateX(${activeSwipe}px)` } : undefined}
                    onPointerDown={(event) => startSwipeGesture(message, event)}
                    onPointerMove={moveSwipeGesture}
                    onPointerUp={() => finishSwipeGesture(message)}
                    onPointerCancel={resetSwipe}
                    onContextMenu={(event) => event.preventDefault()}
                    onClick={(event) => {
                      if (suppressClickRef.current) {
                        suppressClickRef.current = false;
                        event.preventDefault();
                        return;
                      }
                      props.openMessage(message);
                    }}
                  >
                    <span className={`message-state ${message.unread ? "unread" : "read"}`} aria-label={t(message.unread ? "未读邮件" : "已读邮件")}>
                      {message.unread ? <Mail size={17} /> : <MailOpen size={17} />}
                    </span>
                    <strong className="message-sender">
                      {props.folderRoute === "sent"
                        ? mailboxAddresses(message.to) || t("未知发件人")
                        : message.fromEmail || mailboxAddresses(message.from) || t("未知发件人")}
                    </strong>
                    <span className="message-summary-line">
                      <strong>{message.subject}</strong>
                      {message.preview && <small>{message.preview}</small>}
                    </span>
                    <time className="message-time">{messageListDate(message.date, language === "en" ? "en-US" : "zh-CN")}</time>
                    <Star className={message.flagged ? "row-star flagged" : "row-star"} size={16} />
                  </button>
                </div>
              );
            })}
          </div>
          <div className="pagination">
            <button disabled={props.page <= 1} onClick={() => props.setPage((page) => Math.max(1, page - 1))}><ChevronLeft size={15} /></button>
            <span>{t("第 {page} 页", { page: props.page })}</span>
            <button disabled={props.page * 30 >= props.total} onClick={() => props.setPage((page) => page + 1)}><ChevronRight size={15} /></button>
          </div>
        </div>

        <article className="reader-column">
          {props.detailLoading && <div className="reader-empty"><RefreshCw className="spin" size={22} /><span>{t("正在打开邮件…")}</span></div>}
          {!props.detailLoading && !props.selectedMessage && <div className="reader-empty"><div className="reader-illustration"><img className="state-plane-logo" src={brandLogoUrl} alt="" /></div><strong>{t("选择一封邮件")}</strong><span>{t("邮件正文将在这里安全显示")}</span></div>}
          {!props.detailLoading && props.selectedMessage && (
            <MessageReader
              message={props.selectedMessage}
              flagged={Boolean(props.messages.find((message) => message.uid === props.selectedMessage?.uid)?.flagged)}
              canArchive={props.folderRoute !== "archive"}
              actionLoading={props.actionLoading}
              onClose={props.closeMessage}
              onArchive={props.archiveMessage}
              onDelete={props.deleteMessage}
              onToggleFlag={props.toggleFlag}
            />
          )}
        </article>
    </section>
  );
}

function MessageReader({
  message,
  flagged,
  canArchive,
  actionLoading,
  onClose,
  onArchive,
  onDelete,
  onToggleFlag,
}: {
  message: MessageDetail;
  flagged: boolean;
  canArchive: boolean;
  actionLoading: boolean;
  onClose: () => void;
  onArchive: () => void;
  onDelete: () => void;
  onToggleFlag: () => void;
}) {
  const { language, t } = useI18n();
  const srcDoc = `<!doctype html>
<html lang="${language}">
<head>
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src 'none'; connect-src 'none'; media-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'">
  <meta name="color-scheme" content="light only">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <style>
    *{box-sizing:border-box;min-width:0}
    html,body{width:100%;max-width:100%;min-height:100%;margin:0;overflow-x:hidden;background:#fff}
    body{color:#202124;font:14px/1.65 -apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',Arial,sans-serif;overflow-wrap:anywhere;word-break:break-word;-webkit-text-size-adjust:100%}
    .mail-document{width:100%;max-width:900px;margin:0 auto;padding:34px clamp(22px,4.5vw,56px) 72px;overflow:hidden}
    .mail-document *{max-width:100%!important}
    .mail-document> :first-child{margin-top:0!important}
    .mail-document> :last-child{margin-bottom:0!important}
    img,svg,video,canvas{max-width:100%!important;height:auto!important;object-fit:contain}
    table{width:100%!important;max-width:100%!important;table-layout:auto!important;border-collapse:collapse}
    td,th{width:auto!important;max-width:100%!important;overflow-wrap:anywhere!important;word-break:break-word!important}
    div,p,span,a,li{overflow-wrap:anywhere;word-break:break-word}
    a{color:#2563eb;text-decoration-thickness:1px;text-underline-offset:2px}
    pre,code{max-width:100%!important;overflow-wrap:anywhere!important;white-space:pre-wrap!important;word-break:break-word!important}
    .mail-image-unavailable{display:inline-flex;align-items:center;max-width:100%;margin:2px 0;padding:7px 10px;border:1px solid #e5e7eb;border-radius:7px;background:#f8fafc;color:#64748b;font-size:12px;line-height:1.4}
    blockquote{max-width:100%;margin-inline:0;padding-left:14px;border-left:3px solid #e5e7eb;color:#52525b}
    @media(max-width:640px){body{font-size:13px}.mail-document{padding:22px 14px 44px}table{font-size:inherit!important}td,th{padding-left:min(8px,2vw)!important;padding-right:min(8px,2vw)!important}}
  </style>
</head>
<body><main class="mail-document">${message.html || `<pre>${escapeHtml(message.text)}</pre>`}</main></body>
</html>`;
  return (
    <>
      <div className="reader-head">
        <div className="reader-tools"><button className="reader-back" onClick={onClose} aria-label={t("返回邮件列表")}><ArrowLeft size={16} /></button><span className="reader-tool-spacer" /><button disabled={!canArchive || actionLoading} onClick={onArchive} aria-label={t("归档")} title={t("归档")}><Archive size={16} /></button><button disabled={actionLoading} onClick={onDelete} aria-label={t("删除")} title={t("删除")}><Trash2 size={16} /></button><button className={flagged ? "flagged" : ""} disabled={actionLoading} onClick={onToggleFlag} aria-label={t(flagged ? "取消收藏" : "收藏")} title={t(flagged ? "取消收藏" : "收藏")}><Star size={16} /></button></div>
        <h2>{message.subject}</h2>
        <div className="reader-sender"><span className="sender-avatar large">{initials(message.from)}</span><div><strong>{message.from}</strong><span>{t("发送给 {to}", { to: message.to || "me" })}</span></div><time>{formatDate(message.date, true, language === "en" ? "en-US" : "zh-CN")}</time></div>
      </div>
      {message.attachments.length > 0 && <div className="attachment-strip"><Paperclip size={15} /> {message.attachments.map((item) => <span key={item.index}>{item.filename}</span>)}</div>}
      <iframe title={message.subject} className="message-frame" sandbox="allow-popups allow-popups-to-escape-sandbox" srcDoc={srcDoc} />
    </>
  );
}

function MessageMoveConfirmDialog({
  action,
  loading,
  onClose,
  onConfirm,
}: {
  action: MessageMoveConfirmation | null;
  loading: boolean;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const { t } = useI18n();
  if (!action) return null;
  const deleting = action.targetRoute === "trash";
  const permanent = deleting && action.sourceRoute === "trash";
  return (
    <div className="message-action-backdrop" onMouseDown={onClose}>
      <section
        className="message-action-sheet"
        role="dialog"
        aria-modal="true"
        aria-label={t(deleting ? "确认删除邮件" : "确认归档邮件")}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="message-action-copy">
          <span className={deleting ? "danger" : "success"}>{deleting ? <Trash2 size={22} /> : <Archive size={22} />}</span>
          <h2>{t(deleting ? "确认删除邮件" : "确认归档邮件")}</h2>
          <p>{t(permanent ? "此邮件将被永久删除，操作无法撤销。" : deleting ? "此邮件将移至已删除文件夹。" : "此邮件将从当前文件夹移动到归档。")}</p>
          <strong>{action.subject}</strong>
        </div>
        <button className={`message-action-option ${deleting ? "danger" : "success"}`} disabled={loading} onClick={onConfirm}>
          {loading ? t("处理中…") : t(deleting ? "删除" : "归档")}
        </button>
        <button className="message-action-option cancel" disabled={loading} onClick={onClose}>{t("取消")}</button>
      </section>
    </div>
  );
}

function EmptyMailbox({ openImport }: { openImport: () => void }) {
  const { t } = useI18n();
  return (
    <section className="empty-mailbox">
      <div className="empty-brand"><img src={brandLogoUrl} alt="" /><span>Mail</span></div>
      <button className="button primary large-button" onClick={openImport}><Plus size={17} /> {t("导入")}</button>
      <div className="empty-features"><span><ShieldCheck size={16} /> {t("AES-256 加密")}</span><span><Database size={16} /> {t("SQLite 存储")}</span><span><Cloud size={16} /> {t("OAuth2 连接")}</span></div>
    </section>
  );
}

function AccountsPage({ accounts, openImport, notify, reload, authorize }: { accounts: Account[]; openImport: () => void; notify: (message: string, type?: "success" | "error") => void; reload: () => void; authorize: (account: Account) => void }) {
  const { language, t } = useI18n();
  const test = async (account: Account) => {
    try {
      const result = await api<{ canSend: boolean | null; receiveTransport?: "imap" | "outlook-rest" }>(`/api/accounts/${account.id}/test`, { method: "POST" });
      const receiveMessage = result.receiveTransport === "outlook-rest"
        ? "IMAP 当前不可用，已自动切换到 Outlook API 收件"
        : "收件连接正常";
      notify(result.canSend === false ? `${receiveMessage}，但当前令牌缺少 SMTP.Send 发件权限` : `${receiveMessage}，发件授权正常`, result.canSend === false ? "error" : "success");
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
