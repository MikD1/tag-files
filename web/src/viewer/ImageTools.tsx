import { useEffect, useRef, useState } from 'react';
import { Button } from '../components/Button';
import { Icon } from '../components/Icon';
export function ImageTools({ zoom, onZoom, onRotate, onReset }: { zoom: number; onZoom: (value: number) => void; onRotate: () => void; onReset: () => void }) {
  const [open, setOpen] = useState(false);
  const details = useRef<HTMLDetailsElement>(null);
  const summary = useRef<HTMLElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (event: PointerEvent) => { if (event.target instanceof Node && !details.current?.contains(event.target)) setOpen(false); };
    document.addEventListener('pointerdown', close);
    return () => document.removeEventListener('pointerdown', close);
  }, [open]);
  return <details ref={details} className="image-tools" open={open} onToggle={(event) => setOpen(event.currentTarget.open)} onKeyDown={(event) => {
    if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); setOpen(false); summary.current?.focus(); }
  }}><summary role="button" ref={summary} className="button ghost icon-button" aria-label="Image tools" title="Image tools" aria-controls="image-tools-panel" aria-expanded={open} data-testid="image-tools-toggle"><Icon name="adjust"/></summary>
    <div className="viewer-controls" id="image-tools-panel" data-testid="image-tools-panel" role="group" aria-label="Image tools">
      <Button icon="minus" label="Zoom out" className="ghost icon-button" disabled={zoom <= 0.25} onClick={() => onZoom(Math.max(0.25, zoom - 0.25))}/>
      <Button label="Reset zoom and rotation" className="ghost zoom-reset" onClick={onReset}>{Math.round(zoom * 100)}%</Button>
      <Button icon="plus" label="Zoom in" className="ghost icon-button" disabled={zoom >= 4} onClick={() => onZoom(Math.min(4, zoom + 0.25))}/>
      <Button icon="rotate" label="Rotate 90°" className="ghost icon-button" onClick={onRotate}/>
    </div>
  </details>;
}
