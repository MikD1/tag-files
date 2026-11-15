// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { ApiError, type Schema } from './client';
import { ImmutableCommand } from './command';
const failure = (status: number) => new ApiError(new Response(null, { status }), { title: `Rejected ${status}`, status });
function receipt(operationId: string, state: Schema<'Operation'>['state'], result: Record<string, unknown> | null, httpStatus = 201): Schema<'Operation'> {
  return { operationId, state, httpStatus, result, createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 604800000).toISOString() };
}
function ids() { let next = 1; return () => `00000000-0000-4000-8000-${String(next++).padStart(12, '0')}`; }
describe('immutable form commands', () => {
  it('gives a corrected body a fresh key after a definitive422 rejection', async () => {
    const command = new ImmutableCommand<{ name: string }, { id: number }>({ uuid: ids(), lookup: async () => { throw failure(404); } });
    const writes: { body: { name: string }; key: string }[] = [];
    const send = async (body: { name: string }, key: string) => { writes.push({ body, key }); if (body.name === 'a_b') throw failure(422); return { id: 7 }; };
    await expect(command.submit({ name: 'a_b' }, send)).rejects.toMatchObject({ status: 422 });
    await expect(command.submit({ name: 'a-b' }, send)).resolves.toEqual({ id: 7 });
    expect(writes.map((write) => write.body.name)).toEqual(['a_b', 'a-b']);
    expect(writes[1]!.key).not.toBe(writes[0]!.key);
  });
  it('resolves a committed lost response without creating another entity', async () => {
    const stored = new Map<string, Schema<'Operation'>>();
    const command = new ImmutableCommand<{ name: string }, { id: number }>({ uuid: ids(), lookup: async (id) => stored.get(id)! });
    let writes = 0;
    const result = await command.submit({ name: 'saved' }, async (_body, key) => { writes++; stored.set(key, receipt(key, 'completed', { id: 7 })); throw new TypeError('reply lost'); });
    expect(result).toEqual({ id: 7 }); expect(writes).toBe(1);
  });
  it('replays the original key/body before considering later edits when operation lookup was unavailable', async () => {
    let offline = true;
    const command = new ImmutableCommand<{ tagIds: number[] }, { ids: number[] }>({ uuid: ids(), lookup: async () => { if (offline) throw new TypeError('offline'); throw failure(404); } });
    const writes: { ids: number[]; key: string }[] = [];
    const send = async (body: { tagIds: number[] }, key: string) => { writes.push({ ids: [...body.tagIds], key }); if (writes.length === 1) throw new TypeError('reply lost'); return { ids: body.tagIds }; };
    const original = { tagIds: [1, 2] };
    await expect(command.submit(original, send)).rejects.toThrow(/uncertain|resolve|previous/i);
    original.tagIds.push(3);
    offline = false;
    await expect(command.submit({ tagIds: [9] }, send)).resolves.toEqual({ ids: [1, 2] });
    expect(writes).toEqual([{ ids: [1, 2], key: '00000000-0000-4000-8000-000000000001' }, { ids: [1, 2], key: '00000000-0000-4000-8000-000000000001' }]);
  });
  it('allows a replacement only after the uncertain previous operation is proven failed', async () => {
    let offline = true;
    const command = new ImmutableCommand<{ name: string }, { id: number }>({ uuid: ids(), lookup: async (id) => { if (offline) throw new TypeError('offline'); return receipt(id, 'failed', { title: 'Invalid name', status: 422 }, 422); } });
    const writes: { name: string; key: string }[] = [];
    const send = async (body: { name: string }, key: string) => { writes.push({ name: body.name, key }); if (writes.length === 1) throw new TypeError('lost'); return { id: 8 }; };
    await expect(command.submit({ name: 'a_b' }, send)).rejects.toThrow();
    offline = false;
    await expect(command.submit({ name: 'a-b' }, send)).resolves.toEqual({ id: 8 });
    expect(writes[1]!.key).not.toBe(writes[0]!.key);
    expect(writes.map((write) => write.name)).toEqual(['a_b', 'a-b']);
  });
  it('does not resend or replace a known pending operation', async () => {
    let pending = false;
    const command = new ImmutableCommand<{ name: string }, { id: number }>({ uuid: ids(), lookup: async (id) => { if (!pending) throw new TypeError('offline'); return receipt(id, 'pending', null, 202); } });
    let writes = 0;
    const send = async () => { writes++; throw new TypeError('lost'); };
    await expect(command.submit({ name: 'first' }, send)).rejects.toThrow();
    pending = true;
    await expect(command.submit({ name: 'later' }, send)).rejects.toThrow(/pending|running|uncertain/i);
    expect(writes).toBe(1);
  });
});
