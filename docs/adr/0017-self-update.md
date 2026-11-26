# ADR-0017: Self-update through a root-owned background job

- Status: Accepted (2026-11-27)
- Design: [Touch Grass self-update from GitHub Releases](../superpowers/specs/2026-11-27-touchgrass-self-update-design.md)

Touch Grass should let a fleet administrator select a published compatible GitHub release and request installation from the app. A separate root-owned systemd job, dispatched only through a helper under a root-owned parent directory, performs the checksum-verified install under the production cutover lock; the app only starts the bounded request and reads durable status. The existing GitHub Actions upgrade workflow remains the bootstrap and recovery path. No GitHub write credential is placed in the Touch Grass process.

This decision adds a privileged host integration and requires an operator-reviewed rollout. The job backs up SQLite before replacement. Automatic database rollback is forbidden because the new process might accept writes before a health failure; the backup is retained for reviewed recovery. The first feature release must be installed with the existing workflow before a later release can exercise the new button. Revisit if the product adopts a signed, platform-neutral updater or an external deployment controller.
