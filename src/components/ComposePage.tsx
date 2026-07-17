import { useEffect, useMemo, useRef, useState } from "react";
import {
  ALargeSmall,
  AlignCenter,
  AlignLeft,
  AlignRight,
  ArrowLeft,
  Bold,
  CalendarDays,
  Check,
  CheckCircle2,
  ChevronDown,
  Eraser,
  Eye,
  FileUp,
  Highlighter,
  Image,
  Italic,
  Link2,
  List,
  ListOrdered,
  MoreHorizontal,
  Palette,
  Paperclip,
  PenLine,
  Redo2,
  Save,
  Send,
  Settings2,
  ShieldCheck,
  Smile,
  Strikethrough,
  Type,
  Undo2,
  UserRound,
  X,
} from "lucide-react";
import { type Account } from "../api";
import { useI18n } from "../i18n";

export type ComposeAttachment = {
  filename: string;
  contentType: string;
  contentBase64: string;
  size: number;
};

type ComposeDraft = {
  accountId: number | null;
  to: string;
  cc: string;
  bcc: string;
  subject: string;
  text: string;
  html: string;
};

const draftStorageKey = "mail-compose-draft";
const maxAttachmentBytes = 3 * 1024 * 1024;
const composeEmojis = [
  "😀", "😃", "😄", "😁", "😆", "😅", "😂", "🤣",
  "😊", "🙂", "🙃", "😉", "😍", "🥰", "😘", "😎",
  "🤔", "🤗", "🤩", "🥳", "😴", "😢", "😭", "😤",
  "👍", "👎", "👏", "🙌", "🙏", "🤝", "💪", "👌",
  "❤️", "🧡", "💛", "💚", "💙", "💜", "✨", "🎉",
  "🔥", "✅", "⭐", "💡", "📌", "📎", "📧", "🚀",
];

function loadDraft(): ComposeDraft {
  try {
    const parsed = JSON.parse(localStorage.getItem(draftStorageKey) || "{}") as Partial<ComposeDraft>;
    return {
      accountId: typeof parsed.accountId === "number" ? parsed.accountId : null,
      to: typeof parsed.to === "string" ? parsed.to : "",
      cc: typeof parsed.cc === "string" ? parsed.cc : "",
      bcc: typeof parsed.bcc === "string" ? parsed.bcc : "",
      subject: typeof parsed.subject === "string" ? parsed.subject : "",
      text: typeof parsed.text === "string" ? parsed.text : "",
      html: typeof parsed.html === "string" ? parsed.html : "",
    };
  } catch {
    return { accountId: null, to: "", cc: "", bcc: "", subject: "", text: "", html: "" };
  }
}

function readAttachment(file: File) {
  return new Promise<ComposeAttachment>((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("附件读取失败"));
    reader.onload = () => {
      const result = String(reader.result || "");
      resolve({
        filename: file.name,
        contentType: file.type || "application/octet-stream",
        contentBase64: result.slice(result.indexOf(",") + 1),
        size: file.size,
      });
    };
    reader.readAsDataURL(file);
  });
}

