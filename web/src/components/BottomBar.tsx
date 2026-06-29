"use client";

import React from "react";
import { Track } from "livekit-client";
import {
  useTrackToggle,
  useDisconnectButton,
  MicIcon,
  MicDisabledIcon,
  CameraIcon,
  CameraDisabledIcon,
  ScreenShareIcon,
  ScreenShareStopIcon,
  LeaveIcon,
} from "@livekit/components-react";

const iconStyle: React.CSSProperties = { width: 22, height: 22 };

// Mirror of @livekit/components-core's ToggleSource (Camera | Microphone | ScreenShare).
// Not re-exported by the react package, so we restate it here.
type ToggleSource = Track.Source.Camera | Track.Source.Microphone | Track.Source.ScreenShare;

type ToggleButtonProps = {
  source: ToggleSource;
  onIcon: React.ReactNode;
  offIcon: React.ReactNode;
  label: string;
};

function ToggleButton({ source, onIcon, offIcon, label }: ToggleButtonProps) {
  const { buttonProps, enabled } = useTrackToggle({ source });

  const muted = source !== Track.Source.ScreenShare && !enabled;

  return (
    <button
      {...buttonProps}
      aria-label={label}
      title={label}
      className={
        "flex h-11 w-11 cursor-pointer items-center justify-center rounded-full border-none bg-transparent transition-colors " +
        (muted
          ? "bg-red-500 text-white hover:bg-red-600"
          : "text-white hover:bg-white/10")
      }
    >
      {enabled ? onIcon : offIcon}
    </button>
  );
}

export function BottomBar() {
  const { buttonProps } = useDisconnectButton({ stopTracks: true });

  return (
    <div className="flex h-16 w-full items-center justify-center gap-2 border-t border-white/10 bg-neutral-900 px-4">
      <ToggleButton
        source={Track.Source.Microphone}
        onIcon={<MicIcon style={iconStyle} />}
        offIcon={<MicDisabledIcon style={iconStyle} />}
        label="Microphone"
      />
      <ToggleButton
        source={Track.Source.Camera}
        onIcon={<CameraIcon style={iconStyle} />}
        offIcon={<CameraDisabledIcon style={iconStyle} />}
        label="Camera"
      />
      <ToggleButton
        source={Track.Source.ScreenShare}
        onIcon={<ScreenShareStopIcon style={iconStyle} />}
        offIcon={<ScreenShareIcon style={iconStyle} />}
        label="Screen share"
      />

      <div className="mx-1 h-8 w-px bg-white/20" />

      <button
        {...buttonProps}
        aria-label="Leave call"
        title="Leave call"
        className="flex h-11 w-11 cursor-pointer items-center justify-center rounded-full border-none bg-red-500 text-white transition-colors hover:bg-red-600"
      >
        <LeaveIcon style={iconStyle} />
      </button>
    </div>
  );
}
