import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ApplicationErrorBoundary } from "./components/ApplicationErrorBoundary";
import { I18nProvider } from "./i18n";
import "./styles.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <I18nProvider><ApplicationErrorBoundary><App /></ApplicationErrorBoundary></I18nProvider>
  </React.StrictMode>,
);
