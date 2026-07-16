import { useEffect, useState } from "react";
import { CircleAlert, RefreshCw, Send } from "lucide-react";
import { formatDate, type Announcement } from "../api";
import { useI18n } from "../i18n";
import { Modal } from "./Modal";

export function AnnouncementDialog({
  open,
  onClose,
  announcements,
  administrator,
  loading,
  publishing,
  onRefresh,
  onPublish,
}: {
  open: boolean;
  onClose: () => void;
  announcements: Announcement[];
  administrator: boolean;
  loading: boolean;
  publishing: boolean;
  onRefresh: () => void;
  onPublish: (title: string, content: string) => Promise<void>;
}) {
  const { language, t } = useI18n();
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");

  useEffect(() => {
    if (!open) {
      setTitle("");
      setContent("");
    }
  }, [open]);

  const publish = async () => {
    if (!title.trim() || !content.trim()) return;
    await onPublish(title.trim(), content.trim());
    setTitle("");
    setContent("");
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("公告")}
      description={administrator ? t("发布公告并向所有已登录用户推送未读提醒。") : t("查看 Mail 的最新公告。")}
      wide
    >
      <div className="announcement-dialog">
        {administrator && (
          <section className="announcement-compose">
            <div className="announcement-compose-head">
              <span><CircleAlert size={19} /></span>
              <div><strong>{t("发布新公告")}</strong><small>{t("所有已登录用户都将收到未读提醒")}</small></div>
            </div>
            <label>
              <span>{t("公告标题")}</span>
              <input value={title} maxLength={120} onChange={(event) => setTitle(event.target.value)} placeholder={t("输入公告标题")} />
            </label>
            <label>
              <span>{t("公告内容")}</span>
              <textarea value={content} maxLength={4000} onChange={(event) => setContent(event.target.value)} placeholder={t("输入需要推送给用户的公告内容…")} />
            </label>
            <div className="announcement-compose-actions">
              <small>{content.length} / 4000</small>
              <button className="button primary" disabled={!title.trim() || !content.trim() || publishing} onClick={publish}>
                <Send size={15} /> {publishing ? t("发布中…") : t("发布公告")}
              </button>
            </div>
          </section>
        )}

        <section className="announcement-feed">
          <header>
            <div className="announcement-feed-title">
              <span><CircleAlert size={17} /></span>
              <div><strong>{t("全部公告")}</strong><small>{t("共 {count} 条", { count: announcements.length })}</small></div>
            </div>
            <button className="icon-button" onClick={onRefresh} aria-label={t("刷新公告")} title={t("刷新公告")}>
              <RefreshCw className={loading ? "spin" : ""} size={16} />
            </button>
          </header>
          {loading && !announcements.length && <div className="announcement-empty"><RefreshCw className="spin" size={18} /> {t("正在加载公告…")}</div>}
          {!loading && !announcements.length && <div className="announcement-empty"><CircleAlert size={20} /> {t("暂时没有公告")}</div>}
          {announcements.map((announcement) => (
            <article className={announcement.read ? "announcement-item" : "announcement-item unread"} key={announcement.id}>
              <div>
                <strong>{announcement.title}</strong>
                <time>{formatDate(announcement.createdAt, true, language === "en" ? "en-US" : "zh-CN")}</time>
              </div>
              <p>{announcement.content}</p>
              <small>{t("发布人：{author}", { author: announcement.author })}</small>
            </article>
          ))}
        </section>
      </div>
    </Modal>
  );
}
