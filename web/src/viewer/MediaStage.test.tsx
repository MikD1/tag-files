import { StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MediaStage } from './MediaStage';
import type { Schema } from '../api/client';
const base: Schema<'Item'> = { id: 1, mediaKind: 'Image', description: 'private metadata', uploadedAt: '2026-01-01T00:00:00Z', tags: [], collectionId: null, isFavorite: false, viewCount: 0, lastViewAt: null, durationSeconds: null, processingState: 'ready', version: 1, contentUrl: '/media/primary' };
beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe('native inline media', () => {
  it('requests autoplay at volume0.5 and retains an explicit Play action when the browser refuses it', async () => {
    const play = vi.mocked(HTMLMediaElement.prototype.play);
    play.mockRejectedValueOnce(new DOMException('Autoplay requires a gesture', 'NotAllowedError'));
    render(<MediaStage item={{ ...base, mediaKind: 'Video' }}/>);
    const video = screen.getByLabelText('Video without tags') as HTMLVideoElement;
    expect(video.volume).toBe(0.5);
    expect(play).toHaveBeenCalledTimes(1);
    expect(video.controls).toBe(true);
    const start = await screen.findByRole('button', { name: 'Play video' });
    await userEvent.setup().click(start);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Play video' })).not.toBeInTheDocument());
    expect(play).toHaveBeenCalledTimes(2);
  });
  it('ignores a late autoplay refusal from retired StrictMode setup after the active setup succeeds', async () => {
    let refuseRetired: (reason: unknown) => void = () => { throw new Error('autoplay was not requested'); };
    const play = vi.mocked(HTMLMediaElement.prototype.play);
    play.mockImplementationOnce(() => new Promise((_resolve, reject) => { refuseRetired = reject; }));
    const view = render(<StrictMode><MediaStage item={{ ...base, mediaKind: 'Video' }}/></StrictMode>);
    expect(play).toHaveBeenCalledTimes(2);
    const video = screen.getByLabelText('Video without tags') as HTMLVideoElement;
    expect(video.volume).toBe(0.5);
    await act(async () => { refuseRetired(new DOMException('Retired autoplay rejected', 'AbortError')); await Promise.resolve(); });
    expect(screen.queryByRole('button', { name: 'Play video' })).not.toBeInTheDocument();
    view.unmount();
    expect(video).not.toHaveAttribute('src');
  });

  it('keeps the contained image transform and omits private metadata', () => {
    render(<MediaStage item={base} zoom={1.25} rotation={90}/>);
    expect(screen.getByRole('img')).toHaveStyle({ transform: 'scale(1.25) rotate(90deg)' });
    expect(screen.queryByText('private metadata')).not.toBeInTheDocument();
  });
  it('keeps native controls without a poster and accepts exact keys only when video is focused', () => {
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined);
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
    render(<MediaStage item={{ ...base, mediaKind: 'Video' }}/>);
    const video = screen.getByLabelText('Video without tags') as HTMLVideoElement;
    expect(video.controls).toBe(true); expect(video.preload).toBe('metadata'); expect(video.hasAttribute('poster')).toBe(false);
    video.currentTime = 10;
    fireEvent.keyDown(video, { key: 'ArrowRight' }); expect(video.currentTime).toBe(10);
    video.focus(); fireEvent.keyDown(video, { key: 'ArrowRight' }); expect(video.currentTime).toBe(15);
    fireEvent.keyUp(video, { key: 'ArrowRight' }); expect(video.currentTime).toBe(15);
    fireEvent.keyDown(video, { key: 'ArrowLeft' }); expect(video.currentTime).toBe(12);
  });
  it('pauses and detaches the old source on item change and unmount', () => {
    const paused: HTMLMediaElement[] = [];
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(function (this: HTMLMediaElement) { paused.push(this); });
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
    const view = render(<MediaStage item={{ ...base, mediaKind: 'Video' }}/>);
    const old = screen.getByLabelText('Video without tags');
    view.rerender(<MediaStage item={{ ...base, id: 2, mediaKind: 'Video', contentUrl: '/media/next' }}/>);
    expect(paused).toContain(old); expect(old).not.toHaveAttribute('src');
    const current = screen.getByLabelText('Video without tags'); view.unmount();
    expect(paused).toContain(current); expect(current).not.toHaveAttribute('src');
  });
});
