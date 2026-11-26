import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { fetchBuckets, fetchObjects, objectAction, isUnauthorized, type BucketConnection, type BucketObject, type ObjectPage } from '@/lib/api';
import { formatBytes, formatTime } from '@/lib/format';

export default function BucketManager({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [connection, setConnection] = useState<BucketConnection | null>(null);
  const [bucket, setBucket] = useState('');
  const [prefix, setPrefix] = useState('');
  const [search, setSearch] = useState('');
  const [after, setAfter] = useState('');
  const [page, setPage] = useState<ObjectPage | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const [folder, setFolder] = useState('');
  const [selected, setSelected] = useState<BucketObject | null>(null);
  const [confirmation, setConfirmation] = useState('');
  const sequence = useRef(0);
  const fail = useCallback((err: unknown) => {
    if (isUnauthorized(err)) onUnauthorized();
    else setError(err instanceof Error ? err.message : String(err));
  }, [onUnauthorized]);
  useEffect(() => {
    let active = true;
    void fetchBuckets().then(value => { if (active) { setConnection(value); setBucket(value.buckets[0] ?? ''); } }).catch(fail);
    return () => { active = false; };
  }, [fail]);
  useEffect(() => {
    const id = ++sequence.current;
    if (!bucket) return;
    setLoading(true); setError(''); setPage(null);
    void fetchObjects(bucket, prefix, after).then(value => { if (sequence.current === id) setPage(value); }).catch(err => { if (sequence.current === id) fail(err); }).finally(() => { if (sequence.current === id) setLoading(false); });
    return () => { sequence.current++; };
  }, [bucket, prefix, after, revision, fail]);
  function browse(value: string) { setPrefix(value); setSearch(value); setAfter(''); }
  async function perform(task: () => Promise<void>) {
    setBusy(true); setError('');
    try { await task(); } catch (err) { fail(err); } finally { setBusy(false); }
  }
  const directory = prefix.slice(0, prefix.lastIndexOf('/') + 1);
  async function upload(file: File) {
    if (file.size === 0 || file.size > 100 * 1024 * 1024) throw new Error('Choose a non-empty file up to 100 MiB.');
    const grant = await objectAction<{ url: string; key: string; fields: Record<string, string> }>({ action: 'upload', bucket, key: directory, filename: file.name });
    const data = new FormData();
    for (const [key, value] of Object.entries(grant.fields)) data.append(key, value);
    data.append('file', file);
    const response = await fetch(grant.url, { method: 'POST', body: data, credentials: 'omit' });
    if (!response.ok) throw new Error(`Storage rejected the upload (${response.status}).`);
    toast.success(`Uploaded ${grant.key}`); browse(directory); setRevision(value => value + 1);
  }
  return <Card><CardHeader><CardTitle>Bucket manager</CardTitle></CardHeader><CardContent className="space-y-4">
    {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
    {connection === null ? <p>Loading connection…</p> : !connection.configured ? <p className="text-muted-foreground text-sm">Connect an S3-compatible store using TOUCHGRASS_OBJECT_STORAGE_FILE on the server. Only explicitly allowed buckets will appear. See docs/storage-management.md.</p> : <>
      <div className="flex flex-wrap items-center gap-3"><label className="text-sm">Bucket <select className="bg-background rounded border p-2" value={bucket} disabled={busy} onChange={event => { setBucket(event.target.value); browse(''); }} aria-label="Bucket">{connection.buckets.map(name => <option key={name}>{name}</option>)}</select></label><span className="text-muted-foreground text-sm">{connection.writable ? 'Object writes enabled' : 'Read only'}</span><Button variant="outline" disabled={loading || busy} onClick={() => setRevision(value => value + 1)}>Refresh</Button></div>
      <form className="flex gap-2" onSubmit={event => { event.preventDefault(); browse(search); }}><Input aria-label="Object prefix search" placeholder="Search by key prefix, e.g. media/images/" value={search} onChange={event => setSearch(event.target.value)} disabled={busy} /><Button disabled={busy || loading}>Browse</Button><Button type="button" variant="outline" disabled={busy} onClick={() => browse('')}>Root</Button></form>
      {connection.writable && <div className="flex flex-wrap items-center gap-3">
        <label className="text-sm">Upload (up to 100 MiB)<input aria-label="Upload object" type="file" className="ml-2 max-w-64 text-sm" disabled={busy || loading} onChange={event => { const file = event.target.files?.[0]; if (file) void perform(() => upload(file)); event.target.value = ''; }} /></label>
        <form className="flex gap-2" onSubmit={event => { event.preventDefault(); void perform(async () => { await objectAction({ action: 'folder', bucket, key: directory + folder.replace(/\/+$/, '') + '/' }); setFolder(''); setRevision(value => value + 1); }); }}><Input aria-label="New folder name" placeholder="New folder name" value={folder} onChange={event => setFolder(event.target.value)} /><Button disabled={busy || loading || !folder.trim()}>Create folder</Button></form>
        <p className="text-muted-foreground w-full text-xs">New files receive a unique key under {directory || '/'} to avoid overwriting existing objects. Upload grants expire after five minutes.</p>
      </div>}
      {loading && <p role="status">Loading objects…</p>}
      {page && <><p className="text-muted-foreground text-xs">{page.objects.length} entries on this page · {formatBytes(page.objects.filter(item => !item.folder).reduce((sum, item) => sum + item.size, 0))} of files on this page. This is not the total bucket size.</p>
        <div className="max-h-96 overflow-auto"><table className="w-full text-left text-sm"><thead><tr><th className="p-2">Key</th><th className="p-2">Size</th><th className="p-2">Modified</th><th className="p-2">Actions</th></tr></thead><tbody>{page.objects.map(item => <tr className="border-t" key={item.key}><td className="max-w-96 break-all p-2">{item.folder ? <Button variant="link" disabled={busy} onClick={() => browse(item.key)}>{item.key}</Button> : <span className="font-mono text-xs">{item.key}</span>}</td><td className="whitespace-nowrap p-2">{item.folder ? 'Folder' : formatBytes(item.size)}</td><td className="p-2">{item.folder ? '—' : formatTime(item.modified)}</td><td className="p-2">{!item.folder && <div className="flex gap-2"><Button variant="outline" disabled={busy} onClick={() => void perform(async () => { const result = await objectAction<{ url: string }>({ action: 'download', bucket, key: item.key }); const link = document.createElement('a'); link.href = result.url; link.rel = 'noopener noreferrer'; link.target = '_blank'; link.click(); })}>Download</Button>{connection.writable && <Button variant="destructive" disabled={busy} onClick={() => { setSelected(item); setConfirmation(''); }}>Delete</Button>}</div>}</td></tr>)}</tbody></table>{page.objects.length === 0 && <p className="py-4">No objects match this prefix.</p>}</div>
        <div className="flex gap-2"><Button variant="outline" disabled={!after || busy || loading} onClick={() => setAfter('')}>First page</Button><Button variant="outline" disabled={!page.next || busy || loading} onClick={() => setAfter(page.next)}>Next page</Button></div>
      </>}
    </>}
    <Dialog open={selected !== null} onOpenChange={open => { if (!open && !busy) setSelected(null); }}><DialogContent><DialogHeader><DialogTitle>Delete this object?</DialogTitle><DialogDescription>This deletes only the selected key in {bucket} and may break application links to it. Non-versioned deletions cannot be undone. If versioning is enabled, older versions may remain and disk space may not be reclaimed. Type the full key to confirm: {selected?.key}</DialogDescription></DialogHeader><Input aria-label="Confirm object key" value={confirmation} onChange={event => setConfirmation(event.target.value)} disabled={busy} /><DialogFooter><Button variant="outline" disabled={busy} onClick={() => setSelected(null)}>Cancel</Button><Button variant="destructive" disabled={busy || !selected || confirmation !== selected.key} onClick={() => void perform(async () => { if (!selected) return; await objectAction({ action: 'delete', bucket, key: selected.key, confirmation }); setSelected(null); setRevision(value => value + 1); toast.success('Object delete completed.'); })}>{busy ? 'Deleting…' : 'Delete object'}</Button></DialogFooter></DialogContent></Dialog>
  </CardContent></Card>;
}
