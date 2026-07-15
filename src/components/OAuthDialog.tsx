import { useEffect, useRef, useState } from "react";
import { Check, Copy, ExternalLink, KeyRound, LoaderCircle } from "lucide-react";
import { api, type Account } from "../api";
import { Modal } from "./Modal";
import { useI18n } from "../i18n";

interface DeviceCodeResponse {
  device_code: string;
  user_code: string;
  verification_uri: string;
  expires_in: number;
  interval: number;
  message?: string;
}

interface PollResponse {
  pending: boolean;
  slowDown?: boolean;
  refreshToken?: string;
  scope?: string;
}

export function OAuthDialog({
  open,
  onClose,
  account,
  notify,
}: {
  open: boolean;
  onClose: () => void;
  account: Account | null;
  notify: (message: string, type?: "success" | "error") => void;
}) {
  const { t } = useI18n();
  const [clientId, setClientId] = useState("");
  const [device, setDevice] = useState<DeviceCodeResponse | null>(null);
  const [token, setToken] = useState("");
  const [loading, setLoading] = useState(false);
  const [polling, setPolling] = useState(false);
  const cancelled = useRef(false);

  useEffect(() => {
    if (!open) {
      cancelled.current = true;
      setDevice(null);
      setToken("");
      setPolling(false);
    } else {
      cancelled.current = false;
    }
  }, [open]);

  const start = async () => {
    setLoading(true);
    try {
      const next = await api<DeviceCodeResponse>("/api/oauth/device-code", {
        method: "POST",
        body: JSON.stringify({ clientId }),
      });
      setDevice(next);
      window.open(next.verification_uri, "_blank", "noopener,noreferrer");
      void poll(next);
    } catch (error) {
      notify(error instanceof Error ? error.message : "获取授权码失败", "error");
    } finally {
      setLoading(false);
    }
  };

  const poll = async (current: DeviceCodeResponse) => {
    setPolling(true);
    const deadline = Date.now() + current.expires_in * 1000;
    let wait = Math.max(current.interval || 5, 5) * 1000;
    while (!cancelled.current && Date.now() < deadline) {
      await new Promise((resolve) => window.setTimeout(resolve, wait));
      if (cancelled.current) return;
      try {
        const result = await api<PollResponse>("/api/oauth/poll", {
          method: "POST",
          body: JSON.stringify({ clientId, deviceCode: current.device_code }),
        });
        if (result.pending) {
          if (result.slowDown) wait += 5000;
          continue;
        }
        if (result.refreshToken) {
          setToken(result.refreshToken);
          setPolling(false);
          notify("微软授权成功，已获得可收件和发件的令牌");
          return;
        }
      } catch (error) {
        setPolling(false);
        notify(error instanceof Error ? error.message : "授权轮询失败", "error");
        return;
      }
    }
    setPolling(false);
  };

  const applyToken = async () => {
    if (!account || !token) return;
    try {
      await api(`/api/accounts/${account.id}/token`, {
        method: "PUT",
        body: JSON.stringify({ refreshToken: token }),
      });
      notify(`已更新 ${account.email} 的授权令牌`);
      onClose();
    } catch (error) {
      notify(error instanceof Error ? error.message : "更新令牌失败", "error");
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("微软授权工具")}
      description={t("申请 IMAP 收件、SMTP / Graph 发件与离线刷新权限")}
    >
      <div className="oauth-body">
        <label className="stack-field">
          <span>Client ID</span>
          <input
            value={clientId}
            onChange={(event) => setClientId(event.target.value)}
            placeholder="粘贴 Microsoft 公共客户端 ID"
            disabled={Boolean(device)}
          />
        </label>

        {!device ? (
          <button className="button primary full" disabled={clientId.length < 8 || loading} onClick={start}>
            <KeyRound size={16} /> {loading ? t("正在获取…") : t("开始设备授权")}
          </button>
        ) : (
          <div className="device-code-card">
            <span>{t("在微软页面输入验证码")}</span>
            <strong>{device.user_code}</strong>
            <div>
              <button className="button secondary" onClick={() => navigator.clipboard.writeText(device.user_code)}>
                <Copy size={15} /> {t("复制验证码")}
              </button>
              <a className="button primary" href={device.verification_uri} target="_blank" rel="noreferrer">
                {t("打开微软授权")} <ExternalLink size={15} />
              </a>
            </div>
            {polling && <p><LoaderCircle className="spin" size={15} /> {t("等待你在微软页面完成授权…")}</p>}
          </div>
        )}

        {token && (
          <div className="token-success">
            <Check size={18} />
            <div><strong>{t("授权成功")}</strong><span>{t("Refresh Token 已安全接收，不会显示在页面上。")}</span></div>
          </div>
        )}
      </div>
      <footer className="modal-footer">
        <button className="button secondary" onClick={onClose}>{t("关闭")}</button>
        {token && account && <button className="button primary" onClick={applyToken}>应用到 {account.email}</button>}
        {token && !account && (
          <button className="button primary" onClick={() => navigator.clipboard.writeText(token)}>
            <Copy size={15} /> {t("复制 Refresh Token")}
          </button>
        )}
      </footer>
    </Modal>
  );
}
