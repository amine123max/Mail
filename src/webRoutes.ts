import { parseMailPath, routeForSegment, type MailRoute } from "./routes";

export function parseWebMailPath(pathname: string, basePath: string): MailRoute {
  const route = parseMailPath(pathname, basePath);
  return route.segment === "" ? routeForSegment("oauth") : route;
}
