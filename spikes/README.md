# Phase 0 Spikes

Time-boxed experiments that answer the open questions in the ADRs before Phase 1 starts. Each spike keeps its code, its `RESULTS.md` and a way to rerun it.

- **Spikes are reference material, not product components.** They are not versioned under ADR-0015 and have no `VERSION` or `CHANGELOG.md`. Phase 1 code is written fresh in the real layout and may copy patterns from here.
- The results are folded into the ADRs and `docs/stack_info/final_stack.md`. The roadmap change log records when that happened.
- The dev box is not connected to the NAS, so NFS is mocked and **no throughput testing** is done (see final_stack → Testing → Storage mocking).

| Spike | Questions | ADRs |
|---|---|---|
| [`kopia-library/`](kopia-library/RESULTS.md) | Kopia used as a Go library; repository server plus per-agent ACLs; where client and server split the data work, and how deduplication behaves | 0002, 0004, 0007 |
| [`kopia-fidelity-nfs/`](kopia-fidelity-nfs/RESULTS.md) | Kopia metadata fidelity (extended attributes, ACLs, SELinux labels, ownership); SELinux relabeling on restore; mocked NFS (local directory plus a containerized NFS server, including an outage) | 0002, 0006 |
| [`temporal/`](temporal/RESULTS.md) | Temporal in Docker Compose on PostgreSQL 18 (separate databases); workflow-ID exclusivity; the schedule-trigger pattern; saga compensation | 0001, 0005, 0009, 0011 |
| [`huma-swagger/`](huma-swagger/RESULTS.md) | Swagger UI API docs through Huma; docs protection; four-part `info.version`; spec export; oasdiff breaking-change check; docs lint | 0015, final_stack → Control Plane |
