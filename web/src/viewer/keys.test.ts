import { describe, expect, it } from 'vitest';
import { videoKey } from './keys';
function player() {
  const calls: string[] = [];
  return { currentTime: 10, duration: 30, volume: 0.5, paused: true, play: async () => { calls.push('play'); }, pause: () => { calls.push('pause'); }, calls };
}
function event(key: string, options: KeyboardEventInit = {}) { return new KeyboardEvent('keydown', { key, cancelable: true, ...options }); }
describe('accepted video keys', () => {
  it('owns +5/-3 seek and +/-0.1 volume with exact boundaries', () => {
    const video = player();
    expect(videoKey(event('ArrowRight'), video)).toBe(true); expect(video.currentTime).toBe(15);
    videoKey(event('ArrowLeft'), video); expect(video.currentTime).toBe(12);
    videoKey(event('ArrowUp'), video); expect(video.volume).toBeCloseTo(0.6);
    videoKey(event('ArrowDown'), video); expect(video.volume).toBeCloseTo(0.5);
    video.currentTime = 29; videoKey(event('ArrowRight'), video); expect(video.currentTime).toBe(30);
    video.currentTime = 1; videoKey(event('ArrowLeft'), video); expect(video.currentTime).toBe(0);
    video.volume = 0.95; videoKey(event('ArrowUp'), video); expect(video.volume).toBe(1);
    video.volume = 0.01; videoKey(event('ArrowDown'), video); expect(video.volume).toBe(0);
  });
  it('prevents native Space on both phases without repeating the action on keyup or held Space', async () => {
    const video = player();
    const down = event(' '); videoKey(down, video); expect(down.defaultPrevented).toBe(true);
    const up = new KeyboardEvent('keyup', { key: ' ', cancelable: true }); videoKey(up, video); expect(up.defaultPrevented).toBe(true);
    videoKey(event(' ', { repeat: true }), video);
    expect(video.calls).toEqual(['play']);
    video.paused = false; videoKey(event(' '), video); expect(video.calls).toEqual(['play', 'pause']);
  });
  it.each(['metaKey', 'ctrlKey', 'altKey', 'shiftKey'] as const)('leaves %s modified shortcuts to the browser', (modifier) => {
    const video = player(); const key = event('ArrowRight', { [modifier]: true });
    expect(videoKey(key, video)).toBe(false); expect(key.defaultPrevented).toBe(false); expect(video.currentTime).toBe(10);
  });
  it('preserves browser zoom and safely handles a refused play promise and unknown duration', async () => {
    const video = player(); video.duration = NaN;
    video.play = () => Promise.reject(new Error('autoplay blocked'));
    videoKey(event(' '), video); await Promise.resolve();
    videoKey(event('ArrowRight'), video); expect(video.currentTime).toBe(15);
    expect(videoKey(event('+', { metaKey: true }), video)).toBe(false);
  });
});
