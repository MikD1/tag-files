import { useRef, useState } from 'react';
import { Modal, Select } from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { collectionsQuery } from '../api/queries';
import { errorMessage } from '../api/client';
import { Button } from '../components/Button';
import { Icon } from '../components/Icon';
import { getUploadCoordinator, useUploads } from './runtime';
import type { UploadCoordinator, UploadRow, UploadStatus } from './coordinator';
const labels: Record<UploadStatus, string> = {
  creating: 'Queued', receiving: 'Queued', completing: 'Processing', queued: 'Queued', processing: 'Processing', failed: 'Error',
  hashing: 'Checking file', uploading: 'Uploading', 'needs-file': 'Choose file', error: 'Error', cancelling: 'Cancelling',
};
function Progress({ value, name }: { value: number; name: string }) {
  const percent = Math.max(0, Math.min(100, Math.floor(value * 100)));
  return <><div className="progress-label"><span>{percent}%</span>{percent === 100 && <Icon name="check"/>}</div><div className="progress-track" role="progressbar" aria-label={`Progress for ${name}`} aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}><div className="progress-fill" style={{ width: `${percent}%` }}/></div></>;
}
function MediaName({ name, video = false }: { name: string; video?: boolean }) {
  return <><span className="upload-file-icon"><Icon name={video ? 'video' : 'image'}/></span><span className="upload-file-name"><strong>{name}</strong></span></>;
}
function rowDetail(row: UploadRow): string | undefined {
  if (row.error) return row.error;
  if (row.needsFile) return 'Choose the original file to continue.';
  if (row.session.state === 'failed') return 'Could not process this file. Retry or cancel the upload.';
  return undefined;
}
export function UploadsPage({ coordinator = getUploadCoordinator() }: { coordinator?: UploadCoordinator }) {
  const snapshot = useUploads(coordinator);
  const collections = useQuery(collectionsQuery);
  const input = useRef<HTMLInputElement>(null);
  const reselectInput = useRef<HTMLInputElement>(null);
  const reselectId = useRef<string | null>(null);
  const [chosen, setChosen] = useState<File[]>([]);
  const [collection, setCollection] = useState<string | null>(null);
  const [error, setError] = useState('');
  const failures = snapshot.rows.filter((row) => row.status === 'failed' || row.status === 'error').length;
  const total = snapshot.rows.length + snapshot.preparing.length;
  function action(work: Promise<void>) { setError(''); void work.catch((error: unknown) => setError(errorMessage(error))); }
  function upload() {
    const files = chosen;
    const collectionId = collection === null ? null : Number(collection);
    setChosen([]); setCollection(null);
    action(coordinator.add(files, collectionId));
  }
  function chooseOriginal(id: string) { reselectId.current = id; reselectInput.current?.click(); }
  const displayedError = error || snapshot.error;
  return <>
    <header className="page-header"><div><h1>Uploads</h1></div><div className="header-actions"><Button icon="upload" label="Add media" className="ghost icon-button" onClick={() => input.current?.click()}/></div></header>
    <input ref={input} type="file" accept="image/*,video/*" multiple hidden aria-label="Choose media files" data-testid="upload-input" onChange={(event) => {
      setChosen(Array.from(event.currentTarget.files ?? [])); event.currentTarget.value = ''; setCollection(null); setError('');
    }}/>
    <input ref={reselectInput} type="file" accept="image/*,video/*" hidden aria-label="Choose original file" onChange={(event) => {
      const file = event.currentTarget.files?.[0]; event.currentTarget.value = '';
      if (file && reselectId.current) action(coordinator.reselect(reselectId.current, file));
    }}/>
    <div className="upload-summary"><div className="summary-tile"><span>Total</span><strong data-testid="upload-total">{total}</strong></div><div className="summary-tile"><span>In progress</span><strong data-testid="upload-in-progress">{total - failures}</strong></div><div className="summary-tile failed"><span>Errors</span><strong data-testid="upload-errors">{failures}</strong></div></div>
    <section className="upload-queue" aria-label="Upload queue">
      {displayedError && <div role="alert" className="form-error"><p>{displayedError}</p>{snapshot.uncertainOperations > 0 ? <Button className="small secondary" onClick={() => action(coordinator.recoverRequests())}>Retry request</Button> : <Button className="small secondary" onClick={() => { setError(''); void coordinator.refresh(); }}>Refresh</Button>}</div>}
      {total > 0 ? <div className="table-scroll"><table className="data-table upload-table"><thead><tr><th>Media</th><th>Uploading</th><th>Status</th><th><span className="sr-only">Action</span></th></tr></thead><tbody>
        {snapshot.preparing.map((preparing) => <tr key={preparing.id} data-testid="upload-job"><td><MediaName name={preparing.filename}/></td><td><Progress value={preparing.progress} name={preparing.filename}/></td><td><span className="status-badge">{preparing.cancellable ? 'Checking file' : 'Queued'}</span></td><td><div className="upload-actions"><Button icon="close" label="Cancel upload" className="ghost icon-button" disabled={!preparing.cancellable} onClick={() => coordinator.cancelPreparation(preparing.id)}/></div></td></tr>)}
        {snapshot.rows.map((row) => <tr key={row.id} data-testid="upload-job" data-status={row.status}><td><MediaName name={row.session.filename} video={/\.(mp4|mov|m4v|mkv|avi|webm)$/i.test(row.session.filename)}/></td><td><Progress value={row.progress} name={row.session.filename}/></td><td><span className={`status-badge ${row.status === 'error' ? 'failed' : row.status}`}>{(row.status === 'processing' || row.status === 'completing') && <Icon name="clock"/>}{labels[row.status]}</span>{rowDetail(row) && <small className="upload-status-detail">{rowDetail(row)}</small>}</td><td><div className="upload-actions">
          {row.needsFile && <Button className="secondary small" icon="upload" onClick={() => chooseOriginal(row.id)}>Choose file</Button>}
          {row.canRetry && <Button className="secondary small" icon="retry" onClick={() => action(coordinator.retry(row.id))}>Retry</Button>}
          <Button icon="close" label="Cancel upload" className="ghost icon-button" disabled={row.status === 'cancelling'} onClick={() => action(coordinator.cancel(row.id))}/>
        </div></td></tr>)}
      </tbody></table></div> : <div className="upload-empty" data-testid="upload-empty" role="status">{snapshot.loading ? 'Loading…' : 'Queue is empty'}</div>}
    </section>
    <Modal opened={chosen.length > 0} onClose={() => setChosen([])} title="Upload media" closeButtonProps={{ 'aria-label': 'Close upload dialog' }} centered classNames={{ content: 'production-dialog', header: 'production-dialog-header' }}>
      <p>{chosen.length} {chosen.length === 1 ? 'file' : 'files'} selected</p>
      <Select label="Collection" placeholder="No collection" searchable clearable nothingFoundMessage="No collections found"
        value={collection} onChange={setCollection}
        data={(collections.data ?? []).map((entry) => ({ value: String(entry.id), label: entry.name }))}
        rightSection={<Icon name="chevrons-up-down" className="upload-collection-chevron"/>}
        renderOption={({ option, checked }) => <>{checked && <Icon name="check" className="upload-collection-check"/>}<span>{option.label}</span></>}/>
      {collections.error && <p role="alert" className="form-error">{errorMessage(collections.error)}</p>}
      <div className="dialog-actions"><Button className="secondary" onClick={() => setChosen([])}>Cancel</Button><Button className="primary" onClick={upload}>Upload</Button></div>
    </Modal>
  </>;
}
