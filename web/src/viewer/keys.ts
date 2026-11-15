type Video = Pick<HTMLVideoElement, 'currentTime' | 'duration' | 'volume' | 'paused' | 'play' | 'pause'>;
/** Caller admits these keys only while the actual video owns keyboard focus. */
export function videoKey(event: KeyboardEvent, video: Video): boolean {
  if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.isComposing) return false;
  const space = event.key === ' ' || event.code === 'Space';
  if (!space && !['ArrowRight', 'ArrowLeft', 'ArrowUp', 'ArrowDown'].includes(event.key)) return false;
  event.preventDefault(); event.stopPropagation();
  // Own both phases before native media controls can toggle a second time.
  if (event.type === 'keyup' || (space && event.repeat)) return true;
  if (space) {
    if (video.paused) void video.play().catch(() => { /* Native controls remain available after a refused play. */ });
    else video.pause();
  } else if (event.key === 'ArrowRight') video.currentTime = Math.min(Number.isFinite(video.duration) && video.duration > 0 ? video.duration : Infinity, video.currentTime + 5);
  else if (event.key === 'ArrowLeft') video.currentTime = Math.max(0, video.currentTime - 3);
  else if (event.key === 'ArrowUp') video.volume = Math.min(1, video.volume + 0.1);
  else if (event.key === 'ArrowDown') video.volume = Math.max(0, video.volume - 0.1);
  return true;
}
