export type PageRoute = "login" | "inbox" | "compose" | "accounts" | "oauth" | "settings" | "admin" | "users";
export type FolderRoute = "inbox" | "sent" | "drafts" | "archive" | "trash";
export type DialogRoute = "import" | null;
export type MailRouteSegment = "" | FolderRoute | "sendmails" | "accounts" | "import" | "oauth" | "microsoft-oauth" | "settings" | "admin" | "users";

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
  if (segment === "sendmails") return { segment, page: "compose", folder: null, dialog: null, known: true };
  if (segment === "accounts") return { segment, page: "accounts", folder: null, dialog: null, known: true };
  if (segment === "import") return { segment, page: "accounts", folder: null, dialog: "import", known: true };
  if (segment === "oauth") return { segment, page: "login", folder: null, dialog: null, known: true };
  if (segment === "microsoft-oauth") return { segment, page: "oauth", folder: null, dialog: null, known: true };
  if (segment === "settings") return { segment, page: "settings", folder: null, dialog: null, known: true };
  if (segment === "admin") return { segment, page: "admin", folder: null, dialog: null, known: true };
  if (segment === "users") return { segment, page: "users", folder: null, dialog: null, known: true };
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
    decoded = "";
  }
  return routeForSegment(decoded.split("/").filter(Boolean)[0] || "");
}

export function mailPath(segment: MailRouteSegment, basePath: string) {
  const normalizedBase = basePath === "/" ? "" : basePath.replace(/\/$/, "");
  return segment ? `${normalizedBase}/${segment}` : `${normalizedBase}/` || "/";
}
