import { memo, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronDown, ChevronUp, Download, EllipsisVertical, Plus, Search, Trash2, X } from "lucide-react";
import type { Account } from "../api";
import { useI18n } from "../i18n";

type DragVisual = {
  id: number;
  left: number;
  top: number;
  width: number;
  height: number;
};

type DragGesture = {
  id: number;
  pointerId: number;
  pointerType: string;
  startX: number;
  startY: number;
  pointerX: number;
  pointerY: number;
  active: boolean;
  offsetY: number;
  ghostTop: number;
  initialOrder: number[];
  order: number[];
  visibleOrder: number[];
  visibleSlots: number[];
  visibleIndex: number;
  slotCenters: number[];
  scrollContainer: HTMLElement | null;
  scrollStart: number;
  scrollTopEdge: number;
  scrollBottomEdge: number;
  accountsById: Map<number, Account>;
};

type SidebarAccountsProps = {
  accounts: Account[];
  selectedAccountId: number | null;
  onSelect: (accountId: number) => void;
  onImport: () => void;
  onExport: (account: Account) => void | Promise<void>;
  onDelete: (account: Account) => void;
  onOrderChange: (ids: number[]) => void | Promise<void>;
};

const mouseLongPressDelay = 260;
const touchLongPressDelay = 320;
const mouseMovementTolerance = 8;
const touchMovementTolerance = 16;
const autoScrollEdge = 52;
const autoScrollMaximum = 14;

function filterAccounts(accounts: Account[], query: string): Account[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return accounts;
  return accounts.filter((account) => `${account.remark || ""}\n${account.email}`.toLocaleLowerCase().includes(normalized));
}

function accountsFromOrder(order: number[], accountsById: Map<number, Account>): Account[] {
  return order.map((id) => accountsById.get(id)).filter((account): account is Account => Boolean(account));
}

