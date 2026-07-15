import { useRef, useState } from "react";
import { FileText, UploadCloud } from "lucide-react";
import { api, type Account, ApiError } from "../api";
import { Modal } from "./Modal";
import { useI18n } from "../i18n";

interface ImportResult {
  inserted: number;
  updated: number;
  skipped: number;
  accounts: Account[];
}

export function ImportDialog({
  open,
  onClose,
  onImported,
  notify,
}: {
  open: boolean;
  onClose: () => void;
  onImported: (accounts: Account[]) => void;
  notify: (message: string, type?: "success" | "error") => void;
}) {
  const { t } = useI18n();
  const [tab, setTab] = useState<"text" | "file">("text");
  const [raw, setRaw] = useState("");
  const [mode, setMode] = useState<"skip" | "overwrite">("skip");
  const [loading, setLoading] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const submit = async () => {
    setLoading(true);
    try {
      const result = await api<ImportResult>("/api/accounts/import", {
        method: "POST",
        body: JSON.stringify({ raw, mode }),
      });
      onImported(result.accounts);
      notify(
        `导入完成：新增 ${result.inserted}，更新 ${result.updated}，跳过 ${result.skipped}`,
      );
      setRaw("");
      onClose();
    } catch (error) {
      if (error instanceof ApiError && Array.isArray(error.details)) {
        const first = error.details[0] as { line?: number; message?: string };
        notify(`第 ${first.line || "?"} 行：${first.message || error.message}`, "error");
      } else {
        notify(error instanceof Error ? error.message : "导入失败", "error");
      }
    } finally {
      setLoading(false);
    }
  };

  const readFile = async (file?: File) => {
    if (!file) return;
    setRaw(await file.text());
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("导入邮箱账号")}
      description={t("一次导入一个或多个 Outlook / Hotmail 账号")}
      wide
    >
      <div className="dialog-tabs">
        <button className={tab === "text" ? "active" : ""} onClick={() => setTab("text")}>
          <FileText size={16} /> {t("文本导入")}
        </button>
        <button className={tab === "file" ? "active" : ""} onClick={() => setTab("file")}>
          <UploadCloud size={16} /> {t("文件上传")}
        </button>
      </div>

      <div className="import-body">
        <div className="format-note">
          <strong>{t("支持的格式（每行一个账号）")}</strong>
          <code>邮箱地址&lt;TAB&gt;密码&lt;TAB&gt;Client ID&lt;TAB&gt;Refresh Token</code>
          <code>邮箱地址----密码----Client ID----Refresh Token</code>
          <p>{t("四个字段必须完整；字段之间可使用 Tab 键或四个横线分隔。")}</p>
        </div>

        {tab === "file" && (
          <button className="file-drop" onClick={() => fileRef.current?.click()}>
            <UploadCloud size={26} />
            <span>{raw ? t("文件已读取，可继续更换") : t("选择 TXT / CSV 文件")}</span>
            <small>{t("文件内容不会上传到第三方服务")}</small>
            <input
              ref={fileRef}
              type="file"
              accept=".txt,.csv,text/plain,text/csv"
              hidden
              onChange={(event) => readFile(event.target.files?.[0])}
            />
          </button>
        )}

        <textarea
          className="import-textarea"
          value={raw}
          onChange={(event) => setRaw(event.target.value)}
          placeholder={
            "每行一个账号，例如：\nuser@example.com\tpassword123\tclient_id\trefresh_token\nuser2@example.com----password456----client_id2----refresh_token2"
          }
        />

        <div className="import-options">
          <span>{t("遇到相同邮箱：")}</span>
          <label>
            <input
              type="radio"
              checked={mode === "skip"}
              onChange={() => setMode("skip")}
            />
            {t("跳过已有账号")}
          </label>
          <label>
            <input
              type="radio"
              checked={mode === "overwrite"}
              onChange={() => setMode("overwrite")}
            />
            {t("覆盖凭据")}
          </label>
        </div>
      </div>

      <footer className="modal-footer">
        <button className="button secondary" onClick={onClose}>{t("取消")}</button>
        <button className="button primary" disabled={!raw.trim() || loading} onClick={submit}>
          {loading ? t("正在导入…") : mode === "overwrite" ? t("覆盖导入") : t("添加导入")}
        </button>
      </footer>
    </Modal>
  );
}
