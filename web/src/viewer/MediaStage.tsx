import { useCallback, useEffect, useRef, useState } from 'react';
import type { Schema } from '../api/client';
import { Button } from '../components/Button';
import { videoKey } from './keys';
import { mediaName } from './labels';
function VideoMedia({ item, onError }: { item: Schema<'Item'>; onError: () => void }) {
  const video = useRef<HTMLVideoElement>(null);
  const activeSetup = useRef<AbortController | null>(null);
  const [autoplayBlocked, setAutoplayBlocked] = useState(false);
  const requestPlay = useCallback((element: HTMLVideoElement, setup: AbortController) => {
    let playback: Promise<void>;
    try { playback = element.play(); } catch (error) { playback = Promise.reject(error); }
    const current = () => !setup.signal.aborted && activeSetup.current === setup;
    void playback.then(
      () => { if (current()) setAutoplayBlocked(false); },
      () => { if (current()) setAutoplayBlocked(element.paused); },
    );
  }, []);
  const src = item.contentUrl!;
  useEffect(() => {
    const element = video.current!;
    const setup = new AbortController();
    activeSetup.current = setup;
    // StrictMode performs setup/cleanup/setup: reattach the immutable source
    // when that happens, and always stop reads/playback when leaving the item.
    element.setAttribute('src', src);
    element.volume = 0.5;
    requestPlay(element, setup);
    return () => { setup.abort(); element.pause(); element.removeAttribute('src'); element.load(); };
  }, [src, requestPlay]);
  return <><video ref={video} controls playsInline preload="metadata" poster={item.posterUrl} tabIndex={0} aria-label={mediaName(item)} data-testid="viewer-video" onError={onError} onPlaying={() => setAutoplayBlocked(false)}
    onKeyDownCapture={(event) => { if (document.activeElement === event.currentTarget) videoKey(event.nativeEvent, event.currentTarget); }}
    onKeyUpCapture={(event) => { if (document.activeElement === event.currentTarget) videoKey(event.nativeEvent, event.currentTarget); }}
  >Your browser does not support video.</video>{autoplayBlocked && <Button icon="play" label="Play video" className="primary viewer-play" onClick={() => {
    const element = video.current;
    const setup = activeSetup.current;
    if (element && setup && !setup.signal.aborted) { element.focus({ preventScroll: true }); requestPlay(element, setup); }
  }}>Play</Button>}</>;
}
export function MediaStage({ item, zoom = 1, rotation = 0 }: { item: Schema<'Item'>; zoom?: number; rotation?: number }) {
  const [failed, setFailed] = useState(false);
  const available = item.processingState === 'ready' && !!item.contentUrl;
  return <section className="viewer-main" aria-label="Media"><div className="viewer-stage" data-testid="viewer-stage" tabIndex={0}>
    {!available || failed ? <div className="viewer-unavailable" role="status"><p>Media is unavailable.</p>{failed && <Button className="secondary small" onClick={() => setFailed(false)}>Try again</Button>}</div>
      : item.mediaKind === 'Image' ? <img className="viewer-image" src={item.contentUrl} alt={mediaName(item)} data-testid="viewer-image" draggable={false} style={{ transform: `scale(${zoom}) rotate(${rotation}deg)` }} onError={() => setFailed(true)}/>
        : item.mediaKind === 'Video' ? <VideoMedia key={item.id} item={item} onError={() => setFailed(true)}/>
          : <a className="button secondary" href={item.contentUrl} download>Download media</a>}
  </div></section>;
}
