"use client";

import { Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { VideoConference } from "@/components/VideoConference";

export default function RoomPage() {
  return (
    <Suspense fallback={<Loading />}>
      <RoomInner />
    </Suspense>
  );
}

function Loading() {
  return (
    <div className="flex flex-1 items-center justify-center">
      <p className="text-neutral-500">Loading...</p>
    </div>
  );
}

// The /room page renders an in-progress call from query params supplied by
// the daemon (or any other caller client) when it opens this tab: a LiveKit
// token, room name, and LiveKit URL. There is no in-browser UI for starting
// calls or managing rooms — those flows live in the CLI daemon. Visiting
// /room without a complete set of query params redirects home.
function RoomInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const token = searchParams.get("token");
  const name = searchParams.get("name");
  const livekitUrl = searchParams.get("livekit_url");

  if (!(token && name && livekitUrl)) {
    router.replace("/");
    return <Loading />;
  }

  return (
    <VideoConference
      token={token}
      serverUrl={livekitUrl}
      roomName={name}
      onLeave={() => {
        window.location.href = "/";
      }}
    />
  );
}