export function ComposePage({
  accounts,
  initialAccountId,
  onCancel,
  onSend,
  notify,
}: {
  accounts: Account[];
  initialAccountId: number | null;
  onCancel: () => void;
  onSend: (draft: { accountId: number; to: string; cc: string; bcc: string; subject: string; text: string; html: string; attachments: ComposeAttachment[] }) => void;
  notify: (message: string, type?: "success" | "error") => void;
}) {
  const { language, t } = useI18n();
  const initialDraft = useMemo(loadDraft, []);
  const [accountId, setAccountId] = useState<number | null>(initialDraft.accountId || initialAccountId);
  const [to, setTo] = useState(initialDraft.to);
  const [cc, setCc] = useState(initialDraft.cc);
  const [bcc, setBcc] = useState(initialDraft.bcc);
  const [subject, setSubject] = useState(initialDraft.subject);
  const [text, setText] = useState(initialDraft.text);
  const [html, setHtml] = useState(initialDraft.html);
  const [attachments, setAttachments] = useState<ComposeAttachment[]>([]);
  const [ccOpen, setCcOpen] = useState(Boolean(initialDraft.cc));
  const [bccOpen, setBccOpen] = useState(Boolean(initialDraft.bcc));
  const [saved, setSaved] = useState(Boolean(initialDraft.to || initialDraft.subject || initialDraft.text));
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const [commandMenu, setCommandMenu] = useState<"attachment" | "large" | "settings" | null>(null);
  const [commandMenuPosition, setCommandMenuPosition] = useState({ left: 12, top: 60 });
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [emojiMenuPosition, setEmojiMenuPosition] = useState({ left: 12, top: 80 });
  const [formatMoreOpen, setFormatMoreOpen] = useState(false);
  const [formatMorePosition, setFormatMorePosition] = useState({ left: 12, top: 80 });
  const [previewOpen, setPreviewOpen] = useState(false);
  const saveTimerRef = useRef<number | null>(null);
  const editorRef = useRef<HTMLDivElement | null>(null);
  const attachmentInputRef = useRef<HTMLInputElement | null>(null);
  const imageInputRef = useRef<HTMLInputElement | null>(null);
  const attachmentButtonRef = useRef<HTMLButtonElement | null>(null);
  const largeAttachmentButtonRef = useRef<HTMLButtonElement | null>(null);
  const settingsButtonRef = useRef<HTMLButtonElement | null>(null);
  const emojiButtonRef = useRef<HTMLButtonElement | null>(null);
  const formatMoreButtonRef = useRef<HTMLButtonElement | null>(null);
  const selectedAccount = accounts.find((account) => account.id === accountId) || null;
  const canSend = Boolean(accountId && to.trim() && text.trim());

  useEffect(() => {
    if (accountId && accounts.some((account) => account.id === accountId)) return;
    setAccountId(initialAccountId || accounts[0]?.id || null);
  }, [accountId, accounts, initialAccountId]);

  useEffect(() => {
    if (saveTimerRef.current !== null) window.clearTimeout(saveTimerRef.current);
    saveTimerRef.current = window.setTimeout(() => {
      const draft = { accountId, to, cc, bcc, subject, text, html };
      const hasContent = Boolean(to || cc || bcc || subject || text);
      if (hasContent) localStorage.setItem(draftStorageKey, JSON.stringify(draft));
      else localStorage.removeItem(draftStorageKey);
      setSaved(hasContent);
    }, 450);
    return () => {
      if (saveTimerRef.current !== null) window.clearTimeout(saveTimerRef.current);
    };
  }, [accountId, bcc, cc, html, subject, text, to]);

  const syncEditor = () => {
    const editor = editorRef.current;
    if (!editor) return;
    setHtml(editor.innerHTML);
    setText(editor.innerText.replace(/\u00a0/g, " "));
  };

  const format = (command: string, value?: string) => {
    editorRef.current?.focus();
    document.execCommand(command, false, value);
    syncEditor();
  };

  const insertText = (value: string) => {
    editorRef.current?.focus();
    document.execCommand("insertText", false, value);
    syncEditor();
  };

  const addFiles = async (files: FileList | null) => {
    if (!files?.length) return;
    const incoming = Array.from(files);
    const currentSize = attachments.reduce((sum, item) => sum + item.size, 0);
    const incomingSize = incoming.reduce((sum, file) => sum + file.size, 0);
    if (attachments.length + incoming.length > 5 || currentSize + incomingSize > maxAttachmentBytes) {
      notify(t("附件最多 5 个且总大小不能超过 3 MB"), "error");
      return;
    }
    try {
      const nextAttachments = await Promise.all(incoming.map(readAttachment));
      setAttachments((current) => [...current, ...nextAttachments]);
    } catch (error) {
      notify(error instanceof Error ? error.message : t("附件读取失败"), "error");
    }
  };

  const saveDraft = () => {
    localStorage.setItem(draftStorageKey, JSON.stringify({ accountId, to, cc, bcc, subject, text, html }));
    setSaved(true);
    notify(t("草稿已保存在此浏览器"));
  };

  const send = () => {
    if (!accountId || !canSend) return;
    localStorage.removeItem(draftStorageKey);
    setSaved(false);
    onSend({ accountId, to: to.trim(), cc: cc.trim(), bcc: bcc.trim(), subject: subject.trim(), text, html, attachments });
  };

  const insertLink = () => {
    const url = window.prompt(t("输入链接地址"), "https://");
    if (url) format("createLink", url);
  };

  const insertSignature = () => {
    if (!selectedAccount) return;
    insertText(`\n\n${selectedAccount.remark || selectedAccount.email.split("@")[0]}\n${selectedAccount.email}`);
  };

  const toggleEmojiMenu = () => {
    if (emojiOpen) {
      setEmojiOpen(false);
      return;
    }
    const rect = emojiButtonRef.current?.getBoundingClientRect();
    if (rect) {
      const panelWidth = Math.min(286, window.innerWidth - 24);
      setEmojiMenuPosition({
        left: Math.max(12, Math.min(rect.left, window.innerWidth - panelWidth - 12)),
        top: Math.min(rect.bottom + 5, window.innerHeight - 218),
      });
    }
    setEmojiOpen(true);
  };

  const toggleCommandMenu = (menu: "attachment" | "large" | "settings", button: HTMLButtonElement | null) => {
    if (commandMenu === menu) {
      setCommandMenu(null);
      return;
    }
    const rect = button?.getBoundingClientRect();
    if (rect) {
      const panelWidth = Math.min(240, window.innerWidth - 24);
      setCommandMenuPosition({
        left: Math.max(12, Math.min(rect.left, window.innerWidth - panelWidth - 12)),
        top: Math.min(rect.bottom + 5, window.innerHeight - 150),
      });
    }
    setCommandMenu(menu);
  };

  const insertCloudLink = () => {
    const url = window.prompt(t("输入云盘链接"), "https://");
    if (url) insertText(url);
    setCommandMenu(null);
  };

  const toggleFormatMore = () => {
    if (formatMoreOpen) {
      setFormatMoreOpen(false);
      return;
    }
    const rect = formatMoreButtonRef.current?.getBoundingClientRect();
    if (rect) {
      const panelWidth = Math.min(190, window.innerWidth - 24);
      setFormatMorePosition({
        left: Math.max(12, Math.min(rect.right - panelWidth, window.innerWidth - panelWidth - 12)),
        top: Math.min(rect.bottom + 5, window.innerHeight - 245),
      });
    }
    setFormatMoreOpen(true);
  };

  return (
    <section className="compose-page">
      <header className="compose-command-bar">
        <button type="button" className="compose-back-button" onClick={onCancel} aria-label={t("返回收件箱")}><ArrowLeft size={18} /></button>
        <button type="button" className="compose-primary-send" disabled={!canSend} onClick={send}><Send size={16} /> {t("发送")}</button>
        <button type="button" className="compose-command-button" onClick={() => setPreviewOpen(true)}><Eye size={16} /> {t("预览")}</button>
        <div className="compose-settings-menu">
          <button ref={attachmentButtonRef} type="button" className="compose-command-button" aria-expanded={commandMenu === "attachment"} onClick={() => toggleCommandMenu("attachment", attachmentButtonRef.current)}><Paperclip size={16} /> {t("附件")} <ChevronDown size={13} /></button>
          {commandMenu === "attachment" && <><button className="compose-menu-dismiss" aria-label={t("关闭")} onClick={() => setCommandMenu(null)} /><div className="compose-command-popover" style={commandMenuPosition}><button type="button" onClick={() => { setCommandMenu(null); attachmentInputRef.current?.click(); }}><Paperclip size={15} /><span><strong>{t("选择本地附件")}</strong><small>{t("最多 5 个，总计 3 MB")}</small></span></button><button type="button" onClick={() => { setCommandMenu(null); imageInputRef.current?.click(); }}><Image size={15} /><span><strong>{t("添加图片")}</strong><small>JPG · PNG · GIF · WebP</small></span></button></div></>}
        </div>
        <div className="compose-settings-menu">
          <button ref={largeAttachmentButtonRef} type="button" className="compose-command-button" aria-expanded={commandMenu === "large"} onClick={() => toggleCommandMenu("large", largeAttachmentButtonRef.current)}><FileUp size={16} /> {t("超大附件")} <ChevronDown size={13} /></button>
          {commandMenu === "large" && <><button className="compose-menu-dismiss" aria-label={t("关闭")} onClick={() => setCommandMenu(null)} /><div className="compose-command-popover" style={commandMenuPosition}><button type="button" onClick={insertCloudLink}><Link2 size={15} /><span><strong>{t("插入云盘链接")}</strong><small>{t("超大附件请使用云盘链接")}</small></span></button></div></>}
        </div>
        <div className="compose-settings-menu">
          <button ref={settingsButtonRef} type="button" className="compose-command-button" aria-expanded={commandMenu === "settings"} onClick={() => toggleCommandMenu("settings", settingsButtonRef.current)}><Settings2 size={16} /> {t("发送设置")} <ChevronDown size={13} /></button>
          {commandMenu === "settings" && <><button className="compose-menu-dismiss" aria-label={t("关闭")} onClick={() => setCommandMenu(null)} /><div className="compose-settings-popover" style={commandMenuPosition}><span><Check size={14} /> {t("发送后保存到已发送")}</span><span><Check size={14} /> {t("后台发送并显示进度")}</span></div></>}
        </div>
        <span className="compose-command-spacer" />
        {saved && <span className="compose-saved"><CheckCircle2 size={14} /> {t("草稿已自动保存")}</span>}
        <button type="button" className="compose-icon-command" onClick={saveDraft} aria-label={t("保存草稿")} title={t("保存草稿")}><Save size={17} /></button>
      </header>

      <div className="compose-workspace">
        <div className="compose-fields">
          {accounts.length > 1 && <div className="compose-field">
            <span>{t("发件人")}</span>
            <div className="compose-account-select">
              <button type="button" disabled={!accounts.length} aria-haspopup="listbox" aria-expanded={accounts.length > 0 && accountMenuOpen} onClick={() => { if (accounts.length) setAccountMenuOpen((open) => !open); }}><span>{selectedAccount?.email || t("选择邮箱账号")}</span><ChevronDown size={15} /></button>
              {accounts.length > 0 && accountMenuOpen && <><button className="compose-menu-dismiss" aria-label={t("关闭")} onClick={() => setAccountMenuOpen(false)} /><div className="compose-account-options" role="listbox">{accounts.map((account) => <button type="button" role="option" aria-selected={account.id === accountId} key={account.id} onClick={() => { setAccountId(account.id); setAccountMenuOpen(false); }}><span>{account.email}</span>{account.id === accountId && <Check size={14} />}</button>)}</div></>}
            </div>
          </div>}
          <label className="compose-field compose-recipient-field">
            <span>{t("收件人")}</span>
            <input value={to} onChange={(event) => setTo(event.target.value)} placeholder="name@example.com" autoFocus />
            <div className="recipient-extras"><button type="button" className={ccOpen ? "active" : ""} onClick={() => setCcOpen((value) => !value)}>{t("抄送")}</button><button type="button" className={bccOpen ? "active" : ""} onClick={() => setBccOpen((value) => !value)}>{t("密送")}</button></div>
          </label>
          {ccOpen && <label className="compose-field"><span>{t("抄送")}</span><input value={cc} onChange={(event) => setCc(event.target.value)} placeholder={t("可选，多个地址使用逗号分隔")} /></label>}
          {bccOpen && <label className="compose-field"><span>{t("密送")}</span><input value={bcc} onChange={(event) => setBcc(event.target.value)} placeholder={t("可选，多个地址使用逗号分隔")} /></label>}
          <label className="compose-field"><span>{t("主题")}</span><input value={subject} onChange={(event) => setSubject(event.target.value)} placeholder={t("邮件主题")} /></label>
        </div>

        <div className="compose-insert-toolbar">
          <button onClick={() => format("undo")} aria-label={t("撤销")}><Undo2 size={16} /></button>
          <button onClick={() => format("redo")} aria-label={t("重做")}><Redo2 size={16} /></button>
          <span />
          <button onClick={() => imageInputRef.current?.click()}><Image size={16} /> {t("图片")}</button>
          <button onClick={insertLink}><Link2 size={16} /> {t("插入链接")}</button>
          <button onClick={() => attachmentInputRef.current?.click()}><FileUp size={16} /> {t("导入文档")}</button>
          <button onClick={() => insertText(`${new Date().toLocaleDateString(language === "en" ? "en-US" : "zh-CN")} `)}><CalendarDays size={16} /> {t("日程")}</button>
          <div className="compose-emoji-menu">
            <button ref={emojiButtonRef} type="button" aria-expanded={emojiOpen} onClick={toggleEmojiMenu}><Smile size={16} /> {t("表情")}</button>
            {emojiOpen && <><button type="button" className="compose-menu-dismiss" aria-label={t("关闭")} onClick={() => setEmojiOpen(false)} /><div className="compose-emoji-popover" style={emojiMenuPosition} role="dialog" aria-label={t("选择表情")}>{composeEmojis.map((emoji) => <button type="button" key={emoji} onClick={() => { insertText(emoji); setEmojiOpen(false); }} aria-label={emoji}>{emoji}</button>)}</div></>}
          </div>
          <span className="compose-toolbar-spacer" />
          <button onClick={insertSignature}><PenLine size={16} /> {t("签名")} <ChevronDown size={13} /></button>
          <button aria-label={t("更多")}><MoreHorizontal size={17} /></button>
        </div>

        <div className="compose-format-toolbar">
          <button onClick={() => format("removeFormat")} aria-label={t("清除格式")}><Eraser size={16} /></button>
          <span className="compose-format-select"><Type size={15} /> {t("默认字体")} <ChevronDown size={12} /></span>
          <span className="compose-format-select"><ALargeSmall size={15} /> {t("字号")} <ChevronDown size={12} /></span>
          <button onClick={() => format("bold")} aria-label={t("粗体")}><Bold size={16} /></button>
          <button onClick={() => format("italic")} aria-label={t("斜体")}><Italic size={16} /></button>
          <button onClick={() => format("strikeThrough")} aria-label={t("删除线")}><Strikethrough size={16} /></button>
          <button onClick={() => format("foreColor", "#e5484d")} aria-label={t("文字颜色")}><Palette size={16} /></button>
          <button onClick={() => format("hiliteColor", "#fff1a8")} aria-label={t("高亮")}><Highlighter size={16} /></button>
          <button onClick={() => format("justifyLeft")} aria-label={t("左对齐")}><AlignLeft size={16} /></button>
          <button onClick={() => format("justifyCenter")} aria-label={t("居中")}><AlignCenter size={16} /></button>
          <button onClick={() => format("justifyRight")} aria-label={t("右对齐")}><AlignRight size={16} /></button>
          <button onClick={() => format("insertUnorderedList")} aria-label={t("项目符号")}><List size={16} /></button>
          <button onClick={() => format("insertOrderedList")} aria-label={t("编号列表")}><ListOrdered size={16} /></button>
          <button ref={formatMoreButtonRef} onClick={toggleFormatMore} aria-label={t("更多")} aria-expanded={formatMoreOpen}><MoreHorizontal size={17} /></button>
          {formatMoreOpen && <><button type="button" className="compose-menu-dismiss" aria-label={t("关闭")} onClick={() => setFormatMoreOpen(false)} /><div className="compose-format-more-popover" style={formatMorePosition}><button onClick={() => { format("justifyLeft"); setFormatMoreOpen(false); }}><AlignLeft size={15} />{t("左对齐")}</button><button onClick={() => { format("justifyCenter"); setFormatMoreOpen(false); }}><AlignCenter size={15} />{t("居中")}</button><button onClick={() => { format("justifyRight"); setFormatMoreOpen(false); }}><AlignRight size={15} />{t("右对齐")}</button><button onClick={() => { format("insertUnorderedList"); setFormatMoreOpen(false); }}><List size={15} />{t("项目符号")}</button><button onClick={() => { format("insertOrderedList"); setFormatMoreOpen(false); }}><ListOrdered size={15} />{t("编号列表")}</button></div></>}
        </div>

        {attachments.length > 0 && <div className="compose-attachments"><Paperclip size={15} />{attachments.map((attachment, index) => <span key={`${attachment.filename}-${index}`}><strong>{attachment.filename}</strong><small>{Math.max(1, Math.round(attachment.size / 1024))} KB</small><button onClick={() => setAttachments((current) => current.filter((_, itemIndex) => itemIndex !== index))} aria-label={t("移除附件")}><X size={13} /></button></span>)}</div>}

        <div className="compose-editor-head"><strong>{t("邮件正文")}</strong><span>{text.length.toLocaleString()} / 2,000,000</span></div>
        <div ref={editorRef} className="compose-rich-editor" contentEditable suppressContentEditableWarning data-placeholder={t("输入邮件正文…")} dangerouslySetInnerHTML={{ __html: initialDraft.html || initialDraft.text.replace(/\n/g, "<br>") }} onInput={syncEditor} />

        <footer className="compose-page-foot">
          <div className="compose-sender-summary"><span className="compose-sender-avatar"><UserRound size={18} /></span><div><strong>{selectedAccount?.remark || selectedAccount?.email.split("@")[0] || t("未选择邮箱")}</strong><span>{selectedAccount?.email || t("选择发件邮箱后即可发送")}</span></div></div>
          <span className="secure-hint"><ShieldCheck size={15} /> {t("凭据仅由本机服务读取")}</span>
        </footer>
      </div>

      <input ref={attachmentInputRef} className="compose-file-input" type="file" multiple onChange={(event) => { void addFiles(event.target.files); event.target.value = ""; }} />
      <input ref={imageInputRef} className="compose-file-input" type="file" accept="image/*" multiple onChange={(event) => { void addFiles(event.target.files); event.target.value = ""; }} />

      {previewOpen && <div className="modal-backdrop" onMouseDown={() => setPreviewOpen(false)}><section className="compose-preview" role="dialog" aria-modal="true" aria-label={t("邮件预览")} onMouseDown={(event) => event.stopPropagation()}><header><div><span>{t("邮件预览")}</span><h2>{subject || t("无主题")}</h2><p>{t("发送给 {to}", { to: to || "—" })}</p></div><button onClick={() => setPreviewOpen(false)} aria-label={t("关闭预览")}><X size={18} /></button></header><article dangerouslySetInnerHTML={{ __html: html || `<p>${text.replace(/\n/g, "<br>")}</p>` }} />{attachments.length > 0 && <footer><Paperclip size={14} /> {t("{count} 个附件", { count: attachments.length })}</footer>}</section></div>}
    </section>
  );
}
