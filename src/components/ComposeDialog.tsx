import { useEffect, useState } from "react";
import { Send, UserRound } from "lucide-react";
import { type Account } from "../api";
import { Modal } from "./Modal";
import { useI18n } from "../i18n";

export function ComposeDialog({
  open,
  onClose,
  onSend,
  accounts,
  initialAccountId,
}: {
  open: boolean;
  onClose: () => void;
  onSend: (draft: { accountId: number; to: string; cc: string; subject: string; text: string }) => void;
  accounts: Account[];
  initialAccountId: number | null;
}) {
  const { t } = useI18n();
  const [accountId, setAccountId] = useState<number | null>(initialAccountId);
  const [to, setTo] = useState("");
  const [cc, setCc] = useState("");
  const [subject, setSubject] = useState("");
  const [text, setText] = useState("");

  useEffect(() => setAccountId(initialAccountId), [initialAccountId, open]);

  const send = () => {
    if (!accountId) return;
    onSend({ accountId, to: to.trim(), cc: cc.trim(), subject: subject.trim(), text });
    setTo("");
    setCc("");
    setSubject("");
    setText("");
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("写邮件")}
      description={t("使用已导入的 Outlook / Hotmail 账号发送")}
      wide
    >
      <div className="compose-form">
        <label className="field-row">
          <span>{t("发件人")}</span>
          <select
            value={accountId || ""}
            onChange={(event) => setAccountId(Number(event.target.value))}
          >
            <option value="" disabled>{t("选择邮箱账号")}</option>
            {accounts.map((account) => (
              <option key={account.id} value={account.id}>{account.email}</option>
            ))}
          </select>
        </label>
        <label className="field-row">
          <span>{t("收件人")}</span>
          <input value={to} onChange={(event) => setTo(event.target.value)} placeholder="name@example.com" />
        </label>
        <label className="field-row">
          <span>{t("抄送")}</span>
          <input value={cc} onChange={(event) => setCc(event.target.value)} placeholder={t("可选，多个地址使用逗号分隔")} />
        </label>
        <label className="field-row">
          <span>{t("主题")}</span>
          <input value={subject} onChange={(event) => setSubject(event.target.value)} placeholder={t("邮件主题")} />
        </label>
        <textarea
          className="compose-editor"
          value={text}
          onChange={(event) => setText(event.target.value)}
          placeholder={t("输入邮件正文…")}
        />
      </div>
      <footer className="modal-footer compose-footer">
        <span className="secure-hint"><UserRound size={15} /> {t("凭据仅由本机服务读取")}</span>
        <div>
          <button className="button secondary" onClick={onClose}>{t("保存草稿")}</button>
          <button
            className="button primary"
            disabled={!accountId || !to.trim() || !text.trim()}
            onClick={send}
          >
            <Send size={16} /> {t("发送邮件")}
          </button>
        </div>
      </footer>
    </Modal>
  );
}
