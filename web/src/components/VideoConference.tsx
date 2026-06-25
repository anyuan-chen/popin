"use client";

import {
  LiveKitRoom,
  VideoConference as LiveKitVideoConference,
  RoomAudioRenderer,
  useParticipants,
} from "@livekit/components-react";
import "@livekit/components-styles";
import { useEffect, useRef, useState } from "react";

type Props = {
  token: string;
  serverUrl: string;
  roomName: string;
  onLeave: () => void;
  // Called when another participant joins the room. The room page uses this
  // to flip a "ringing" call into "connected".
  onPeerJoined?: () => void;
};

export function VideoConference({
  token,
  serverUrl,
  roomName,
  onLeave,
  onPeerJoined,
}: Props) {
  const [connected, setConnected] = useState(false);

  return (
    <LiveKitRoom
      token={token}
      serverUrl={serverUrl}
      audio={{
        noiseSuppression: true,
        echoCancellation: true,
        autoGainControl: true,
      }}
      video={true}
      onConnected={() => setConnected(true)}
      onDisconnected={() => onLeave()}
      className="flex flex-1 flex-col"
    >
      <PeerJoinWatcher onPeerJoined={onPeerJoined} />
      <LiveKitVideoConference className="flex-1" />
      <RoomAudioRenderer />

      {!connected && (
        <div className="absolute inset-0 flex items-center justify-center bg-white">
          <p className="text-black/60">Connecting to {roomName}...</p>
        </div>
      )}
    </LiveKitRoom>
  );
}

// PeerJoinWatcher must live inside <LiveKitRoom> so that the room context is
// available. It calls onPeerJoined once (and only once) when a remote
// participant appears.
function PeerJoinWatcher({ onPeerJoined }: { onPeerJoined?: () => void }) {
  const participants = useParticipants();
  const firedRef = useRef(false);
  useEffect(() => {
    if (firedRef.current || !onPeerJoined) return;
    // useParticipants includes the local participant. A "peer joined" is any
    // additional participant beyond the local one.
    if (participants.length > 1) {
      firedRef.current = true;
      onPeerJoined();
    }
  }, [participants, onPeerJoined]);
  return null;
}
