import { Button } from '@/components/ui/button';
import EmptyState from './EmptyState.tsx';

// NoServicesAssigned is what a new scoped account sees until an admin
// grants it at least one service.
export default function NoServicesAssigned({
  username,
  onRetry,
}: {
  username: string;
  onRetry: () => void;
}) {
  return (
    <EmptyState
      title="No services assigned yet"
      body={`The account "${username}" has not been given access to any service. Ask an admin to grant the services you work on under Users, then check again.`}
      action={
        <Button variant="outline" size="sm" onClick={onRetry}>
          Check again
        </Button>
      }
    />
  );
}
