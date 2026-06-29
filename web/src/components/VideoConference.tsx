"use client";

import {
  LiveKitRoom,
  RoomAudioRenderer,
  LayoutContextProvider,
  useTracks,
  usePinnedTracks,
  useCreateLayoutContext,
  useParticipants,
  GridLayout,
  ParticipantTile,
  FocusLayoutContainer,
  CarouselLayout,
  FocusLayout,
  TrackReferenceOrPlaceholder,
} from "@livekit/components-react";
import { Track, RoomEvent } from "livekit-client";
import "@livekit/components-styles";
import { useEffect, useRef, useState } from "react";
import { BottomBar } from "./BottomBar";

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
      <div className="flex flex-1 flex-col bg-neutral-900">
        <div className="flex-1 p-4">
          <ConferenceStage />
        </div>
        <BottomBar />
      </div>
      <RoomAudioRenderer />

      {!connected && (
        <div className="absolute inset-0 flex items-center justify-center bg-neutral-900">
          <p className="text-white/60">Connecting to {roomName}...</p>
        </div>
      )}
    </LiveKitRoom>
  );
}

function ConferenceStage() {
  const layoutContext = useCreateLayoutContext();
  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { updateOnlyOn: [RoomEvent.ActiveSpeakersChanged], onlySubscribed: false }
  );
  const focusTrack = usePinnedTracks(layoutContext)?.[0];
  const carouselTracks = tracks.filter((t) => t !== focusTrack);

  // Auto-focus a screen share track when it appears.
  const autoPinRef = useRef<TrackReferenceOrPlaceholder | null>(null);
  const screenShareTracks = tracks.filter(
    (t) => t.publication?.source === Track.Source.ScreenShare && t.publication?.isSubscribed
  );
  useEffect(() => {
    if (screenShareTracks.length > 0 && autoPinRef.current === null) {
      layoutContext.pin.dispatch?.({ msg: "set_pin", trackReference: screenShareTracks[0] });
      autoPinRef.current = screenShareTracks[0];
    } else if (
      autoPinRef.current &&
      !screenShareTracks.some(
        (t) => t.publication?.trackSid === autoPinRef.current?.publication?.trackSid
      )
    ) {
      layoutContext.pin.dispatch?.({ msg: "clear_pin" });
      autoPinRef.current = null;
    }
  }, [screenShareTracks, layoutContext.pin]);

  return (
    <LayoutContextProvider value={layoutContext}>
      <div className="relative flex h-full w-full flex-1">
        {focusTrack ? (
          <div className="lk-focus-layout-wrapper" style={{ width: "100%", height: "100%" }}>
            <FocusLayoutContainer>
              {carouselTracks.length > 0 && (
                <CarouselLayout tracks={carouselTracks} orientation="vertical">
                  <ParticipantTile />
                </CarouselLayout>
              )}
              {focusTrack && <FocusLayout trackRef={focusTrack} />}
            </FocusLayoutContainer>
          </div>
        ) : (
          <div className="lk-grid-layout-wrapper flex flex-1 items-center justify-center">
            <GridLayout tracks={tracks} className="lk-grid-layout" style={{ width: "100%", height: "100%" }}>
              <ParticipantTile />
            </GridLayout>
          </div>
        )}
      </div>
    </LayoutContextProvider>
  );
}

// PeerJoinWatcher must live inside <LiveKitRoom> so that the room context is
// available. It calls onPeerJoined once (and only once) when a remote
// participant appears.
function PeerJoinWatcher({ onPeerJoined }: { onPeerJoined?: () => void }) {
  const participants = useParticipants({ updateOnlyOn: [] });
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