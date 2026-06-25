export type User = {
  id: number;
  username: string;
};

// Returned by /api/auth/login when called with kind:"daemon". The web app
// does NOT keep this token; it forwards it via the redirect query string to
// the daemon's localhost callback server.
export type DaemonLoginResponse = User & {
  token: string;
  kind: "daemon";
};

async function api<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const opts: RequestInit = {
    method,
    credentials: "include",
    headers: { "Content-Type": "application/json" },
  };
  if (body) {
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opts);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    throw new Error((data as { error?: string }).error || "Request failed");
  }
  return data as T;
}

export const authApi = {
  register: (username: string, password: string) =>
    api<User>("POST", "/api/auth/register", {
      username,
      password,
    }),
  // kind defaults to "browser". kind:"daemon" mints a daemon token that is
  // returned in the JSON body (no cookie set); the caller must arrange for
  // that token to reach the daemon, in our case via the `redirect` flow.
  login: (
    username: string,
    password: string,
    options?: { kind?: "browser" | "daemon"; redirect?: string },
  ) => {
    const body: Record<string, string> = { username, password };
    if (options?.kind) body.kind = options.kind;
    if (options?.redirect) body.redirect = options.redirect;
    return api<User | DaemonLoginResponse>("POST", "/api/auth/login", body);
  },
  logout: () => api<{ status: string }>("POST", "/api/auth/logout"),
  me: () => api<User>("GET", "/api/auth/me"),
};