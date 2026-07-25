# Next feature preface: create-guard rollout (post-023)

Roll the two 023 recipes across the audited remainder (see
`specs/023-fix-duplicate-create/research.md` table):

1. **Lost-result adoption guard** (ambiguity markers → list-and-match →
   adopt/create/refuse): Server (priority — billable), Network, Firewall, Cdn.
   Identity keys per kind recorded in the audit table.
2. **Stomp defense** (extname-vs-status.upstreamID contradiction → park +
   `ExternalNameConflict`): KubernetesCluster, Router, Server, Network,
   FloatingIP, Firewall, Cdn — mechanical replication of the nodepool
   implementation.

Justified-absent kinds (upstream-idempotent identity): S3Bucket, S3User,
ContainerRegistry(+Repository), Addon; risk-accepted: Project, SSHKey.
