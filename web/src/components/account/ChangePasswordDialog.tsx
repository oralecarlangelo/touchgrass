import { toast } from 'sonner';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

import PasswordFields from './PasswordFields.tsx';

// Self-service password change from the account menu, for any role.
export default function ChangePasswordDialog({
  open,
  username,
  onClose,
}: {
  open: boolean;
  username: string;
  onClose: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Change password</DialogTitle>
          <DialogDescription>
            This device stays signed in. Everywhere else you are signed in as {username} is signed
            out.
          </DialogDescription>
        </DialogHeader>
        {open && (
          <PasswordFields
            idPrefix="change-password"
            username={username}
            submitLabel="Change password"
            onChanged={() => {
              toast.success('Password changed.');
              onClose();
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
