"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { authApi, type DaemonLoginResponse } from "@/lib/api";

function isDaemonRedirect(redirect: string | null): redirect is string {
  if (!redirect) return false;
  try {
    const u = new URL(redirect);
    return (
      (u.protocol === "http:" || u.protocol === "https:") &&
      (u.hostname === "localhost" || u.hostname === "127.0.0.1")
    );
  } catch {
    return false;
  }
}

export default function LoginPage() {
  return (
    <Suspense fallback={<LoginFallback />}>
      <LoginForm />
    </Suspense>
  );
}

function LoginFallback() {
  return (
    <div className="flex flex-1 items-center justify-center">
      <p className="text-neutral-500">Loading...</p>
    </div>
  );
}

function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const redirect = searchParams.get("redirect");
  const isDaemonFlow = isDaemonRedirect(redirect);

  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);

    try {
      if (mode === "register") {
        await authApi.register(username, password);
      }

      if (isDaemonFlow && redirect) {
        // Daemon authorization flow: mint a daemon-kind token then forward it
        // back to the daemon's localhost callback via the redirect URL. The
        // daemon's local server captures ?token= and writes it to disk.
        const res = (await authApi.login(username, password, {
          kind: "daemon",
          redirect,
        })) as DaemonLoginResponse;
        const target = new URL(redirect);
        target.searchParams.set("token", res.token);
        window.location.href = target.toString();
        return;
      }

      await authApi.login(username, password);
      router.push("/room");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
      setLoading(false);
    }
  }

  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <div className="w-full max-w-sm rounded-2xl bg-neutral-900 p-8">
        <h1 className="text-2xl font-bold">Popin</h1>
        <p className="mt-1 text-sm text-neutral-400">
          {isDaemonFlow
            ? "Authorize the Popin daemon"
            : mode === "login"
              ? "Sign in to start a call"
              : "Create an account"}
        </p>

        {isDaemonFlow && (
          <p className="mt-2 rounded-lg bg-blue-950/50 px-3 py-2 text-xs text-blue-300">
            Authorizing the Popin CLI daemon on your machine. Login to approve.
          </p>
        )}

        <form onSubmit={handleSubmit} className="mt-6 space-y-3">
          <input
            type="text"
            placeholder="Username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            className="w-full rounded-lg bg-neutral-800 px-4 py-3 text-sm outline-none ring-neutral-700 ring-1 focus:ring-blue-500"
          />
          <input
            type="password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            className="w-full rounded-lg bg-neutral-800 px-4 py-3 text-sm outline-none ring-neutral-700 ring-1 focus:ring-blue-500"
          />
          {error && <p className="text-sm text-red-400">{error}</p>}

          <button
            type="submit"
            disabled={loading}
            className="w-full rounded-lg bg-blue-600 px-4 py-3 text-sm font-medium text-white hover:bg-blue-500 disabled:opacity-50"
          >
            {loading
              ? "..."
              : isDaemonFlow
                ? "Authorize daemon"
                : mode === "login"
                  ? "Login"
                  : "Register & Login"}
          </button>
        </form>

        {!isDaemonFlow && (
          <button
            onClick={() => {
              setMode(mode === "login" ? "register" : "login");
              setError("");
            }}
            className="mt-4 w-full text-sm text-neutral-400 hover:text-neutral-200"
          >
            {mode === "login"
              ? "Need an account? Register"
              : "Already have an account? Login"}
          </button>
        )}
      </div>
    </div>
  );
}