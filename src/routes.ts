export type PageRoute = "inbox" | "accounts" | "settings";
export type FolderRoute = "inbox" | "sent" | "drafts" | "archive" | "trash";
export type DialogRoute = "compose" | "import" | "oauth" | null;
export type MailRouteSegment = "" | FolderRoute | "sendmails" | "accounts" | "import" | "oauth" | "settings";

export type MailRoute = {
  segment: MailRouteSegment;
  page: PageRoute;
  folder: FolderRoute | null;
  dialog: DialogRoute;
  known: boolean;
};

const folderRoutes = new Set<FolderRoute>(["inbox", "sent", "drafts", "archive", "trash"]);

export function routeForSegment(value: string): MailRoute {
  const segment = value.trim().replace(/^\/+|\/+$/g, "").toLowerCase();
  if (!segment) return { segment: "", page: "inbox", folder: "inbox", dialog: null, known: true };
  if (folderRoutes.has(segment as FolderRoute)) {
    const folder = segment as FolderRoute;
    return { segment: folder, page: "inbox", folder, dialog: null, known: true };
  }
  if (segment === "sendmails") return { segment, page: "inbox", folder: null, dialog: "compose", known: true };
  if (segment === "accounts") return { segment, page: "accounts", folder: null, dialog: null, known: true };
  if (segment === "import") return { segment, page: "accounts", folder: null, dialog: "import", known: true };
  if (segment === "oauth") return { segment, page: "settings", folder: null, dialog: "oauth", known: true };
  if (segment === "settings") return { segment, page: "settings", folder: null, dialog: null, known: true };
  return { segment: "", page: "inbox", folder: "inbox", dialog: null, known: false };
}

export function parseMailPath(pathname: string, basePath: string): MailRoute {
  const normalizedBase = basePath === "/" ? "" : basePath.replace(/\/$/, "");
  let relative = pathname;
  if (normalizedBase && (relative === normalizedBase || relative.startsWith(`${normalizedBase}/`))) {
    relative = relative.slice(normalizedBase.length);
  }
  let decoded = relative;
  try {
    decoded = decodeURIComponent(relative);
  } catch {
    // Invalid URL encoding is treated as an unknown route and normalized to the inbox.
  }
  return routeForSegment(decoded.split("/").filter(Boolean)[0] || "");
}

export function mailPath(segment: MailRouteSegment, basePath: string) {
  const normalizedBase = basePath === "/" ? "" : basePath.replace(/\/$/, "");
  return segment ? `${normalizedBase}/${segment}` : `${normalizedBase}/` || "/";
}
