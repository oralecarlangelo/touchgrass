import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';

import PasswordFields from './PasswordFields.tsx';

// The whole screen while an account holds a one-time password: the
// server refuses every other route, so there is nothing else to show.
export default function FirstLoginPassword({
  username,
  knownCurrent,
  onChanged,
  onLogout,
}: {
  username: string;
  knownCurrent: string;
  onChanged: () => void;
  onLogout: () => void;
}) {
  return (
    <div className="bg-background flex min-h-screen items-center justify-center px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Set your password</CardTitle>
          <CardDescription>
            <span className="text-foreground font-medium">{username}</span> is on a one-time
            password. Choose your own to continue — the one-time password stops working as soon as
            you save.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <PasswordFields
            idPrefix="first-login"
            username={username}
            knownCurrent={knownCurrent}
            submitLabel="Save and continue"
            onChanged={onChanged}
            secondaryAction={
              <Button type="button" variant="ghost" onClick={onLogout}>
                Log out
              </Button>
            }
          />
        </CardContent>
      </Card>
    </div>
  );
}
