"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { authApi, type User } from "@/lib/api";

export default function Home() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    authApi
      .me()
      .then(setUser)
      .catch(() => router.push("/login"))
      .finally(() => setLoading(false));
  }, [router]);

  if (loading) {
    return (
      <div className="flex flex-1 items-center justify-center">
        <p className="text-neutral-500">Loading...</p>
      </div>
    );
  }

  if (!user) return null;

  return (
    <div className="flex flex-1 flex-col">
      <header className="flex items-center justify-between border-b border-neutral-800 px-6 py-3">
        <span className="text-lg font-semibold">Popin</span>
        <button
          onClick={async () => {
            await authApi.logout();
            router.push("/login");
          }}
          className="rounded-md bg-neutral-800 px-3 py-1.5 text-xs hover:bg-neutral-700"
        >
          Logout
        </button>
      </header>
      <div className="flex flex-1 items-center justify-center">
        <p className="text-sm text-neutral-500">
          Logged in as <span className="text-neutral-200">{user.username}</span>. You&apos;ll receive incoming calls in a new tab.
        </p>
      </div>
    </div>
  );
}