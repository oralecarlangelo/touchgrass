import { useEffect, useState, type ClipboardEvent, type KeyboardEvent } from 'react';
import { Lock, Plus } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { looksLikeDotenvPaste, looksSecret, validateEnvKey, validateEnvValue } from '@/lib/env.ts';

// EnvAddRow is the inline "add entry" row above the table. Pasting one or
// more KEY=VALUE lines into the key field hands the text to the paste sheet.
export default function EnvAddRow({
  serviceId,
  disabled,
  busy,
  serverError,
  prefillKey,
  existingKeys,
  onStage,
  onPaste,
}: {
  serviceId: string;
  disabled: boolean;
  busy: boolean;
  serverError: string | null;
  prefillKey: string | null;
  existingKeys: Set<string>;
  onStage: (key: string, value: string, isSecret: boolean) => void;
  onPaste: (text: string) => void;
}) {
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [secret, setSecret] = useState(false);
  const [secretTouched, setSecretTouched] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (prefillKey !== null && prefillKey !== '') {
      setKey(prefillKey);
      setSecret(looksSecret(prefillKey));
      setSecretTouched(false);
      setError(null);
    }
  }, [prefillKey]);

  function handleKeyChange(next: string): void {
    setKey(next);
    setError(null);
    if (!secretTouched) {
      setSecret(looksSecret(next));
    }
  }

  function handleKeyPaste(event: ClipboardEvent<HTMLInputElement>): void {
    const text = event.clipboardData.getData('text');

    if (looksLikeDotenvPaste(text)) {
      event.preventDefault();
      onPaste(text);
    }
  }

  function submit(): void {
    const trimmedKey = key.trim();
    const keyError = validateEnvKey(trimmedKey);

    if (keyError !== null) {
      setError(keyError);
      return;
    }

    const valueError = validateEnvValue(value);

    if (valueError !== null) {
      setError(valueError);
      return;
    }

    onStage(trimmedKey, value, secret);
    setKey('');
    setValue('');
    setSecret(false);
    setSecretTouched(false);
    setError(null);
  }

  function handleEnter(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.key === 'Enter' && !disabled && !busy) {
      event.preventDefault();
      submit();
    }
  }

  const overwrites = key.trim() !== '' && existingKeys.has(key.trim());
  const message = error ?? serverError;
  const keyId = `env-add-key-${serviceId}`;
  const valueId = `env-add-value-${serviceId}`;
  const secretId = `env-add-secret-${serviceId}`;

  return (
    <div className="border-border bg-muted/30 space-y-2 border-b px-4 py-3" aria-label="Add an entry">
      <div className="grid gap-2 md:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)_auto_auto] md:items-end">
        <div className="space-y-1">
          <Label htmlFor={keyId} className="text-xs">
            Key
          </Label>
          <Input
            id={keyId}
            value={key}
            disabled={disabled || busy}
            autoComplete="off"
            spellCheck={false}
            placeholder="DATABASE_URL — or paste .env lines"
            aria-invalid={error !== null && key.trim() !== '' && validateEnvKey(key.trim()) !== null}
            className="font-mono text-xs"
            onChange={(event) => handleKeyChange(event.target.value)}
            onPaste={handleKeyPaste}
            onKeyDown={handleEnter}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={valueId} className="text-xs">
            Value
          </Label>
          <Input
            id={valueId}
            type={secret ? 'password' : 'text'}
            value={value}
            disabled={disabled || busy}
            autoComplete="off"
            spellCheck={false}
            placeholder={secret ? 'Masked once staged' : 'postgres://…'}
            className="font-mono text-xs"
            onChange={(event) => {
              setValue(event.target.value);
              setError(null);
            }}
            onKeyDown={handleEnter}
          />
        </div>
        <div className="flex h-8 items-center gap-2">
          <Checkbox
            id={secretId}
            checked={secret}
            disabled={disabled || busy}
            onCheckedChange={(checked) => {
              setSecret(checked === true);
              setSecretTouched(true);
            }}
          />
          <Label htmlFor={secretId} className="text-xs font-normal">
            <Lock className="size-3" aria-hidden />
            Secret
          </Label>
        </div>
        <Button type="button" size="sm" disabled={disabled || busy} onClick={submit} aria-label="Stage this entry">
          <Plus aria-hidden />
          {busy ? 'Staging…' : 'Stage'}
        </Button>
      </div>
      {message !== null ? (
        <p role="alert" className="text-destructive text-xs">
          {message}
        </p>
      ) : overwrites ? (
        <p className="text-xs text-amber-700 dark:text-amber-300">
          {key.trim()} already exists. Staging replaces its value.
        </p>
      ) : (
        <p className="text-muted-foreground text-xs">
          Staging keeps the change here until you review and apply it. Paste a block of KEY=VALUE
          lines into the key field to stage several at once.
        </p>
      )}
    </div>
  );
}