export const SidebarAccounts = memo(function SidebarAccounts({
  accounts,
  selectedAccountId,
  onSelect,
  onImport,
  onExport,
  onDelete,
  onOrderChange,
}: SidebarAccountsProps) {
  const { t } = useI18n();
  const [searchOpen, setSearchOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [collapsed, setCollapsed] = useState(false);
  const [menuId, setMenuId] = useState<number | null>(null);
  const [displayAccounts, setDisplayAccounts] = useState(accounts);
  const [dragVisual, setDragVisual] = useState<DragVisual | null>(null);
  const sectionRef = useRef<HTMLDivElement | null>(null);
  const ghostRef = useRef<HTMLDivElement | null>(null);
  const displayAccountsRef = useRef(accounts);
  const dragTimerRef = useRef<number | null>(null);
  const dragFrameRef = useRef<number | null>(null);
  const dragGestureRef = useRef<DragGesture | null>(null);
  const clickBlockUntilRef = useRef(0);

  const visibleAccounts = useMemo(() => filterAccounts(displayAccounts, search), [displayAccounts, search]);
  const draggedAccount = dragVisual
    ? displayAccounts.find((account) => account.id === dragVisual.id) || accounts.find((account) => account.id === dragVisual.id) || null
    : null;

  useEffect(() => {
    displayAccountsRef.current = displayAccounts;
  }, [displayAccounts]);

  useEffect(() => {
    if (dragGestureRef.current?.active) return;
    displayAccountsRef.current = accounts;
    setDisplayAccounts(accounts);
  }, [accounts]);

  const clearDragTimer = () => {
    if (dragTimerRef.current !== null) window.clearTimeout(dragTimerRef.current);
    dragTimerRef.current = null;
  };

  const clearDragFrame = () => {
    if (dragFrameRef.current !== null) window.cancelAnimationFrame(dragFrameRef.current);
    dragFrameRef.current = null;
  };

  const scheduleDragFrame = () => {
    if (dragFrameRef.current !== null) return;
    dragFrameRef.current = window.requestAnimationFrame(processDragFrame);
  };

  const processDragFrame = () => {
    dragFrameRef.current = null;
    const gesture = dragGestureRef.current;
    if (!gesture?.active) return;

    const ghost = ghostRef.current;
    if (ghost) {
      const translateY = gesture.pointerY - gesture.offsetY - gesture.ghostTop;
      ghost.style.transform = `translate3d(0, ${translateY}px, 0) scale(1.015)`;
    }

    let keepScrolling = false;
    const scrollContainer = gesture.scrollContainer;
    if (scrollContainer) {
      let speed = 0;
      if (gesture.pointerY < gesture.scrollTopEdge + autoScrollEdge) {
        const strength = Math.min(1, (gesture.scrollTopEdge + autoScrollEdge - gesture.pointerY) / autoScrollEdge);
        speed = -autoScrollMaximum * strength;
      } else if (gesture.pointerY > gesture.scrollBottomEdge - autoScrollEdge) {
        const strength = Math.min(1, (gesture.pointerY - (gesture.scrollBottomEdge - autoScrollEdge)) / autoScrollEdge);
        speed = autoScrollMaximum * strength;
      }
      if (speed !== 0) {
        const previous = scrollContainer.scrollTop;
        const maximum = Math.max(0, scrollContainer.scrollHeight - scrollContainer.clientHeight);
        scrollContainer.scrollTop = Math.max(0, Math.min(maximum, previous + speed));
        keepScrolling = Math.abs(scrollContainer.scrollTop - previous) > 0.1;
      }
    }

    const scrollDelta = scrollContainer ? scrollContainer.scrollTop - gesture.scrollStart : 0;
    let nearestIndex = gesture.visibleIndex;
    let nearestDistance = Number.POSITIVE_INFINITY;
    gesture.slotCenters.forEach((center, index) => {
      const distance = Math.abs(gesture.pointerY - (center - scrollDelta));
      if (distance < nearestDistance) {
        nearestDistance = distance;
        nearestIndex = index;
      }
    });

    if (nearestIndex !== gesture.visibleIndex) {
      const nextVisibleOrder = [...gesture.visibleOrder];
      const [movedId] = nextVisibleOrder.splice(gesture.visibleIndex, 1);
      nextVisibleOrder.splice(nearestIndex, 0, movedId);
      const nextOrder = [...gesture.order];
      gesture.visibleSlots.forEach((slot, index) => {
        nextOrder[slot] = nextVisibleOrder[index];
      });
      gesture.visibleOrder = nextVisibleOrder;
      gesture.visibleIndex = nearestIndex;
      gesture.order = nextOrder;
      const nextAccounts = accountsFromOrder(nextOrder, gesture.accountsById);
      displayAccountsRef.current = nextAccounts;
      setDisplayAccounts(nextAccounts);
    }

    if (keepScrolling) scheduleDragFrame();
  };

  const releasePointer = (event: React.PointerEvent<HTMLButtonElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };

  const startDrag = (account: Account, event: React.PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    const row = event.currentTarget.closest<HTMLElement>("[data-account-id]");
    if (!row) return;

    clearDragTimer();
    clearDragFrame();
    event.currentTarget.setPointerCapture(event.pointerId);
    const pointerType = event.pointerType || "mouse";
    const initialOrder = displayAccountsRef.current.map((item) => item.id);
    dragGestureRef.current = {
      id: account.id,
      pointerId: event.pointerId,
      pointerType,
      startX: event.clientX,
      startY: event.clientY,
      pointerX: event.clientX,
      pointerY: event.clientY,
      active: false,
      offsetY: 0,
      ghostTop: 0,
      initialOrder,
      order: initialOrder,
      visibleOrder: [],
      visibleSlots: [],
      visibleIndex: -1,
      slotCenters: [],
      scrollContainer: null,
      scrollStart: 0,
      scrollTopEdge: 0,
      scrollBottomEdge: window.innerHeight,
      accountsById: new Map(displayAccountsRef.current.map((item) => [item.id, item])),
    };

    dragTimerRef.current = window.setTimeout(() => {
      dragTimerRef.current = null;
      const gesture = dragGestureRef.current;
      const section = sectionRef.current;
      if (!gesture || gesture.id !== account.id || !section || !row.isConnected) return;

      const rows = Array.from(section.querySelectorAll<HTMLElement>("[data-account-id]"))
        .map((element) => ({ element, rect: element.getBoundingClientRect() }))
        .sort((left, right) => left.rect.top - right.rect.top);
      const visibleOrder = rows.map(({ element }) => Number(element.dataset.accountId)).filter(Number.isInteger);
      const visibleSet = new Set(visibleOrder);
      const visibleSlots = gesture.order
        .map((id, index) => visibleSet.has(id) ? index : -1)
        .filter((index) => index >= 0);
      const visibleIndex = visibleOrder.indexOf(account.id);
      if (visibleIndex < 0 || visibleSlots.length !== visibleOrder.length) {
        dragGestureRef.current = null;
        return;
      }

      const sourceRect = row.getBoundingClientRect();
      const scrollContainer = section.closest<HTMLElement>(".sidebar-scroll");
      const scrollRect = scrollContainer?.getBoundingClientRect();
      gesture.active = true;
      gesture.offsetY = gesture.pointerY - sourceRect.top;
      gesture.ghostTop = sourceRect.top;
      gesture.visibleOrder = visibleOrder;
      gesture.visibleSlots = visibleSlots;
      gesture.visibleIndex = visibleIndex;
      gesture.slotCenters = rows.map(({ rect }) => rect.top + rect.height / 2);
      gesture.scrollContainer = scrollContainer;
      gesture.scrollStart = scrollContainer?.scrollTop || 0;
      gesture.scrollTopEdge = scrollRect?.top || 0;
      gesture.scrollBottomEdge = scrollRect?.bottom || window.innerHeight;
      clickBlockUntilRef.current = Date.now() + 1000;
      setMenuId(null);
      setDragVisual({
        id: account.id,
        left: sourceRect.left,
        top: sourceRect.top,
        width: sourceRect.width,
        height: sourceRect.height,
      });
      scheduleDragFrame();
    }, pointerType === "mouse" ? mouseLongPressDelay : touchLongPressDelay);
  };

  const moveDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = dragGestureRef.current;
    if (!gesture || gesture.pointerId !== event.pointerId) return;
    const coalesced = event.nativeEvent.getCoalescedEvents?.();
    const latest = coalesced?.length ? coalesced[coalesced.length - 1] : event.nativeEvent;
    gesture.pointerX = latest.clientX;
    gesture.pointerY = latest.clientY;

    if (!gesture.active) {
      const tolerance = gesture.pointerType === "mouse" ? mouseMovementTolerance : touchMovementTolerance;
      if (Math.hypot(gesture.pointerX - gesture.startX, gesture.pointerY - gesture.startY) > tolerance) {
        clearDragTimer();
        dragGestureRef.current = null;
        clickBlockUntilRef.current = Date.now() + 350;
        releasePointer(event);
      }
      return;
    }

    event.preventDefault();
    event.stopPropagation();
    clickBlockUntilRef.current = Date.now() + 1000;
    scheduleDragFrame();
  };

  const finishDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = dragGestureRef.current;
    clearDragTimer();
    clearDragFrame();
    dragGestureRef.current = null;
    releasePointer(event);
    if (!gesture?.active) return;
    event.preventDefault();
    event.stopPropagation();
    clickBlockUntilRef.current = Date.now() + 1000;
    setDragVisual(null);
    void onOrderChange(gesture.order);
  };

  const cancelDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const gesture = dragGestureRef.current;
    clearDragTimer();
    clearDragFrame();
    dragGestureRef.current = null;
    releasePointer(event);
    if (!gesture?.active) return;
    event.preventDefault();
    event.stopPropagation();
    clickBlockUntilRef.current = Date.now() + 1000;
    const reverted = accountsFromOrder(gesture.initialOrder, gesture.accountsById);
    displayAccountsRef.current = reverted;
    setDisplayAccounts(reverted);
    setDragVisual(null);
  };

  useEffect(() => () => {
    clearDragTimer();
    clearDragFrame();
  }, []);

  return (
    <div ref={sectionRef} className={`side-accounts ${dragVisual ? "is-dragging" : ""}`}>
      <div className="side-section-title">
        <span>{t("邮箱账号")}</span>
        <div className="side-section-actions">
          <button
            onClick={() => {
              if (searchOpen) {
                setSearchOpen(false);
                setSearch("");
              } else {
                setCollapsed(false);
                setSearchOpen(true);
              }
            }}
            aria-label={t(searchOpen ? "关闭搜索" : "搜索邮箱账号")}
            title={t(searchOpen ? "关闭搜索" : "搜索邮箱账号")}
          >
            {searchOpen ? <X size={14} /> : <Search size={14} />}
          </button>
          <button onClick={onImport} aria-label={t("导入账号")} title={t("导入账号")}><Plus size={14} /></button>
          <button
            onClick={() => {
              setCollapsed((value) => {
                if (!value) {
                  setSearchOpen(false);
                  setSearch("");
                }
                return !value;
              });
            }}
            aria-expanded={!collapsed}
            aria-label={t(collapsed ? "展开邮箱账号" : "收起邮箱账号")}
            title={t(collapsed ? "展开邮箱账号" : "收起邮箱账号")}
          >
            {collapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
          </button>
        </div>
      </div>

      {searchOpen && !collapsed && (
        <div className="account-search">
          <Search size={14} />
          <input autoFocus value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("搜索邮箱账号")} />
          <button type="button" onClick={() => { setSearchOpen(false); setSearch(""); }} aria-label={t("关闭搜索")} title={t("关闭搜索")}>
            <X size={13} />
          </button>
        </div>
      )}

      {!collapsed && visibleAccounts.map((account) => (
        <div
          key={account.id}
          data-account-id={account.id}
          className={`account-mini ${account.id === selectedAccountId ? "active" : ""} ${dragVisual?.id === account.id ? "drag-source" : ""}`}
        >
          <button
            type="button"
            className="account-mini-main"
            onClick={(event) => {
              if (Date.now() < clickBlockUntilRef.current) {
                event.preventDefault();
                event.stopPropagation();
                return;
              }
              onSelect(account.id);
            }}
          >
            <span className="mini-avatar">{account.email.slice(0, 1).toUpperCase()}</span>
            <span className="account-mini-copy"><strong>{account.remark || account.email.split("@")[0]}</strong><small>{account.email}</small></span>
          </button>
          <button
            type="button"
            className="account-more"
            aria-label={t("账号操作")}
            aria-expanded={menuId === account.id}
            onPointerDown={(event) => startDrag(account, event)}
            onPointerMove={moveDrag}
            onPointerUp={finishDrag}
            onPointerCancel={cancelDrag}
            onContextMenu={(event) => event.preventDefault()}
            onClick={(event) => {
              event.stopPropagation();
              if (Date.now() < clickBlockUntilRef.current) return;
              setMenuId((current) => current === account.id ? null : account.id);
            }}
          ><EllipsisVertical size={16} /></button>
          {menuId === account.id && (
            <>
              <button className="account-menu-dismiss" aria-label={t("关闭账号菜单")} onClick={() => setMenuId(null)} />
              <div className="account-menu" role="menu">
                <button type="button" role="menuitem" className="account-menu-export" onClick={() => { setMenuId(null); void onExport(account); }}><Download size={16} /> {t("导出")}</button>
                <button type="button" role="menuitem" className="account-menu-delete" onClick={() => { setMenuId(null); onDelete(account); }}><Trash2 size={16} /> {t("删除")}</button>
              </div>
            </>
          )}
        </div>
      ))}

      {!collapsed && Boolean(search.trim()) && !visibleAccounts.length && <div className="side-empty">{t("没有匹配的邮箱账号")}</div>}

      {dragVisual && draggedAccount && createPortal(
        <div
          ref={ghostRef}
          className="account-drag-ghost"
          style={{ left: dragVisual.left, top: dragVisual.top, width: dragVisual.width, height: dragVisual.height }}
          aria-hidden="true"
        >
          <span className="mini-avatar">{draggedAccount.email.slice(0, 1).toUpperCase()}</span>
          <span className="account-mini-copy"><strong>{draggedAccount.remark || draggedAccount.email.split("@")[0]}</strong><small>{draggedAccount.email}</small></span>
          <EllipsisVertical size={16} />
        </div>,
        document.body,
      )}
    </div>
  );
});
