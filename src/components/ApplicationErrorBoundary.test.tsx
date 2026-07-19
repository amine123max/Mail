import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ApplicationErrorBoundary } from "./ApplicationErrorBoundary";

function BrokenView(): never {
  throw new Error("render failed");
}

describe("ApplicationErrorBoundary", () => {
  beforeEach(() => {
    localStorage.setItem("mail-language", "zh");
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("replaces a broken render with a recoverable screen", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);

    render(
      <I18nProvider>
        <ApplicationErrorBoundary><BrokenView /></ApplicationErrorBoundary>
      </I18nProvider>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("应用需要重新加载");
    expect(screen.getByRole("button", { name: "重新加载应用" })).toBeEnabled();
    expect(consoleError).toHaveBeenCalledWith("Aillive Mail UI render failed");
  });
});
