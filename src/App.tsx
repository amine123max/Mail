import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Archive,
  ArrowLeft,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  CircleAlert,
  Cloud,
  Database,
  Download,
  EllipsisVertical,
  FilePenLine,
  FolderCog,
  Inbox,
  KeyRound,
  Languages,
  LayoutDashboard,
  LockKeyhole,
  LogOut,
  Mail,
  MailOpen,
  Menu,
  Minus,
  Moon,
  Paperclip,
  Plus,
  Copy,
  RefreshCw,
  Search,
  Send,
  Settings,
  ShieldCheck,
  Star,
  Sun,
  Trash2,
  Upload,
  UserRound,
  UserCog,
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
import { ComposePage, type ComposeAttachment } from "./components/ComposePage";
import { ImportDialog } from "./components/ImportDialog";
import { OAuthPage } from "./components/OAuthPage";
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
type AdminStats = { users: number; mailboxAccounts: number; activeGuests: number; announcements: number };
type AdminUserSummary = { id: number; username: string; email: string; administrator: boolean; accountCount: number; createdAt: string };
type AdminActivityPoint = { date: string; users: number; accounts: number; guests: number; announcements: number };
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
type AccountDragState = {
  id: number;
  pointerY: number;
  offsetY: number;
  left: number;
  width: number;
  height: number;
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

async function requestAccountsFile(ids: number[]): Promise<{ filename: string; content: string }> {
  return api<{ filename: string; content: string }>("/api/accounts/export", {
    method: "POST",
    body: JSON.stringify({ ids }),
  });
}

async function downloadAccountsFile(ids: number[]): Promise<void> {
  const result = await requestAccountsFile(ids);
  const blob = new Blob([result.content], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = result.filename || "mail.txt";
  link.style.display = "none";
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function copyPlainText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // Fall back to a temporary textarea for browsers that deny Clipboard API access.
    }
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("Clipboard access was denied");
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
  const [accountMenuId, setAccountMenuId] = useState<number | null>(null);
  const [accountDeleteConfirmation, setAccountDeleteConfirmation] = useState<Account | null>(null);
  const [accountDeleteLoading, setAccountDeleteLoading] = useState(false);
  const [accountDrag, setAccountDrag] = useState<AccountDragState | null>(null);
  const accountDragTimerRef = useRef<number | null>(null);
  const accountDragFrameRef = useRef<number | null>(null);
  const accountDragPointerRef = useRef<{ x: number; y: number } | null>(null);
  const accountDragGestureRef = useRef<{
    id: number;
    startY: number;
    active: boolean;
    order: number[];
  } | null>(null);
  const accountClickBlockUntilRef = useRef(0);
  const [mailPage, setMailPage] = useState(1);
  const [importOpen, setImportOpen] = useState(initialRoute.dialog === "import");
  const [oauthAccount, setOauthAccount] = useState<Account | null>(null);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [mobileNavClosing, setMobileNavClosing] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [appEntering, setAppEntering] = useState(false);
  const [appRevealReady, setAppRevealReady] = useState(false);
  const [dark, setDark] = useState(() => localStorage.getItem("mail-theme") === "dark");
  const [mailFontScale, setMailFontScale] = useState(() => {
    const stored = Number(localStorage.getItem("mail-list-font-scale"));
    return Number.isFinite(stored) && stored >= 0.9 && stored <= 1.4 ? stored : 1.1;
  });

  const selectedAccount = accounts.find((item) => item.id === selectedAccountId) || null;
  const draggedAccount = accountDrag ? accounts.find((item) => item.id === accountDrag.id) || null : null;
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
    setImportOpen(route.dialog === "import");
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
    if (!appEntering) return;
    setAppRevealReady(false);
    const mobileEntry = window.matchMedia("(max-width: 980px)").matches;
    const revealDelay = mobileEntry ? 700 : 140;
    const completionDelay = mobileEntry ? 5000 : 4000;
    let secondFrame = 0;
    let revealTimer = 0;
    const firstFrame = window.requestAnimationFrame(() => {
      secondFrame = window.requestAnimationFrame(() => {
        revealTimer = window.setTimeout(() => setAppRevealReady(true), revealDelay);
      });
    });
    const timer = window.setTimeout(() => {
      setAppEntering(false);
      setAppRevealReady(false);
    }, completionDelay);
    return () => {
      window.cancelAnimationFrame(firstFrame);
      if (secondFrame) window.cancelAnimationFrame(secondFrame);
      if (revealTimer) window.clearTimeout(revealTimer);
      window.clearTimeout(timer);
    };
  }, [appEntering]);

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

  const clearAccountDragTimer = useCallback(() => {
    if (accountDragTimerRef.current !== null) window.clearTimeout(accountDragTimerRef.current);
    accountDragTimerRef.current = null;
  }, []);

  const clearAccountDragFrame = useCallback(() => {
    if (accountDragFrameRef.current !== null) window.cancelAnimationFrame(accountDragFrameRef.current);
    accountDragFrameRef.current = null;
    accountDragPointerRef.current = null;
  }, []);

  const persistAccountOrder = useCallback(async (ids: number[]) => {
    try {
      const result = await api<{ accounts: Account[] }>("/api/accounts/order", {
        method: "PUT",
        body: JSON.stringify({ ids }),
      });
      setAccounts(result.accounts);
    } catch (error) {
      notify(error instanceof Error ? error.message : "账号排序失败", "error");
      await loadAccounts();
    }
  }, [loadAccounts, notify]);

  const startAccountDrag = (account: Account, event: React.PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    const trigger = event.currentTarget;
    const row = trigger.closest("[data-account-id]") as HTMLElement | null;
    if (!row) return;
    trigger.setPointerCapture(event.pointerId);
    clearAccountDragTimer();
    accountDragGestureRef.current = {
      id: account.id,
      startY: event.clientY,
      active: false,
      order: accounts.map((item) => item.id),
    };
    accountDragTimerRef.current = window.setTimeout(() => {
      const gesture = accountDragGestureRef.current;
      if (!gesture || gesture.id !== account.id) return;
      const rect = row.getBoundingClientRect();
      gesture.active = true;
      accountClickBlockUntilRef.current = Date.now() + 1000;
      setAccountMenuId(null);
      setAccountDrag({
        id: account.id,
        pointerY: event.clientY,
        offsetY: event.clientY - rect.top,
        left: rect.left,
        width: rect.width,
        height: rect.height,
      });
    }, 360);
  };

  const moveAccountDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = accountDragGestureRef.current;
    if (!gesture) return;
    const deltaY = event.clientY - gesture.startY;
    if (!gesture.active) {
      if (Math.abs(deltaY) > 9) {
        clearAccountDragTimer();
        accountDragGestureRef.current = null;
      }
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    accountClickBlockUntilRef.current = Date.now() + 1000;
    accountDragPointerRef.current = { x: event.clientX, y: event.clientY };
    if (accountDragFrameRef.current !== null) return;
    accountDragFrameRef.current = window.requestAnimationFrame(() => {
      accountDragFrameRef.current = null;
      const pointer = accountDragPointerRef.current;
      const currentGesture = accountDragGestureRef.current;
      if (!pointer || !currentGesture?.active) return;
      setAccountDrag((current) => current ? { ...current, pointerY: pointer.y } : current);
      const targetRow = document
        .elementsFromPoint(pointer.x, pointer.y)
        .map((element) => element.closest("[data-account-id]") as HTMLElement | null)
        .find((element) => element && Number(element.dataset.accountId) !== currentGesture.id);
      const targetId = Number(targetRow?.dataset.accountId);
      if (!Number.isInteger(targetId) || !currentGesture.order.includes(targetId)) return;
      setAccounts((current) => {
        const from = current.findIndex((item) => item.id === currentGesture.id);
        const to = current.findIndex((item) => item.id === targetId);
        if (from < 0 || to < 0 || from === to) return current;
        const next = [...current];
        const [dragged] = next.splice(from, 1);
        next.splice(to, 0, dragged);
        currentGesture.order = next.map((item) => item.id);
        return next;
      });
    });
  };

  const finishAccountDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = accountDragGestureRef.current;
    clearAccountDragTimer();
    clearAccountDragFrame();
    accountDragGestureRef.current = null;
    if (!gesture?.active) return;
    event.preventDefault();
    event.stopPropagation();
    accountClickBlockUntilRef.current = Date.now() + 1000;
    setAccountDrag(null);
    void persistAccountOrder(gesture.order);
  };

  const cancelAccountDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = accountDragGestureRef.current;
    clearAccountDragTimer();
    clearAccountDragFrame();
    accountDragGestureRef.current = null;
    if (!gesture?.active) return;
    event.preventDefault();
    event.stopPropagation();
    accountClickBlockUntilRef.current = Date.now() + 1000;
    setAccountDrag(null);
    void persistAccountOrder(gesture.order);
  };

  const exportSidebarAccount = async (account: Account) => {
    setAccountMenuId(null);
    try {
      await downloadAccountsFile([account.id]);
      notify("账号已导出");
    } catch (error) {
      notify(error instanceof Error ? error.message : "账号导出失败", "error");
    }
  };

  const confirmAccountDelete = async () => {
    const account = accountDeleteConfirmation;
    if (!account) return;
    setAccountDeleteLoading(true);
    try {
      await api(`/api/accounts/${account.id}`, { method: "DELETE" });
      setAccountDeleteConfirmation(null);
      setAccountMenuId(null);
      notify("账号已删除");
      await loadAccounts();
    } catch (error) {
      notify(error instanceof Error ? error.message : "删除失败", "error");
    } finally {
      setAccountDeleteLoading(false);
    }
  };

  useEffect(() => () => clearAccountDragTimer(), [clearAccountDragTimer]);

  useEffect(() => {
    // Keep the empty mailbox target stable until the landing handoff and the
    // staged app reveal have finished. Loading accounts earlier can replace
    // that target mid-animation on fast connections.
    if (!appEntering && (authState === "authenticated" || authState === "guest")) void loadAccounts();
  }, [appEntering, authState, loadAccounts]);

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
    if (page === "oauth" && !oauthAccount && selectedAccount) setOauthAccount(selectedAccount);
  }, [oauthAccount, page, selectedAccount]);

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

  const sendMailInBackground = async (draft: { accountId: number; to: string; cc: string; bcc: string; subject: string; text: string; html: string; attachments: ComposeAttachment[] }) => {
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
        body: JSON.stringify({ to: draft.to, cc: draft.cc, bcc: draft.bcc, subject: draft.subject, text: draft.text, html: draft.html, attachments: draft.attachments }),
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
    return <LoginPage setupRequired={authState === "setup"} dark={dark} setDark={setDark} onLogin={(user) => { navigateTo("", { replace: true }); setAccounts([]); setSelectedAccountId(null); setAppEntering(true); setCurrentUser(user); setAuthState("authenticated"); }} onGuest={() => { navigateTo("", { replace: true }); setAccounts([]); setSelectedAccountId(null); setAppEntering(true); setCurrentUser(null); setAuthState("guest"); }} />;
  }

  const sidebar = (
    <>
      <div className="brand-row">
        <div className="brand-mark"><img src={brandLogoUrl} alt="" /></div>
        <span>Mail</span>
        <button className="mobile-close" onClick={closeMobileNav}><X size={18} /></button>
      </div>
      <button className={`compose-button ${page === "compose" ? "active" : ""}`} onClick={() => navigateTo("sendmails")} disabled={!accounts.length || authState === "guest"} title={authState === "guest" ? t("游客模式仅支持收件") : ""}>
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
          {currentUser?.administrator && <>
            <span className="nav-label nav-label-spaced">ADMIN</span>
            <button className={page === "admin" ? "active" : ""} onClick={() => navigateTo("admin")}><LayoutDashboard size={18} /> {t("管理概览")}</button>
            <button className={page === "users" ? "active" : ""} onClick={() => navigateTo("users")}><UserCog size={18} /> {t("用户管理")}</button>
            <button onClick={openAnnouncements}><CircleAlert size={18} /> {t("公告管理")}{announcementUnread > 0 && <em>{announcementUnread}</em>}</button>
          </>}
          <span className="nav-label nav-label-spaced">{t("系统")}</span>
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
            <div
              key={account.id}
              data-account-id={account.id}
              className={`account-mini ${account.id === selectedAccountId ? "active" : ""} ${accountDrag?.id === account.id ? "drag-source" : ""}`}
            >
              <button
                type="button"
                className="account-mini-main"
                onClick={(event) => {
                  if (Date.now() < accountClickBlockUntilRef.current) {
                    event.preventDefault();
                    event.stopPropagation();
                    return;
                  }
                  setSelectedAccountId(account.id);
                  navigateTo("inbox");
                }}
              >
                <span className="mini-avatar">{account.email.slice(0, 1).toUpperCase()}</span>
                <span className="account-mini-copy"><strong>{account.remark || account.email.split("@")[0]}</strong><small>{account.email}</small></span>
              </button>
              <button
                type="button"
                className="account-more"
                aria-label={t("账号操作")}
                aria-expanded={accountMenuId === account.id}
                onPointerDown={(event) => startAccountDrag(account, event)}
                onPointerMove={moveAccountDrag}
                onPointerUp={finishAccountDrag}
                onPointerCancel={cancelAccountDrag}
                onContextMenu={(event) => event.preventDefault()}
                onClick={(event) => {
                  event.stopPropagation();
                  if (Date.now() < accountClickBlockUntilRef.current) return;
                  setAccountMenuId((current) => current === account.id ? null : account.id);
                }}
              ><EllipsisVertical size={16} /></button>
              {accountMenuId === account.id && (
                <>
                  <button className="account-menu-dismiss" aria-label={t("关闭账号菜单")} onClick={() => setAccountMenuId(null)} />
                  <div className="account-menu" role="menu">
                    <button type="button" role="menuitem" className="account-menu-export" onClick={() => void exportSidebarAccount(account)}><Download size={16} /> {t("导出")}</button>
                    <button type="button" role="menuitem" className="account-menu-delete" onClick={() => { setAccountMenuId(null); setAccountDeleteConfirmation(account); }}><Trash2 size={16} /> {t("删除")}</button>
                  </div>
                </>
              )}
            </div>
          ))}
          {!accountsCollapsed && Boolean(accountSearch.trim()) && !filteredAccounts.length && <div className="side-empty">{t("没有匹配的邮箱账号")}</div>}
        </div>
        {accountDrag && draggedAccount && (
          <div
            className="account-drag-ghost"
            style={{
              left: accountDrag.left,
              top: accountDrag.pointerY - accountDrag.offsetY,
              width: accountDrag.width,
              height: accountDrag.height,
            }}
          >
            <span className="mini-avatar">{draggedAccount.email.slice(0, 1).toUpperCase()}</span>
            <span className="account-mini-copy"><strong>{draggedAccount.remark || draggedAccount.email.split("@")[0]}</strong><small>{draggedAccount.email}</small></span>
            <EllipsisVertical size={16} />
          </div>
        )}
      </div>
      <div className="sidebar-foot">
        {authState === "authenticated" && currentUser && <div className="sidebar-user"><span className="sidebar-user-avatar" style={{ background: avatarGradient(currentUser.username) }}>{currentUser.username.slice(0, 2).toUpperCase()}</span><span className="sidebar-user-copy"><strong>{currentUser.username}</strong><small>{currentUser.administrator ? t("管理员") : t("Mail 用户")}</small></span><ShieldCheck size={17} /></div>}
        {authState !== "authenticated" && <div className="storage-line"><Database size={15} /><span>{t("SQLite 本地存储")}</span><ShieldCheck size={15} /></div>}
      </div>
    </>
  );

  return (
    <div className={`app-shell ${appEntering ? "app-entering" : ""} ${appRevealReady ? "app-revealing" : ""}`}>
      {appEntering && <img className="app-entry-mobile-plane" src={brandLogoUrl} alt="" aria-hidden="true" />}
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

      <div
        className="workspace"
        onAnimationEnd={(event) => {
          if (!appEntering || !appRevealReady || event.target !== event.currentTarget || event.animationName !== "app-entry-radial") return;
          setAppEntering(false);
          setAppRevealReady(false);
        }}
      >
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

        <main className={`main-content ${page === "inbox" ? "inbox-content" : page === "compose" ? "compose-content" : ""}`}>
          {page === "inbox" && (
            <InboxPage
              accounts={accounts}
              account={selectedAccount}
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
          {page === "compose" && (
            <ComposePage
              accounts={accounts}
              initialAccountId={selectedAccountId}
              onCancel={() => navigateTo("inbox")}
              onSend={sendMailInBackground}
              notify={notify}
            />
          )}
          {page === "accounts" && (
            <AccountsPage
              accounts={accounts}
              openImport={() => navigateTo("import")}
              notify={notify}
              reload={loadAccounts}
              authorize={(account) => { setOauthAccount(account); navigateTo("oauth"); }}
              requestDelete={setAccountDeleteConfirmation}
            />
          )}
          {page === "settings" && (
            <SettingsPage authorize={() => { setOauthAccount(selectedAccount); navigateTo("oauth"); }} />
          )}
          {page === "admin" && currentUser?.administrator && <AdminOverviewPage />}
          {page === "users" && currentUser?.administrator && <AdminUsersPage />}
          {page === "oauth" && (
            <OAuthPage
              accounts={accounts}
              initialAccount={oauthAccount || selectedAccount}
              notify={notify}
              onBack={() => navigateTo("settings")}
              onAccountSelected={(account) => { setOauthAccount(account); setSelectedAccountId(account.id); }}
            />
          )}
        </main>
      </div>

      <ImportDialog
        open={importOpen}
        onClose={() => navigateTo("accounts", { replace: true })}
        notify={notify}
        onImported={(next) => { setAccounts(next); setSelectedAccountId((current) => current || next[0]?.id || null); }}
      />
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
      <AccountDeleteConfirmDialog
        account={accountDeleteConfirmation}
        loading={accountDeleteLoading}
        onClose={() => { if (!accountDeleteLoading) setAccountDeleteConfirmation(null); }}
        onConfirm={() => void confirmAccountDelete()}
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
  const [successTransition, setSuccessTransition] = useState(false);
  const [flightPlane, setFlightPlane] = useState<{ width: number; height: number; initialScale: number; finalScale: number; finalRotation: number } | null>(null);
  const [flightPath, setFlightPath] = useState<string | null>(null);
  const [flightDuration, setFlightDuration] = useState(2.8);
  const brandPlaneRef = useRef<HTMLImageElement | null>(null);
  const desktopFlightTargetRef = useRef<HTMLElement | null>(null);
  const mobileFlightTargetRef = useRef<HTMLDivElement | null>(null);
  const successTimerRef = useRef<number | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (resendSeconds <= 0) return;
    const timer = window.setTimeout(() => setResendSeconds((value) => Math.max(0, value - 1)), 1000);
    return () => window.clearTimeout(timer);
  }, [resendSeconds]);

  useEffect(() => () => {
    if (successTimerRef.current !== null) window.clearTimeout(successTimerRef.current);
  }, []);

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

  const beginSuccessTransition = async (complete: () => void) => {
    await document.fonts?.ready.catch(() => undefined);
    await Promise.all([
      brandPlaneRef.current?.decode().catch(() => undefined),
      desktopFlightTargetRef.current?.querySelector<HTMLImageElement>(".brand-mark img")?.decode().catch(() => undefined),
      mobileFlightTargetRef.current?.querySelector<HTMLImageElement>(".empty-brand img")?.decode().catch(() => undefined),
    ]);
    await new Promise<void>((resolve) => {
      window.requestAnimationFrame(() => window.requestAnimationFrame(() => resolve()));
    });
    const planeRect = brandPlaneRef.current?.getBoundingClientRect();
    if (!planeRect) {
      complete();
      return;
    }
    const desktopTarget = window.innerWidth > 980;
    const desktopTargetRect = desktopTarget ? desktopFlightTargetRef.current?.querySelector(".brand-mark img")?.getBoundingClientRect() : null;
    const mobileTargetRect = desktopTarget ? null : mobileFlightTargetRef.current?.querySelector(".empty-brand img")?.getBoundingClientRect();
    const targetSize = desktopTarget ? desktopTargetRect?.width || 31 : mobileTargetRect?.width || 58;
    const targetLeft = desktopTarget ? desktopTargetRect?.left ?? 21 : mobileTargetRect?.left ?? Math.max(18, window.innerWidth / 2 - 73);
    const targetTop = desktopTarget ? desktopTargetRect?.top ?? 18 : mobileTargetRect?.top ?? 60 + (window.innerHeight - 60) * .33;
    const startX = planeRect.left + planeRect.width / 2;
    const startY = planeRect.top + planeRect.height / 2;
    const endX = targetLeft + targetSize / 2;
    const endY = targetTop + targetSize / 2;
    const width = window.innerWidth;
    const height = window.innerHeight;
    const finalControlX = desktopTarget ? width * .13 : width * .18;
    const finalControlY = desktopTarget ? height * .06 : height * .28;
    const finalTangent = Math.atan2(endY - finalControlY, endX - finalControlX) * 180 / Math.PI;
    setFlightPlane({ width: targetSize, height: targetSize, initialScale: planeRect.width / targetSize, finalScale: 1, finalRotation: -(finalTangent + 30) });
    document.documentElement.style.setProperty("--app-entry-workspace-origin-x", `${endX - (desktopTarget ? 244 : 0)}px`);
    document.documentElement.style.setProperty("--app-entry-workspace-origin-y", `${endY}px`);
    if (!desktopTarget) {
      document.documentElement.style.setProperty("--app-entry-mobile-plane-left", `${targetLeft}px`);
      document.documentElement.style.setProperty("--app-entry-mobile-plane-top", `${targetTop}px`);
      document.documentElement.style.setProperty("--app-entry-mobile-plane-size", `${targetSize}px`);
    }
    const path = desktopTarget
      ? `M ${startX} ${startY} C ${startX - width * .12} ${startY + height * .04}, ${width * .23} ${height * .3}, ${width * .21} ${height * .62} C ${width * .2} ${height * .88}, ${width * .48} ${height * .97}, ${width * .64} ${height * .91} C ${width * .88} ${height * .83}, ${width * .88} ${height * .49}, ${width * .84} ${height * .28} C ${width * .81} ${height * .11}, ${width * .62} ${height * .1}, ${width * .48} ${height * .07} C ${width * .31} ${height * .04}, ${width * .13} ${height * .06}, ${endX} ${endY}`
      : `M ${startX} ${startY} C ${width * .2} ${startY + height * .05}, ${width * .06} ${height * .36}, ${width * .07} ${height * .63} C ${width * .08} ${height * .88}, ${width * .72} ${height * .94}, ${width * .92} ${height * .68} C ${width * .98} ${height * .48}, ${width * .87} ${height * .17}, ${width * .55} ${height * .1} C ${width * .25} ${height * .04}, ${width * .18} ${height * .28}, ${endX} ${endY}`;
    setFlightPath(path);
    setSuccessTransition(true);
    // Keep the branded flight cadence consistent across desktop and mobile.
    // Some mobile browsers report reduced motion implicitly, which previously
    // shortened this transition enough to look like the plane had jumped.
    const duration = 2.8;
    setFlightDuration(duration);
    const landingHold = 420;
    successTimerRef.current = window.setTimeout(complete, duration * 1000 + landingHold);
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
      const user = { username: result.username, administrator: Boolean(result.administrator) };
      if (mode === "setup") onLogin(user); else await beginSuccessTransition(() => onLogin(user));
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
      await beginSuccessTransition(onGuest);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("进入游客模式失败"));
    } finally {
      setLoading(false);
    }
  };
  return (
    <div className={`login-page ${successTransition ? "auth-transitioning" : ""}`}>
      <div className="login-actions">
        <button className="language-toggle" onClick={() => setLanguage(language === "zh" ? "en" : "zh")} aria-label={t("界面语言")}><Languages size={15} /> {language === "zh" ? "EN" : "中文"}</button>
        <button className="login-theme icon-button" onClick={() => setDark(!dark)}>{dark ? <Sun size={18} /> : <Moon size={18} />}</button>
      </div>
      <aside ref={desktopFlightTargetRef} className="sidebar login-desktop-flight-target" aria-hidden="true"><div className="brand-row"><span className="brand-mark"><img src={brandLogoUrl} alt="" /></span><span>Mail</span></div></aside>
      <div ref={mobileFlightTargetRef} className="login-mobile-flight-target" aria-hidden="true">
        <div className="workspace">
          <header className="topbar" />
          <main className="main-content inbox-content"><EmptyMailbox openImport={() => undefined} /></main>
        </div>
      </div>
      {successTransition && flightPath && flightPlane && <div className="login-plane-motion" aria-hidden="true" style={{ width: flightPlane.width, height: flightPlane.height, offsetPath: `path('${flightPath}')`, "--flight-initial-scale": flightPlane.initialScale, "--flight-final-scale": flightPlane.finalScale, "--flight-mid-rotation": `${flightPlane.finalRotation * .5}deg`, "--flight-final-rotation": `${flightPlane.finalRotation}deg`, "--flight-duration": `${flightDuration}s` } as React.CSSProperties}><div className="login-plane-body"><img className="login-plane-svg-image" src={brandLogoUrl} alt="" /></div></div>}
      <section className={`login-card ${successTransition ? "success-transition" : ""}`}>
        {mode === "setup" ? <h1>{t("配置管理员")}</h1> : <div className="login-brand-hero"><img ref={brandPlaneRef} src={brandLogoUrl} alt="" /><span>Mail</span></div>}
        {!setupRequired && <div className="auth-tabs"><button className={mode === "login" ? "active" : ""} onClick={() => changeMode("login")}>{t("登录")}</button><button className={mode === "register" ? "active" : ""} onClick={() => changeMode("register")}>{t("注册")}</button></div>}
        <form onSubmit={submit}>
          {mode === "login"
            ? <label className="stack-field"><span>{t("邮箱")}</span><input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="email" placeholder={t("输入邮箱地址")} /></label>
            : <><label className="stack-field"><span>{t("用户名")}</span><input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" placeholder={t("3-32 位字母、数字或下划线")} /></label><label className="stack-field"><span>{t("邮箱")}</span><input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="email" placeholder="name@example.com" /></label></>}
          <label className="stack-field"><span>{t("密码")}</span><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder={mode === "setup" ? t("管理员密码至少 12 位") : mode === "register" ? t("至少 8 位密码") : t("输入密码")} autoComplete={mode === "login" ? "current-password" : "new-password"} /></label>
          {mode !== "login" && <label className="stack-field"><span>{t("验证码")}</span><div className="verification-control"><input value={verificationCode} onChange={(event) => setVerificationCode(event.target.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" placeholder={t("6 位验证码")} /><button type="button" disabled={!email || sendingCode || resendSeconds > 0} onClick={sendVerificationCode}>{sendingCode ? t("发送中…") : resendSeconds > 0 ? t("{seconds} 秒后重发", { seconds: resendSeconds }) : t("发送验证码")}</button></div></label>}
          {notice && <div className="login-notice"><CheckCircle2 size={15} />{notice}</div>}
          {error && <div className="login-error"><CircleAlert size={15} />{error}</div>}
          <button className="button primary full login-submit" disabled={(mode === "login" ? !email : !username || !email || verificationCode.length !== 6) || !password || loading || successTransition}><LockKeyhole size={16} />{loading ? t("处理中…") : mode === "setup" ? t("完成管理员配置") : mode === "login" ? t("登录 Mail") : t("创建账号")}</button>
        </form>
        {!setupRequired && mode === "login" && <><div className="guest-divider"><span>{t("或者")}</span></div><button className="button secondary full guest-button" disabled={loading || successTransition} onClick={enterGuest}><UserRound size={16} /> {t("以游客模式继续")}</button></>}
      </section>
    </div>
  );
}

function InboxPage(props: {
  accounts: Account[];
  account: Account | null;
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
  const blockClickUntilRef = useRef(0);
  const [swipeState, setSwipeState] = useState<{ uid: number | string; offset: number } | null>(null);
  const mailPanelRef = useRef<HTMLElement | null>(null);
  const messageColumnRef = useRef<HTMLDivElement | null>(null);
  const resizeRef = useRef<{ startX: number; startWidth: number; currentWidth: number } | null>(null);
  const [resizingMailLayout, setResizingMailLayout] = useState(false);
  const [mailboxCopied, setMailboxCopied] = useState(false);
  const mailboxCopyTimerRef = useRef<number | null>(null);
  const [mailListWidth, setMailListWidth] = useState(() => {
    const stored = Number(localStorage.getItem("mail-list-column-width"));
    if (Number.isFinite(stored) && stored >= 420 && stored <= 1200) return stored;
    return Math.max(600, Math.min(900, window.innerWidth * .55));
  });

  const clearLongPressTimer = () => {
    if (longPressTimerRef.current !== null) window.clearTimeout(longPressTimerRef.current);
    longPressTimerRef.current = null;
  };

  const resetSwipe = () => {
    clearLongPressTimer();
    gestureRef.current = null;
    setSwipeState(null);
  };

  const blockMessageClick = () => {
    blockClickUntilRef.current = Date.now() + 1000;
  };

  const messageClickBlocked = () => Date.now() < blockClickUntilRef.current;

  const clampMailListWidth = (width: number) => {
    const panelWidth = mailPanelRef.current?.getBoundingClientRect().width || window.innerWidth;
    return Math.max(420, Math.min(Math.max(420, panelWidth - 286), width));
  };

  const startMailResize = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || window.innerWidth <= 980 || !messageColumnRef.current) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    const width = messageColumnRef.current.getBoundingClientRect().width;
    resizeRef.current = { startX: event.clientX, startWidth: width, currentWidth: width };
    setResizingMailLayout(true);
  };

  const moveMailResize = (event: React.PointerEvent<HTMLDivElement>) => {
    const resize = resizeRef.current;
    if (!resize) return;
    event.preventDefault();
    event.stopPropagation();
    const width = clampMailListWidth(resize.startWidth + event.clientX - resize.startX);
    resize.currentWidth = width;
    setMailListWidth(width);
  };

  const finishMailResize = () => {
    if (resizeRef.current) localStorage.setItem("mail-list-column-width", String(Math.round(resizeRef.current.currentWidth)));
    resizeRef.current = null;
    setResizingMailLayout(false);
  };

  const resizeMailWithKeyboard = (direction: -1 | 1) => {
    const width = clampMailListWidth(mailListWidth + direction * 28);
    setMailListWidth(width);
    localStorage.setItem("mail-list-column-width", String(Math.round(width)));
  };

  useEffect(() => () => {
    clearLongPressTimer();
    if (mailboxCopyTimerRef.current !== null) window.clearTimeout(mailboxCopyTimerRef.current);
  }, []);

  const copyCurrentMailbox = async () => {
    if (!props.account) return;
    try {
      await copyPlainText(props.account.email);
      setMailboxCopied(true);
      if (mailboxCopyTimerRef.current !== null) window.clearTimeout(mailboxCopyTimerRef.current);
      mailboxCopyTimerRef.current = window.setTimeout(() => setMailboxCopied(false), 1800);
    } catch {
      setMailboxCopied(false);
    }
  };

  const startSwipeGesture = (message: MessageSummary, event: React.PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    clearLongPressTimer();
    blockClickUntilRef.current = 0;
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
      blockMessageClick();
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
    event.stopPropagation();
    blockMessageClick();
    gesture.offset = Math.max(-112, Math.min(112, deltaX));
    setSwipeState({ uid: gesture.uid, offset: gesture.offset });
  };

  const finishSwipeGesture = (message: MessageSummary, event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = gestureRef.current;
    clearLongPressTimer();
    if (!gesture || gesture.uid !== message.uid) {
      resetSwipe();
      return;
    }
    if (gesture.activated) {
      event.preventDefault();
      event.stopPropagation();
      blockMessageClick();
      if (gesture.offset <= -68) props.requestMoveMessage(message, "trash");
      if (gesture.offset >= 68) props.requestMoveMessage(message, "archive");
    }
    resetSwipe();
  };

  const cancelSwipeGesture = (event: React.PointerEvent<HTMLButtonElement>) => {
    if (gestureRef.current?.activated) {
      event.preventDefault();
      event.stopPropagation();
      blockMessageClick();
    }
    resetSwipe();
  };

  if (!props.accounts.length) return <EmptyMailbox openImport={props.openImport} />;

  return (
    <section ref={mailPanelRef} className={`mail-panel ${resizingMailLayout ? "resizing" : ""}`} style={{ "--mail-primary-size": `${(12.6 * props.fontScale).toFixed(1)}px`, "--mail-time-size": `${(10.8 * props.fontScale).toFixed(1)}px`, "--mail-secondary-size": `${(11.4 * props.fontScale).toFixed(1)}px`, "--mail-row-height": `${Math.round(54 * props.fontScale)}px`, "--mail-row-padding": `${Math.round(7 * props.fontScale)}px`, "--mail-list-width": `${mailListWidth}px` } as React.CSSProperties}>
        <div className="message-column" ref={messageColumnRef}>
          <div className="column-head"><div className="column-title"><strong>{t("邮件列表")}</strong><span className="column-mailbox-meta"><span title={props.account?.email}>{props.account?.email}</span><button type="button" className={mailboxCopied ? "copied" : ""} onClick={() => void copyCurrentMailbox()} aria-label={t(mailboxCopied ? "已复制邮箱" : "复制邮箱")} title={t(mailboxCopied ? "已复制邮箱" : "复制邮箱")}>{mailboxCopied ? <Check size={13} /> : <Copy size={13} />}</button><em>{props.total + props.pendingSends.length} {t("封邮件")}</em></span></div><div className="column-actions"><button disabled={props.fontScale <= 0.9} onClick={() => props.setFontScale((value) => Math.max(0.9, Number((value - 0.1).toFixed(1))))} aria-label={t("减小邮件列表字号")}><Minus size={15} /></button><span className="font-scale-label">{Math.round(props.fontScale * 100)}%</span><button disabled={props.fontScale >= 1.4} onClick={() => props.setFontScale((value) => Math.min(1.4, Number((value + 0.1).toFixed(1))))} aria-label={t("增大邮件列表字号")}><Plus size={15} /></button><button onClick={props.reload} aria-label={t("同步")}><RefreshCw size={16} /></button></div></div>
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
                    onPointerUp={(event) => finishSwipeGesture(message, event)}
                    onPointerCancel={cancelSwipeGesture}
                    onContextMenu={(event) => event.preventDefault()}
                    onClickCapture={(event) => {
                      if (!messageClickBlocked()) return;
                      event.preventDefault();
                      event.stopPropagation();
                    }}
                    onClick={(event) => {
                      if (messageClickBlocked()) {
                        event.preventDefault();
                        event.stopPropagation();
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

        <div
          className="mail-resizer"
          role="separator"
          aria-orientation="vertical"
          aria-label={t("调整邮件列表宽度")}
          aria-valuemin={420}
          aria-valuemax={1200}
          aria-valuenow={Math.round(mailListWidth)}
          tabIndex={0}
          onPointerDown={startMailResize}
          onPointerMove={moveMailResize}
          onPointerUp={finishMailResize}
          onPointerCancel={finishMailResize}
          onDoubleClick={() => {
            const width = clampMailListWidth(window.innerWidth * .55);
            setMailListWidth(width);
            localStorage.setItem("mail-list-column-width", String(Math.round(width)));
          }}
          onKeyDown={(event) => {
            if (event.key === "ArrowLeft") { event.preventDefault(); resizeMailWithKeyboard(-1); }
            if (event.key === "ArrowRight") { event.preventDefault(); resizeMailWithKeyboard(1); }
          }}
        ><span /></div>

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
          <div className="message-action-title">
            <span className={deleting ? "danger" : "success"}>{deleting ? <Trash2 size={18} /> : <Archive size={18} />}</span>
            <h2>{t(deleting ? "确认删除邮件" : "确认归档邮件")}</h2>
          </div>
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

function AccountDeleteConfirmDialog({
  account,
  loading,
  onClose,
  onConfirm,
}: {
  account: Account | null;
  loading: boolean;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const { t } = useI18n();
  if (!account) return null;
  return (
    <div className="message-action-backdrop" onMouseDown={onClose}>
      <section
        className="message-action-sheet"
        role="dialog"
        aria-modal="true"
        aria-label={t("确认删除账号")}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="message-action-copy">
          <div className="message-action-title">
            <span className="danger"><Trash2 size={18} /></span>
            <h2>{t("确认删除账号")}</h2>
          </div>
          <strong>{account.email}</strong>
        </div>
        <button className="message-action-option danger" disabled={loading} onClick={onConfirm}>{loading ? t("处理中…") : t("删除")}</button>
        <button className="message-action-option cancel" disabled={loading} onClick={onClose}>{t("取消")}</button>
      </section>
    </div>
  );
}

function AccountBatchGroupDialog({ open, count, value, loading, onChange, onClose, onConfirm }: { open: boolean; count: number; value: string; loading: boolean; onChange: (value: string) => void; onClose: () => void; onConfirm: () => void }) {
  const { t } = useI18n();
  if (!open) return null;
  return (
    <div className="message-action-backdrop" onMouseDown={onClose}>
      <section className="account-batch-dialog" role="dialog" aria-modal="true" aria-label={t("批量设置分组")} onMouseDown={(event) => event.stopPropagation()}>
        <span className="account-batch-dialog-icon group"><FolderCog size={20} /></span>
        <div><h2>{t("批量设置分组")}</h2><p>{t("为已选择的 {count} 个邮箱设置分组", { count })}</p></div>
        <label><span>{t("分组名称")}</span><input autoFocus value={value} maxLength={80} onChange={(event) => onChange(event.target.value)} placeholder={t("例如：工作、个人或项目")} /></label>
        <div className="account-batch-dialog-actions"><button className="button secondary" disabled={loading} onClick={onClose}>{t("取消")}</button><button className="button primary" disabled={loading} onClick={onConfirm}>{loading ? t("处理中…") : t("保存分组")}</button></div>
      </section>
    </div>
  );
}

function AccountBatchDeleteDialog({ open, count, loading, onClose, onConfirm }: { open: boolean; count: number; loading: boolean; onClose: () => void; onConfirm: () => void }) {
  const { t } = useI18n();
  if (!open) return null;
  return (
    <div className="message-action-backdrop" onMouseDown={onClose}>
      <section className="account-batch-dialog compact" role="dialog" aria-modal="true" aria-label={t("批量删除")} onMouseDown={(event) => event.stopPropagation()}>
        <span className="account-batch-dialog-icon danger"><Trash2 size={20} /></span>
        <div><h2>{t("确认批量删除")}</h2><p>{t("将删除已选择的 {count} 个邮箱账号", { count })}</p></div>
        <div className="account-batch-dialog-actions"><button className="button secondary" disabled={loading} onClick={onClose}>{t("取消")}</button><button className="button danger-button" disabled={loading} onClick={onConfirm}>{loading ? t("处理中…") : t("批量删除")}</button></div>
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

function AccountsPage({ accounts, openImport, notify, reload, authorize, requestDelete }: { accounts: Account[]; openImport: () => void; notify: (message: string, type?: "success" | "error") => void; reload: () => void; authorize: (account: Account) => void; requestDelete: (account: Account) => void }) {
  const { language, t } = useI18n();
  const [batchAction, setBatchAction] = useState<"export" | "copy" | "group" | "delete" | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<number>>(() => new Set());
  const [busyAction, setBusyAction] = useState(false);
  const [groupDialogOpen, setGroupDialogOpen] = useState(false);
  const [groupValue, setGroupValue] = useState("");
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [groupFilter, setGroupFilter] = useState("all");
  const [groupMenuOpen, setGroupMenuOpen] = useState(false);
  const [accountPage, setAccountPage] = useState(1);
  const pageSize = 10;
  const groups = useMemo(() => Array.from(new Set(accounts.map((account) => account.group).filter(Boolean))).sort((a, b) => a.localeCompare(b)), [accounts]);
  const filtered = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return accounts.filter((account) => {
      const matchesQuery = !normalized || `${account.email}\n${account.remark || ""}\n${account.group || ""}`.toLocaleLowerCase().includes(normalized);
      const matchesGroup = groupFilter === "all" || (groupFilter === "ungrouped" ? !account.group : account.group === groupFilter.slice(6));
      return matchesQuery && matchesGroup;
    });
  }, [accounts, groupFilter, query]);
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const pageAccounts = filtered.slice((accountPage - 1) * pageSize, accountPage * pageSize);
  const allSelected = filtered.length > 0 && filtered.every((account) => selectedIds.has(account.id));
  const selectedAccounts = accounts.filter((account) => selectedIds.has(account.id));
  const selectionMode = batchAction !== null;
  const groupFilterLabel = groupFilter === "all"
    ? t("全部分组")
    : groupFilter === "ungrouped"
      ? t("未分组")
      : groupFilter.slice(6);

  useEffect(() => {
    const available = new Set(accounts.map((account) => account.id));
    setSelectedIds((current) => new Set(Array.from(current).filter((id) => available.has(id))));
  }, [accounts]);

  useEffect(() => setAccountPage(1), [query, groupFilter]);

  useEffect(() => {
    if (accountPage > pageCount) setAccountPage(pageCount);
  }, [accountPage, pageCount]);

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

  const toggleSelected = (id: number) => {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };

  const cancelBatchMode = () => {
    setBatchAction(null);
    setSelectedIds(new Set());
    setGroupDialogOpen(false);
    setDeleteDialogOpen(false);
  };

  const executeBatchAction = async () => {
    const ids = selectedAccounts.map((account) => account.id);
    if (!batchAction || !ids.length) return;
    if (batchAction === "group") {
      setGroupValue(selectedAccounts.every((account) => account.group === selectedAccounts[0]?.group) ? selectedAccounts[0]?.group || "" : "");
      setGroupDialogOpen(true);
      return;
    }
    if (batchAction === "delete") {
      setDeleteDialogOpen(true);
      return;
    }
    setBusyAction(true);
    try {
      if (batchAction === "export") {
        await downloadAccountsFile(ids);
        notify(t("账号备份已导出"));
      } else {
        const result = await requestAccountsFile(ids);
        await copyPlainText(result.content);
        notify(t("所选账号已复制"));
      }
      cancelBatchMode();
    } catch (error) {
      notify(error instanceof Error ? error.message : t(batchAction === "export" ? "账号导出失败" : "批量复制失败"), "error");
    } finally {
      setBusyAction(false);
    }
  };

  const confirmGroup = async () => {
    setBusyAction(true);
    try {
      await api("/api/accounts/batch/group", { method: "PATCH", body: JSON.stringify({ ids: selectedAccounts.map((account) => account.id), group: groupValue.trim() }) });
      notify(t(groupValue.trim() ? "账号分组已更新" : "账号已移出分组"));
      reload();
      cancelBatchMode();
    } catch (error) {
      notify(error instanceof Error ? error.message : t("设置分组失败"), "error");
    } finally {
      setBusyAction(false);
    }
  };

  const confirmBatchDelete = async () => {
    setBusyAction(true);
    try {
      await api("/api/accounts/batch/delete", { method: "POST", body: JSON.stringify({ ids: selectedAccounts.map((account) => account.id) }) });
      notify(t("所选账号已删除"));
      reload();
      cancelBatchMode();
    } catch (error) {
      notify(error instanceof Error ? error.message : t("批量删除失败"), "error");
    } finally {
      setBusyAction(false);
    }
  };

  const batchActionLabel = batchAction === "export" ? t("导出所选") : batchAction === "copy" ? t("复制所选") : batchAction === "group" ? t("设置分组") : t("删除所选");

  return (
    <>
      <section className="accounts-professional-page">
      <header className="accounts-professional-head"><div><h1>{t("账号管理")}</h1><p>{t("安全管理您的所有邮箱账号")}</p></div><div className="accounts-count"><strong>{accounts.length}</strong><span>{t("邮箱账号")}</span></div></header>

      <div className="accounts-toolbar">
        <label className="accounts-search"><Search size={16} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("搜索邮箱地址或备注…")} /></label>
        <div className="accounts-group-select">
          <button type="button" aria-haspopup="listbox" aria-expanded={groupMenuOpen} aria-label={t("邮箱分组")} onClick={() => setGroupMenuOpen((open) => !open)}><span>{groupFilterLabel}</span><ChevronDown size={15} /></button>
          {groupMenuOpen && <><button type="button" className="accounts-group-dismiss" aria-label={t("关闭")} onClick={() => setGroupMenuOpen(false)} /><div className="accounts-group-options" role="listbox"><button type="button" role="option" aria-selected={groupFilter === "all"} onClick={() => { setGroupFilter("all"); setGroupMenuOpen(false); }}><span>{t("全部分组")}</span>{groupFilter === "all" && <Check size={14} />}</button><button type="button" role="option" aria-selected={groupFilter === "ungrouped"} onClick={() => { setGroupFilter("ungrouped"); setGroupMenuOpen(false); }}><span>{t("未分组")}</span>{groupFilter === "ungrouped" && <Check size={14} />}</button>{groups.map((group) => { const value = `group:${group}`; return <button type="button" role="option" aria-selected={groupFilter === value} key={group} onClick={() => { setGroupFilter(value); setGroupMenuOpen(false); }}><span>{group}</span>{groupFilter === value && <Check size={14} />}</button>; })}</div></>}
        </div>
        <button className="button account-tool import" onClick={openImport}><Upload size={15} /> {t("导入邮箱")}</button>
        <button className={`button account-tool export ${batchAction === "export" ? "active" : ""}`} disabled={!accounts.length || busyAction} onClick={() => setBatchAction("export")}><Download size={15} /> {t("导出备份")}</button>
        <button className={`button account-tool copy ${batchAction === "copy" ? "active" : ""}`} disabled={!accounts.length || busyAction} onClick={() => setBatchAction("copy")}><Copy size={15} /> {t("批量复制")}</button>
        <button className={`button account-tool group ${batchAction === "group" ? "active" : ""}`} disabled={!accounts.length || busyAction} onClick={() => setBatchAction("group")}><FolderCog size={15} /> {t("批量设置分组")}</button>
        <button className={`button account-tool delete ${batchAction === "delete" ? "active" : ""}`} disabled={!accounts.length || busyAction} onClick={() => setBatchAction("delete")}><Trash2 size={15} /> {t("批量删除")}</button>
      </div>

      {selectionMode && <div className={`account-batch-selection ${batchAction}`}><div><button type="button" onClick={() => setSelectedIds(allSelected ? new Set() : new Set(filtered.map((account) => account.id)))}><span className={allSelected ? "selection-box checked" : "selection-box"}>{allSelected && <Check size={12} />}</span>{t(allSelected ? "取消全选" : "全选")}</button><span>{t("已选择 {count} 个账号", { count: selectedIds.size })}</span></div><div><button type="button" className="button secondary" onClick={cancelBatchMode}>{t("取消")}</button><button type="button" className="button batch-run" disabled={!selectedIds.size || busyAction} onClick={() => void executeBatchAction()}>{busyAction ? t("处理中…") : `${batchActionLabel} (${selectedIds.size})`}</button></div></div>}

      <div className={`professional-accounts-table ${selectionMode ? "export-mode" : ""}`}>
        <div className="professional-account-head">{selectionMode && <span /> }<span>#</span><span>{t("邮箱地址")}</span><span>{t("连接状态")}</span><span>{t("权限类型")}</span><span>{t("最后同步")}</span><span>{t("操作")}</span></div>
        {pageAccounts.map((account, index) => (
          <div className="professional-account-row" key={account.id}>
            {selectionMode && <button type="button" className="account-check" onClick={() => toggleSelected(account.id)} aria-label={t(selectedIds.has(account.id) ? "取消选择 {email}" : "选择 {email}", { email: account.email })}><span className={selectedIds.has(account.id) ? "selection-box checked" : "selection-box"}>{selectedIds.has(account.id) && <Check size={12} />}</span></button>}
            <span className="account-index">{(accountPage - 1) * pageSize + index + 1}</span>
            <div className="professional-account-identity"><span className="account-avatar">{account.email.slice(0, 1).toUpperCase()}</span><div><strong>{account.email}</strong><small>{account.group ? `${account.group} · ` : ""}{account.remark || t("未添加备注")}</small></div></div>
            <span className={account.lastSyncAt ? "account-status synced" : "account-status pending"}>{account.lastSyncAt ? <Check size={13} /> : <RefreshCw size={13} />}{t(account.lastSyncAt ? "正常" : "待检测")}</span>
            <span className="account-permission"><LockKeyhole size={13} /> OAuth2</span>
            <time>{formatDate(account.lastSyncAt, true, language === "en" ? "en-US" : "zh-CN")}</time>
            <div className="professional-account-actions"><button onClick={() => test(account)}><RefreshCw size={15} /> {t("测试")}</button><button onClick={() => authorize(account)}><KeyRound size={15} /> {t("授权")}</button><button className="danger" aria-label={t("删除 {email}", { email: account.email })} title={t("删除 {email}", { email: account.email })} onClick={() => requestDelete(account)}><Trash2 size={15} /></button></div>
          </div>
        ))}
        {!pageAccounts.length && <div className="empty-table"><Users size={24} /><span>{accounts.length ? t("没有匹配的邮箱账号") : t("还没有导入邮箱账号")}</span>{!accounts.length && <button className="button secondary" onClick={openImport}>{t("立即导入")}</button>}</div>}
        <footer className="accounts-pagination"><span>{t("共 {count} 个账号", { count: filtered.length })}</span><div><button disabled={accountPage <= 1} onClick={() => setAccountPage((page) => Math.max(1, page - 1))}><ChevronLeft size={15} /></button><strong>{accountPage}</strong><span>/ {pageCount}</span><button disabled={accountPage >= pageCount} onClick={() => setAccountPage((page) => Math.min(pageCount, page + 1))}><ChevronRight size={15} /></button></div></footer>
      </div>
      </section>
      <AccountBatchGroupDialog open={groupDialogOpen} count={selectedIds.size} value={groupValue} loading={busyAction} onChange={setGroupValue} onClose={() => { if (!busyAction) setGroupDialogOpen(false); }} onConfirm={() => void confirmGroup()} />
      <AccountBatchDeleteDialog open={deleteDialogOpen} count={selectedIds.size} loading={busyAction} onClose={() => { if (!busyAction) setDeleteDialogOpen(false); }} onConfirm={() => void confirmBatchDelete()} />
    </>
  );
}

function AdminOverviewPage() {
  const { t } = useI18n();
  const [stats, setStats] = useState<AdminStats | null>(null);
  const [activity, setActivity] = useState<AdminActivityPoint[]>([]);
  const [period, setPeriod] = useState<7 | 14 | 30>(30);
  const [metric, setMetric] = useState<"users" | "accounts" | "guests" | "announcements">("accounts");
  const [error, setError] = useState("");
  useEffect(() => {
    Promise.all([api<AdminStats>("/api/admin/stats"), api<{ activity: AdminActivityPoint[] }>("/api/admin/activity?days=30")])
      .then(([nextStats, nextActivity]) => { setStats(nextStats); setActivity(nextActivity.activity); })
      .catch((reason) => setError(reason instanceof Error ? reason.message : t("无法读取管理数据")));
  }, [t]);
  const cards = [
    { label: t("注册用户"), value: stats?.users, icon: Users },
    { label: t("邮箱账号"), value: stats?.mailboxAccounts, icon: Mail },
    { label: t("活跃游客"), value: stats?.activeGuests, icon: UserRound },
    { label: t("已发布公告"), value: stats?.announcements, icon: CircleAlert },
  ];
  const visibleActivity = activity.slice(-period);
  const metricOptions: Array<{ key: typeof metric; label: string; value: number | undefined }> = [
    { key: "users", label: t("注册用户"), value: stats?.users },
    { key: "accounts", label: t("邮箱账号"), value: stats?.mailboxAccounts },
    { key: "guests", label: t("活跃游客"), value: stats?.activeGuests },
    { key: "announcements", label: t("已发布公告"), value: stats?.announcements },
  ];
  const maxValue = Math.max(1, ...visibleActivity.map((point) => point[metric]));
  return (
    <section className="admin-professional-page">
      <header className="accounts-professional-head"><div><h1>{t("管理概览")}</h1><p>{t("查看用户、邮箱账号与站点运行摘要")}</p></div></header>
      {error && <div className="login-error"><CircleAlert size={15} />{error}</div>}
      <div className="admin-stat-grid">{cards.map(({ label, value, icon: Icon }) => <div key={label} className="admin-stat-card"><div><span>{label}</span><strong>{value ?? "—"}</strong></div><Icon size={20} /></div>)}</div>
      <div className="admin-data-panel">
        <header><div><h2>{t("数据趋势")}</h2><p>{t("查看最近一段时间的新增数据")}</p></div><select value={period} onChange={(event) => setPeriod(Number(event.target.value) as 7 | 14 | 30)}><option value={7}>{t("最近 7 天")}</option><option value={14}>{t("最近 14 天")}</option><option value={30}>{t("最近 30 天")}</option></select></header>
        <div className="admin-metric-tabs">{metricOptions.map((option) => <button key={option.key} className={metric === option.key ? "active" : ""} onClick={() => setMetric(option.key)}><span>{option.label}</span><strong>{option.value ?? "—"}</strong></button>)}</div>
        <div className="admin-chart" aria-label={t("数据趋势图")}>
          {visibleActivity.map((point) => <div className="admin-chart-column" key={point.date} title={`${point.date}: ${point[metric]}`}><div><i style={{ height: `${Math.max(point[metric] ? 6 : 1.5, point[metric] / maxValue * 100)}%` }} /></div><span>{point.date.slice(5)}</span></div>)}
        </div>
      </div>
    </section>
  );
}

function AdminUsersPage() {
  const { language, t } = useI18n();
  const [users, setUsers] = useState<AdminUserSummary[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => {
    api<{ users: AdminUserSummary[] }>("/api/admin/users").then((result) => setUsers(result.users)).catch((reason) => setError(reason instanceof Error ? reason.message : t("无法读取用户列表"))).finally(() => setLoading(false));
  }, [t]);
  const filtered = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return normalized ? users.filter((user) => `${user.username}\n${user.email}`.toLocaleLowerCase().includes(normalized)) : users;
  }, [query, users]);
  return (
    <section className="admin-professional-page">
      <header className="accounts-professional-head"><div><h1>{t("用户管理")}</h1><p>{t("仅显示安全的用户摘要，不包含密码或邮箱令牌")}</p></div><div className="accounts-count"><strong>{users.length}</strong><span>{t("注册用户")}</span></div></header>
      <div className="accounts-toolbar admin-users-toolbar"><label className="accounts-search"><Search size={16} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("搜索用户名或登录邮箱…")} /></label></div>
      <div className="admin-users-table">
        <div className="admin-users-head"><span>{t("用户")}</span><span>{t("登录邮箱")}</span><span>{t("角色")}</span><span>{t("邮箱数量")}</span><span>{t("注册时间")}</span></div>
        {loading && <div className="empty-table"><RefreshCw className="spin" size={21} /><span>{t("正在加载用户…")}</span></div>}
        {!loading && error && <div className="empty-table"><CircleAlert size={22} /><span>{error}</span></div>}
        {!loading && !error && filtered.map((user) => <div className="admin-user-row" key={user.id}><div><span className="sidebar-user-avatar" style={{ background: avatarGradient(user.username) }}>{user.username.slice(0, 2).toUpperCase()}</span><strong>{user.username}</strong></div><span>{user.email || "—"}</span><span className={user.administrator ? "admin-role admin" : "admin-role"}>{t(user.administrator ? "管理员" : "普通用户")}</span><strong>{user.accountCount}</strong><time>{formatDate(user.createdAt, true, language === "en" ? "en-US" : "zh-CN")}</time></div>)}
        {!loading && !error && !filtered.length && <div className="empty-table"><Users size={22} /><span>{t("没有匹配的用户")}</span></div>}
      </div>
    </section>
  );
}

function SettingsPage({ authorize }: { authorize: () => void }) {
  const { t } = useI18n();
  return (
    <section className="settings-professional-page">
      <header className="accounts-professional-head"><div><h1>{t("系统设置")}</h1><p>{t("查看存储、安全和微软授权配置。")}</p></div><button className="button account-import-button" onClick={authorize}><KeyRound size={16} /> {t("微软授权工具")}</button></header>
      <div className="settings-professional-list">
        <div className="settings-professional-row"><span className="settings-icon"><Database size={20} /></span><div><h3>{t("SQLite 数据库")}</h3><p>{t("账号、备注和同步时间存储在单个本地数据库文件中。")}</p><code>data/mail.sqlite</code></div><span className="config-status">{t("已启用")}</span></div>
        <div className="settings-professional-row"><span className="settings-icon purple"><ShieldCheck size={20} /></span><div><h3>{t("凭据加密")}</h3><p>{t("密码、Client ID 与 Refresh Token 在写入前使用 AES-256-GCM 加密。")}</p><code>data/.master-key</code></div><span className="config-status">{t("已启用")}</span></div>
        <div className="settings-professional-row"><span className="settings-icon green"><Cloud size={20} /></span><div><h3>{t("微软邮件连接")}</h3><p>{t("收件使用 IMAP XOAUTH2，发件使用 SMTP OAuth2，不启用过时的基本认证。")}</p><code>IMAP 993 · SMTP 587</code></div><span className="config-status">OAuth2</span></div>
        <div className="settings-professional-row permission"><span className="settings-icon purple"><KeyRound size={20} /></span><div><h3>{t("发件权限说明")}</h3><p>{t("从部分旧工具取得的令牌仅包含 IMAP 权限，能够收件但不能发件。使用内置授权工具重新申请令牌时，会同时请求 IMAP、SMTP、Graph 和离线刷新权限。")}</p></div><button className="button secondary" onClick={authorize}>{t("重新授权")}</button></div>
      </div>
    </section>
  );
}

function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[character] || character);
}

export default App;
