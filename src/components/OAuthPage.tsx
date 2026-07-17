import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Check, CheckCircle2, ChevronDown, Copy, ExternalLink, KeyRound, LoaderCircle, LockKeyhole, Mail, RotateCcw, ShieldCheck } from "lucide-react";
import { api, type Account } from "../api";
import { useI18n } from "../i18n";

interface DeviceCodeResponse {
  device_code: string;
  user_code: string;
  verification_uri: string;
  expires_in: number;
  interval: number;
}

interface PollResponse {
  pending: boolean;
  slowDown?: boolean;
  refreshToken?: string;
}

export function OAuthPage({ accounts, initialAccount, notify, onBack, onAccountSelected }: {
  accounts: Account[];
  initialAccount: Account | null;
  notify: (message: string, type?: "success" | "error") => void;
  onBack: () => void;
  onAccountSelected: (account: Account) => void;
}) {
  const { t } = useI18n();
  const [accountId, setAccountId] = useState<number | null>(initialAccount?.id || accounts[0]?.id || null);
  const [clientId, setClientId] = useState(() => localStorage.getItem("mail-oauth-client-id") || "");
  const [device, setDevice] = useState<DeviceCodeResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [polling, setPolling] = useState(false);
  const [completed, setCompleted] = useState(false);
  const [copied, setCopied] = useState(false);
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const cancelled = useRef(false);
  const selectedAccount = accounts.find((account) => account.id === accountId) || null;
  const step = completed ? 3 : device ? 2 : 1;

  useEffect(() => {
    cancelled.current = false;
    return () => { cancelled.current = true; };
  }, []);

  useEffect(() => {
    if (initialAccount && accounts.some((account) => account.id === initialAccount.id)) setAccountId(initialAccount.id);
  }, [accounts, initialAccount]);

  const poll = async (current: DeviceCodeResponse, targetAccountId: number, requestedClientId: string) => {
    setPolling(true);
    const deadline = Date.now() + current.expires_in * 1000;
    let wait = Math.max(current.interval || 5, 5) * 1000;
    while (!cancelled.current && Date.now() < deadline) {
      await new Promise((resolve) => window.setTimeout(resolve, wait));
      if (cancelled.current) return;
      try {
        const result = await api<PollResponse>("/api/oauth/poll", {
          method: "POST",
          body: JSON.stringify({ clientId: requestedClientId, deviceCode: current.device_code }),
        });
        if (result.pending) {
          if (result.slowDown) wait += 5000;
          continue;
        }
        if (result.refreshToken) {
          await api(`/api/accounts/${targetAccountId}/token`, {
            method: "PUT",
            body: JSON.stringify({ refreshToken: result.refreshToken }),
          });
          setPolling(false);
          setCompleted(true);
          notify("微软授权成功，账号令牌已安全更新");
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

  const start = async () => {
    if (!selectedAccount) return;
    const requestedClientId = clientId.trim();
    setLoading(true);
    try {
      localStorage.setItem("mail-oauth-client-id", requestedClientId);
      const next = await api<DeviceCodeResponse>("/api/oauth/device-code", {
        method: "POST",
        body: JSON.stringify({ clientId: requestedClientId }),
      });
      setDevice(next);
      void poll(next, selectedAccount.id, requestedClientId);
    } catch (error) {
      notify(error instanceof Error ? error.message : "获取授权码失败", "error");
    } finally {
      setLoading(false);
    }
  };

  const reset = () => {
    cancelled.current = true;
    window.setTimeout(() => { cancelled.current = false; }, 0);
    setDevice(null);
    setPolling(false);
    setCompleted(false);
    setCopied(false);
  };

  const copyCode = async () => {
    if (!device) return;
    await navigator.clipboard.writeText(device.user_code);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <section className="oauth-page">
      <header className="oauth-page-head">
        <div><h1>{t("微软授权工具")}</h1><p>{t("以设备代码流程安全申请收件、发件和离线刷新权限。")}</p></div>
        <button type="button" className="button secondary" onClick={onBack}><ArrowLeft size={16} /> {t("返回设置")}</button>
      </header>

      <div className="oauth-steps" aria-label={t("授权进度")}>
        {[t("选择账号"), t("微软验证"), t("完成授权")].map((label, index) => <div className={step > index ? "active" : ""} key={label}><span>{step > index + 1 ? <Check size={14} /> : index + 1}</span><strong>{label}</strong></div>)}
      </div>

      <div className="oauth-layout">
        <div className="oauth-main-card">
          {!device && !completed && <>
            <div className="oauth-card-heading"><span><KeyRound size={20} /></span><div><h2>{t("配置授权请求")}</h2><p>{t("选择需要更新的邮箱，并填写 Microsoft 公共客户端 ID。")}</p></div></div>
            <label className="oauth-field"><span>{t("目标邮箱")}</span><div className="oauth-account-select"><button type="button" aria-haspopup="listbox" aria-expanded={accountMenuOpen} onClick={() => setAccountMenuOpen((open) => !open)}><span>{selectedAccount?.email || t("选择邮箱账号")}</span><ChevronDown size={15} /></button>{accountMenuOpen && <><button type="button" className="oauth-select-dismiss" aria-label={t("关闭")} onClick={() => setAccountMenuOpen(false)} /><div className="oauth-account-options" role="listbox">{accounts.map((account) => <button type="button" role="option" aria-selected={account.id === accountId} key={account.id} onClick={() => { setAccountId(account.id); onAccountSelected(account); setAccountMenuOpen(false); }}><span>{account.email}</span>{account.id === accountId && <Check size={14} />}</button>)}</div></>}</div></label>
            <label className="oauth-field"><span>Client ID</span><input value={clientId} onChange={(event) => setClientId(event.target.value)} placeholder={t("粘贴 Microsoft 公共客户端 ID")} /></label>
            <div className="oauth-security-note"><ShieldCheck size={17} /><div><strong>{t("安全设备代码流程")}</strong><span>{t("无需在 Mail 中输入微软密码，授权令牌会加密写入 SQLite。")}</span></div></div>
            <button className="button oauth-start-button" disabled={!selectedAccount || clientId.trim().length < 8 || loading} onClick={() => void start()}><LockKeyhole size={17} /> {loading ? t("正在获取…") : t("生成授权验证码")}</button>
          </>}

          {device && !completed && <div className="oauth-verification"><span className="oauth-microsoft-mark"><Mail size={25} /></span><h2>{t("在微软页面完成验证")}</h2><p>{t("打开微软授权页面，并输入下面的设备验证码。")}</p><strong className="oauth-device-code">{device.user_code}</strong><div className="oauth-code-actions"><button className="button secondary" onClick={() => void copyCode()}>{copied ? <Check size={16} /> : <Copy size={16} />} {t(copied ? "已复制" : "复制验证码")}</button><a className="button oauth-open-button" href={device.verification_uri} target="_blank" rel="noreferrer">{t("打开微软授权")} <ExternalLink size={15} /></a></div>{polling && <span className="oauth-waiting"><LoaderCircle className="spin" size={16} /> {t("正在等待微软确认，请勿关闭此页面…")}</span>}</div>}

          {completed && <div className="oauth-complete"><span><CheckCircle2 size={30} /></span><h2>{t("授权已完成")}</h2><p>{t("新的 Refresh Token 已加密保存，账号现在可以重新测试收件和发件权限。")}</p><button className="button secondary" onClick={reset}><RotateCcw size={16} /> {t("重新授权其他账号")}</button></div>}
        </div>
      </div>
    </section>
  );
}
