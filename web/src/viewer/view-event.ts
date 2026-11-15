import { useCallback, useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api, ApiError, errorMessage, unwrap } from '../api/client';
import { acceptItem } from './cache';
interface Opening { id: number; eventId: string; startedAt: number; fired: boolean; inFlight: boolean }
export function useViewOpening(id: number, enabled: boolean) {
  const client = useQueryClient();
  const opening = useRef<Opening | null>(null);
  const [error, setError] = useState('');
  const submit = useCallback(() => {
    const current = opening.current;
    if (!current || current.inFlight || !current.fired) return;
    current.inFlight = true; setError('');
    void (async () => {
      try {
        for (let attempt = 0; attempt < 2; attempt++) {
          try {
            const item = unwrap(await api.POST('/api/v1/items/{id}/views', { params: { path: { id: current.id } }, body: { eventId: current.eventId } }));
            acceptItem(client, item); return;
          } catch (error) {
            if (attempt === 1 || (error instanceof ApiError && error.status < 500 && error.status !== 408)) throw error;
            await new Promise((resolve) => setTimeout(resolve, 500));
          }
        }
      } catch (error) { if (opening.current === current) setError(errorMessage(error)); }
      finally { current.inFlight = false; }
    })();
  }, [client]);
  useEffect(() => {
    if (!enabled) return;
    if (!opening.current || opening.current.id !== id) opening.current = { id, eventId: crypto.randomUUID(), startedAt: Date.now(), fired: false, inFlight: false };
    const current = opening.current;
    if (current.fired) return;
    const timer = setTimeout(() => { current.fired = true; submit(); }, Math.max(0, 3000 - (Date.now() - current.startedAt)));
    return () => clearTimeout(timer);
  }, [id, enabled, submit]);
  return { error, retry: submit };
}
