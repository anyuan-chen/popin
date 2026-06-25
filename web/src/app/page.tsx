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
        <p className="text-black/60">Loading...</p>
      </div>
    );
  }

  if (!user) return null;

  return (
    <div className="flex flex-1 flex-col">
      <header className="flex items-center justify-between border-b border-black px-6 py-3">
        <span className="text-lg font-semibold">Popin</span>
        <button
          onClick={async () => {
            await authApi.logout();
            router.push("/login");
          }}
          className="bg-black px-3 py-1.5 text-xs text-white hover:bg-black/80"
        >
          Logout
        </button>
      </header>
      <div className="flex flex-1 items-center justify-center">
        <p className="text-sm text-black/60">
          Logged in as <span className="text-black">{user.username}</span>. You&apos;ll receive incoming calls in a new tab.
        </p>
      </div>
    </div>
  );
}