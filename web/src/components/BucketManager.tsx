import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { discoverBuckets, fetchBuckets, fetchObjects, objectAction, saveStorageConnection, isUnauthorized, type BucketConnection, type BucketObject, type ObjectPage } from '@/lib/api';
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
  const [endpoint, setEndpoint] = useState('http://127.0.0.1:9000');
  const [region, setRegion] = useState('us-east-1');
  const [accessKey, setAccessKey] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [discovered, setDiscovered] = useState<string[]>([]);
  const [discoveryComplete, setDiscoveryComplete] = useState(false);
  const [showSetup, setShowSetup] = useState(false);
  const [selectedBuckets, setSelectedBuckets] = useState<string[]>([]);
  const [writable, setWritable] = useState(false);
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
    const query = new URLSearchParams({ bucket, prefix: directory, filename: file.name });
    const response = await fetch(`/api/storage/content?${query}`, { method: 'PUT', body: file, credentials: 'same-origin' });
    if (!response.ok) throw new Error(`Storage rejected the upload (${response.status}).`);
    const result = await response.json() as { key: string };
    toast.success(`Uploaded ${result.key}`); browse(directory); setRevision(value => value + 1);
  }
  async function discover() {
    const result = await discoverBuckets({ endpoint, region, access_key: accessKey, secret_key: secretKey });
    setDiscovered(result.buckets); setSelectedBuckets(result.buckets); setDiscoveryComplete(true);
  }
  async function saveConnection() {
    const value = await saveStorageConnection({ endpoint, region, access_key: accessKey, secret_key: secretKey, buckets: selectedBuckets, writable });
    setSecretKey(''); setConnection(value); setBucket(value.buckets[0] ?? ''); setShowSetup(false); setDiscoveryComplete(false);
    setPrefix(''); setSearch(''); setAfter(''); setPage(null); setSelected(null); setRevision(value => value + 1);
  }
  return <Card><CardHeader><CardTitle>Bucket manager</CardTitle></CardHeader><CardContent className="space-y-4">
    {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
    {connection === null ? <p>Loading connection…</p> : (!connection.configured || showSetup) ? <form className="space-y-4" onSubmit={event => { event.preventDefault(); void perform(!discoveryComplete ? discover : saveConnection); }}>
      <div><p className="font-medium">Connect MinIO or S3-compatible storage</p><p className="text-muted-foreground text-sm">Credentials stay on this server and are never returned after setup. Use the S3 API address reachable by Touch Grass—for a host-installed MinIO this is usually http://127.0.0.1:9000. Object transfers remain authenticated through Touch Grass.</p></div>
      <div className="grid gap-3 md:grid-cols-2"><label className="text-sm">S3 API endpoint<Input value={endpoint} onChange={event => { setEndpoint(event.target.value); setDiscovered([]); setDiscoveryComplete(false); }} placeholder="http://127.0.0.1:9000" required /></label><label className="text-sm">Region<Input value={region} onChange={event => setRegion(event.target.value)} placeholder="us-east-1" /></label><label className="text-sm">Access key<Input autoComplete="username" value={accessKey} onChange={event => { setAccessKey(event.target.value); setDiscovered([]); setDiscoveryComplete(false); }} required /></label><label className="text-sm">Secret key<Input type="password" autoComplete="new-password" value={secretKey} onChange={event => { setSecretKey(event.target.value); setDiscovered([]); setDiscoveryComplete(false); }} required /></label></div>
      {discoveryComplete && discovered.length === 0 && <p role="status" className="text-muted-foreground text-sm">Connection succeeded, but these credentials cannot list any buckets. Grant bucket-list access or use credentials scoped to the buckets you want to manage.</p>}
      {discovered.length > 0 && <fieldset className="space-y-2"><legend className="font-medium">Allowed buckets</legend><p className="text-muted-foreground text-xs">Select only the buckets Touch Grass may browse.</p>{discovered.map(name => <label className="flex items-center gap-2 text-sm" key={name}><input type="checkbox" checked={selectedBuckets.includes(name)} onChange={event => setSelectedBuckets(value => event.target.checked ? [...value, name] : value.filter(item => item !== name))} />{name}</label>)}<label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={writable} onChange={event => setWritable(event.target.checked)} />Allow uploads, folders, and deletes</label></fieldset>}
      <div className="flex gap-2"><Button disabled={busy || !endpoint || !accessKey || !secretKey || (discoveryComplete && selectedBuckets.length === 0)}>{busy ? 'Checking…' : !discoveryComplete ? 'Discover buckets' : 'Save connection'}</Button>{(discoveryComplete || showSetup) && <Button type="button" variant="outline" disabled={busy} onClick={() => { setSecretKey(''); setAccessKey(''); setDiscovered([]); setDiscoveryComplete(false); if (showSetup) setShowSetup(false); }}>Cancel</Button>}</div>
    </form> : <>
      <div className="flex flex-wrap items-center gap-3"><label className="text-sm">Bucket <select className="bg-background rounded border p-2" value={bucket} disabled={busy} onChange={event => { setBucket(event.target.value); browse(''); }} aria-label="Bucket">{connection.buckets.map(name => <option key={name}>{name}</option>)}</select></label><span className="text-muted-foreground text-sm">{connection.writable ? 'Object writes enabled' : 'Read only'}</span><Button variant="outline" disabled={loading || busy} onClick={() => setRevision(value => value + 1)}>Refresh</Button><Button variant="outline" disabled={busy} onClick={() => setShowSetup(true)}>Edit connection</Button></div>
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
