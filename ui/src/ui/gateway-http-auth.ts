import { gatewayHttpBase } from "./gateway-url.ts";

let gatewayOrigin = "";
let gatewayCredential = "";
let fetchWrapped = false;

export function setGatewayHttpCredential(host: string, credential: string) {
  const base = gatewayHttpBase(host);
  gatewayOrigin = base ? new URL(base).origin : "";
  gatewayCredential = credential.trim();
}

export function installGatewayHttpAuth() {
  if (fetchWrapped || typeof window === "undefined") {
    return;
  }
  fetchWrapped = true;
  const originalFetch = window.fetch.bind(window);
  window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    if (!gatewayCredential || !gatewayOrigin) {
      return originalFetch(input, init);
    }
    const requestUrl = new URL(
      input instanceof Request ? input.url : input.toString(),
      window.location.href,
    );
    const isProtectedPath =
      requestUrl.pathname.startsWith("/api/") || requestUrl.pathname === "/health";
    if (requestUrl.origin !== gatewayOrigin || !isProtectedPath) {
      return originalFetch(input, init);
    }

    const headers = new Headers(input instanceof Request ? input.headers : undefined);
    new Headers(init?.headers).forEach((value, key) => headers.set(key, value));
    headers.set("X-Gateway-Token", gatewayCredential);
    return originalFetch(input, { ...init, headers });
  };
}

installGatewayHttpAuth();
