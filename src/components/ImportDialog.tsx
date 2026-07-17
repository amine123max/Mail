import { useRef, useState } from "react";
import { CheckCircle2, FileText, UploadCloud } from "lucide-react";
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
  const [loading, setLoading] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [fileName, setFileName] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const submit = async (mode: "skip" | "overwrite") => {
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
      setFileName("");
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
    if (!/\.(txt|csv)$/i.test(file.name)) {
      notify(t("仅支持 TXT 或 CSV 文件"), "error");
      return;
    }
    setRaw(await file.text());
    setFileName(file.name);
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("导入邮箱账号")}
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

      <div className="import-body redesigned">
        {tab === "text" ? <>
          <div className="format-note redesigned">
            <strong>{t("支持的格式（每行一个账号）")}</strong>
            <ul><li><code>邮箱地址&lt;TAB&gt;密码&lt;TAB&gt;Client ID&lt;TAB&gt;Refresh Token</code></li><li><code>邮箱地址----密码----Client ID----Refresh Token</code></li></ul>
            <p>{t("四个字段必须完整；字段之间可使用 Tab 键或四个横线分隔。")}</p>
          </div>
          <textarea className="import-textarea redesigned" value={raw} onChange={(event) => { setRaw(event.target.value); setFileName(""); }} placeholder={"请粘贴账号信息，每行一个账号\n例如：\nuser@example.com\tpassword123\tclient_id\trefresh_token\nuser2@example.com----password456----client_id2----refresh_token2"} />
        </> : <>
          <div
            className={`file-drop redesigned ${dragging ? "dragging" : ""} ${fileName ? "ready" : ""}`}
            role="button"
            tabIndex={0}
            onClick={() => fileRef.current?.click()}
            onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") fileRef.current?.click(); }}
            onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
            onDragOver={(event) => { event.preventDefault(); setDragging(true); }}
            onDragLeave={(event) => { if (event.currentTarget === event.target) setDragging(false); }}
            onDrop={(event) => { event.preventDefault(); setDragging(false); void readFile(event.dataTransfer.files?.[0]); }}
          >
            {fileName ? <CheckCircle2 size={42} /> : <UploadCloud size={48} />}
            <strong>{fileName || t("将文件拖到此处，或点击选择文件")}</strong>
            <span>{fileName ? t("文件已读取，可点击重新选择") : t("支持 TXT、CSV 文件")}</span>
            <input ref={fileRef} type="file" accept=".txt,.csv,text/plain,text/csv" hidden onChange={(event) => void readFile(event.target.files?.[0])} />
          </div>
          <div className="file-import-notes"><span>{t("TXT 格式：每行一个账号，使用 Tab 或四个横线分隔")}</span><span><CheckCircle2 size={14} /> {t("导入后按文件中的顺序显示")}</span><span><CheckCircle2 size={14} /> {t("文件内容不会上传到第三方服务")}</span></div>
        </>}
      </div>

      <footer className="modal-footer">
        <button className="button secondary" onClick={onClose}>{t("取消")}</button>
        <button className="button primary" disabled={!raw.trim() || loading} onClick={() => void submit("skip")}>{loading ? t("正在导入…") : t("添加导入")}</button>
        <button className="button import-overwrite-button" disabled={!raw.trim() || loading} onClick={() => void submit("overwrite")}>{t("覆盖导入")}</button>
      </footer>
    </Modal>
  );
}
