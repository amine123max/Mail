import { Component, type PropsWithChildren } from "react";
import { RefreshCw, ShieldAlert } from "lucide-react";
import { useI18n } from "../i18n";

function ApplicationErrorFallback() {
  const { t } = useI18n();

  return (
    <main className="application-error-shell" role="alert">
      <section className="application-error-card">
        <span className="application-error-icon"><ShieldAlert size={22} /></span>
        <div className="application-error-copy">
          <strong>{t("应用需要重新加载")}</strong>
          <p>{t("界面发生异常，邮件数据未被删除。重新加载即可恢复。")}</p>
        </div>
        <button className="button primary" type="button" onClick={() => window.location.reload()}>
          <RefreshCw size={15} /> {t("重新加载应用")}
        </button>
      </section>
    </main>
  );
}

interface ApplicationErrorBoundaryState {
  failed: boolean;
}

export class ApplicationErrorBoundary extends Component<PropsWithChildren, ApplicationErrorBoundaryState> {
  state: ApplicationErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): ApplicationErrorBoundaryState {
    return { failed: true };
  }

  componentDidCatch() {
    console.error("Aillive Mail UI render failed");
  }

  render() {
    return this.state.failed ? <ApplicationErrorFallback /> : this.props.children;
  }
}
